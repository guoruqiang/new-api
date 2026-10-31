package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/risk"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRiskManagementTest(t *testing.T) (*gorm.DB, string, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousSnapshot := model.DB, risk.CurrentSnapshot()
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, previousLog)
	t.Cleanup(func() {
		risk.InstallSnapshot(previousSnapshot)
		model.DB = previousDB
		common.SetDatabaseTypes(previousMain, previousLog)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(model.RiskModels()...))
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, model.ReconcileRiskGroupNamespaces([]string{"default", "other"}))
	require.NoError(t, risk.SeedBaselinePolicy())
	require.NoError(t, risk.LoadInitialSnapshot())
	rows, err := model.ListRiskGroupNamespaces(true)
	require.NoError(t, err)
	groups := map[string]string{}
	for _, row := range rows {
		groups[row.GroupName] = row.Namespace
	}
	return db, groups["default"], groups["other"]
}

func riskManagementRequest(t *testing.T, handler gin.HandlerFunc, method, path string, params gin.Params, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := common.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(encoded))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	c.Set("id", 1)
	c.Set("role", common.RoleRootUser)
	handler(c)
	return w
}

func TestRiskConfigPublicationValidationAndConflict(t *testing.T) {
	db, namespace, other := setupRiskManagementTest(t)
	defaults := riskManagementRequest(t, GetRiskDefaults, http.MethodGet, "/api/risk/defaults", nil, nil)
	require.Equal(t, http.StatusOK, defaults.Code)
	assert.Contains(t, defaults.Body.String(), `"policy_save_activation":true`)
	cfg := risk.DefaultConfigPayload()
	cfg.Enabled = true
	cfg.DefaultBinding.Mode = risk.ModeEnforce
	body := map[string]any{"expected_revision": 0, "enabled": true, "default_binding": cfg.DefaultBinding, "exempt_namespaces": []string{}}
	w := riskManagementRequest(t, UpdateRiskConfig, http.MethodPut, "/api/risk/config", nil, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"revision":1`)
	require.True(t, risk.CurrentSnapshot().Enabled)
	w = riskManagementRequest(t, UpdateRiskConfig, http.MethodPut, "/api/risk/config", nil, body)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "revision_conflict")
	for _, tc := range []struct {
		name     string
		exempt   []string
		bypass   []string
		bindings []risk.GroupBinding
	}{
		{name: "unknown", exempt: []string{"missing"}},
		{name: "auto", exempt: []string{"auto"}},
		{name: "duplicate", exempt: []string{namespace, namespace}},
		{name: "exempt and bound", exempt: []string{namespace}, bindings: []risk.GroupBinding{{Namespace: namespace, PolicyKey: risk.BaselinePolicyKey, PolicyRevision: 1, Mode: risk.ModeObserve}}},
		{name: "legacy bypass unknown", bypass: []string{"missing"}},
		{name: "legacy bypass auto", bypass: []string{"auto"}},
		{name: "legacy bypass and bound", bypass: []string{namespace}, bindings: []risk.GroupBinding{{Namespace: namespace, PolicyKey: risk.BaselinePolicyKey, PolicyRevision: 1, Mode: risk.ModeObserve}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body["expected_revision"], body["exempt_namespaces"], body["bypass_namespaces"], body["group_bindings"] = 1, tc.exempt, tc.bypass, tc.bindings
			w := riskManagementRequest(t, UpdateRiskConfig, http.MethodPut, "/api/risk/config", nil, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"key":`)
			view, err := risk.LatestConfig()
			require.NoError(t, err)
			assert.Equal(t, int64(1), view.Revision)
			assert.Empty(t, view.Payload.ExemptNamespaces)
		})
	}
	policy := map[string]any{"policy_key": "published", "name": "Published", "base_revision": 0, "payload": risk.DefaultPolicyPayload()}
	w = riskManagementRequest(t, CreateRiskPolicy, http.MethodPost, "/api/risk/policies", nil, policy)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"revision":1`)
	w = riskManagementRequest(t, CreateRiskPolicy, http.MethodPost, "/api/risk/policies", nil, policy)
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "revision_conflict")
	policy["base_revision"] = 1
	w = riskManagementRequest(t, CreateRiskPolicy, http.MethodPost, "/api/risk/policies", nil, policy)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"revision":2`)

	cfg.Enabled = false
	cfg.ExemptNamespaces = []string{other}
	cfg.GroupBindings = []risk.GroupBinding{{Namespace: namespace, PolicyKey: "published", PolicyRevision: 1, Mode: risk.ModeObserve}}
	cfg.Retention = risk.Retention{EventsDays: 17, CountersDays: 29, ViolationsDays: 31}
	_, problems, err := risk.PublishConfig(1, cfg, 1)
	require.NoError(t, err)
	require.Empty(t, problems)
	payload := risk.DefaultPolicyPayload()
	payload.SkipGPTModels = true
	policy["base_revision"], policy["payload"] = 2, payload
	policy["activation"] = map[string]any{"expected_config_revision": 2}
	w = riskManagementRequest(t, CreateRiskPolicy, http.MethodPost, "/api/risk/policies", nil, policy)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"config_revision":3`)
	view, err := risk.LatestConfig()
	require.NoError(t, err)
	assert.False(t, view.Payload.Enabled, "saving alone preserves the global switch")
	assert.Equal(t, risk.Binding{PolicyKey: "published", PolicyRevision: 3, Mode: risk.ModeEnforce}, view.Payload.DefaultBinding)
	assert.Equal(t, []risk.GroupBinding{{Namespace: namespace, PolicyKey: "published", PolicyRevision: 3, Mode: risk.ModeObserve}}, view.Payload.GroupBindings)
	assert.Equal(t, cfg.ExemptNamespaces, view.Payload.ExemptNamespaces)
	assert.Equal(t, cfg.Retention, view.Payload.Retention)

	policy["base_revision"] = 3
	policy["activation"] = map[string]any{"expected_config_revision": 3, "enable": true}
	w = riskManagementRequest(t, CreateRiskPolicy, http.MethodPost, "/api/risk/policies", nil, policy)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"config_revision":4`)
	assert.Contains(t, w.Body.String(), `"revision":3`)
	require.True(t, risk.CurrentSnapshot().Enabled)
	assert.True(t, risk.CurrentSnapshot().Default.Policy.Payload.SkipGPTModels)
	assert.Equal(t, risk.ModeEnforce, risk.CurrentSnapshot().Default.Mode)
	assert.Equal(t, risk.ModeObserve, risk.CurrentSnapshot().Groups[namespace].Mode)
	policy["activation"] = map[string]any{"expected_config_revision": 4, "enable": true}
	w = riskManagementRequest(t, CreateRiskPolicy, http.MethodPost, "/api/risk/policies", nil, policy)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"config_revision":4`)
	var count int64
	require.NoError(t, db.Model(&model.RiskPolicy{}).Where("policy_key = ?", "published").Count(&count).Error)
	assert.EqualValues(t, 3, count, "repeated saving without changes must not create revisions")
}

func TestRiskProviderResponsesNeverExposeKeysAndUpdatesPreserveKeys(t *testing.T) {
	_, _, _ = setupRiskManagementTest(t)
	key := "risk-test-secret-not-for-responses"
	body := risk.ProviderInput{Name: "test", ProviderType: "jev", BaseURL: "https://example.test", APIKeys: &[]string{key}}
	w := riskManagementRequest(t, CreateRiskProvider, http.MethodPost, "/api/risk/providers", nil, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool
		Data    risk.ProviderView
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success, w.Body.String())
	assert.Equal(t, 1, response.Data.ApiKeyCount)
	assert.NotContains(t, w.Body.String(), key)
	params := gin.Params{{Key: "id", Value: fmt.Sprint(response.Data.Id)}}
	body.APIKeys, body.Name = nil, "renamed"
	w = riskManagementRequest(t, UpdateRiskProvider, http.MethodPut, "/api/risk/providers/1", params, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	stored, err := model.GetRiskProvider(response.Data.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{key}, stored.KeyLines())
	w = riskManagementRequest(t, GetRiskProviders, http.MethodGet, "/api/risk/providers", nil, nil)
	assert.NotContains(t, w.Body.String(), key)
	assert.NotContains(t, w.Body.String(), `"api_keys"`)
	body.APIKeys = &[]string{}
	w = riskManagementRequest(t, UpdateRiskProvider, http.MethodPut, "/api/risk/providers/1", params, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	stored, err = model.GetRiskProvider(response.Data.Id)
	require.NoError(t, err)
	assert.Empty(t, stored.KeyLines())
}

func TestRiskPreviewDoesNotWriteRecords(t *testing.T) {
	db, namespace, _ := setupRiskManagementTest(t)
	payload := risk.DefaultPolicyPayload()
	payload.Distill.Enabled, payload.Model.Enabled = false, false
	payload.HardRegex.Rules = []risk.HardRule{{Category: "custom", Source: "riskblock", Enabled: true}}
	row, problems, err := risk.CreatePolicyRevision("preview", "Preview", 0, payload, 1)
	require.NoError(t, err)
	require.Empty(t, problems)
	cfg := risk.DefaultConfigPayload()
	cfg.Enabled = true
	cfg.DefaultBinding = risk.Binding{PolicyKey: row.PolicyKey, PolicyRevision: row.Revision, Mode: risk.ModeEnforce}
	cfg.ExemptNamespaces = []string{namespace}
	_, problems, err = risk.PublishConfig(0, cfg, 1)
	require.NoError(t, err)
	require.Empty(t, problems)
	for _, viaAuto := range []bool{false, true} {
		w := riskManagementRequest(t, PreviewRisk, http.MethodPost, "/api/risk/preview", nil, risk.PreviewRequest{GroupName: "default", ViaAuto: viaAuto, Body: `{"model":"test","messages":[{"role":"user","content":"riskblock"}]}`})
		var response struct {
			Success bool
			Data    risk.PreviewResult
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		require.True(t, response.Success, w.Body.String())
		assert.Equal(t, !viaAuto, response.Data.Exempt)
		assert.Equal(t, viaAuto, response.Data.Audited)
		if viaAuto {
			assert.Equal(t, risk.OutcomePass, response.Data.Decision.Outcome)
			assert.Equal(t, risk.ReasonModelDisabled, response.Data.Decision.Reason, "a keyword alone is not a confirmed violation")
		}
	}
	for _, table := range []any{&model.RiskViolation{}, &model.RiskEvent{}, &model.RiskCounter{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		assert.Zero(t, count, "preview must not write %T", table)
	}
}

func TestRiskViolationHTTPReviewAndBan(t *testing.T) {
	db, _, _ := setupRiskManagementTest(t)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	user := &model.User{Username: "violator", AffCode: "violator", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
	admin := &model.User{Username: "reviewer", AffCode: "reviewer", Password: "password123", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, db.Create(admin).Error)
	violation := &model.RiskViolation{RequestId: "req", ContentHash: "hash", UserId: user.Id, Mode: risk.ModeEnforce, Category: "nsfw", RequestBody: `{"messages":[]}`}
	_, _, err := model.RecordRiskViolation(violation, 20, false, false, common.GetTimestamp())
	require.NoError(t, err)
	adminViolation := &model.RiskViolation{RequestId: "admin-req", ContentHash: "hash", UserId: admin.Id, Mode: risk.ModeEnforce, Category: "nsfw"}
	_, _, err = model.RecordRiskViolation(adminViolation, 20, false, false, common.GetTimestamp())
	require.NoError(t, err)
	id := gin.Params{{Key: "id", Value: strconv.Itoa(violation.Id)}}

	w := riskManagementRequest(t, GetRiskViolations, http.MethodGet, "/api/risk/violations?status=pending", nil, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"request_id":"req"`)
	assert.NotContains(t, w.Body.String(), "request_body\":", "lists never carry evidence")
	w = riskManagementRequest(t, GetRiskViolation, http.MethodGet, "/api/risk/violations/x", id, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"score":80`)
	assert.Contains(t, w.Body.String(), `"request_body":"{\"messages\":[]}"`)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	for _, tc := range []struct {
		name, action, note string
		ok                 bool
		contains           string
	}{
		{"a note is required", "revoke", "", false, "A note of at most 500 characters is required"},
		{"unknown action", "delete", "note", false, ""},
		{"revoke refunds the deduction", "revoke", "false positive", true, `"score":100`},
		{"a revoked violation cannot be revoked again", "revoke", "again", false, "no longer available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := riskManagementRequest(t, ResolveRiskViolation, http.MethodPost, "/api/risk/violations/x/resolve", id, map[string]string{"action": tc.action, "note": tc.note})
			assert.Equal(t, tc.ok, strings.Contains(w.Body.String(), `"success":true`), w.Body.String())
			assert.Contains(t, w.Body.String(), tc.contains)
		})
	}

	require.NoError(t, db.Model(admin).Update("status", common.UserStatusDisabled).Error)
	for _, tc := range []struct {
		name, action string
		violation    *model.RiskViolation
		ok           bool
		status       int
	}{
		{"ban disables the violator's account", "ban", violation, true, common.UserStatusDisabled},
		{"unban enables the account", "unban", violation, true, common.UserStatusEnabled},
		{"administrator accounts cannot be re-enabled here", "unban", adminViolation, false, common.UserStatusDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := riskManagementRequest(t, BanRiskViolationUser, http.MethodPost, "/api/risk/violations/x/ban", gin.Params{{Key: "id", Value: strconv.Itoa(tc.violation.Id)}}, map[string]string{"action": tc.action})
			assert.Equal(t, tc.ok, strings.Contains(w.Body.String(), `"success":true`), w.Body.String())
			stored, err := model.GetUserById(tc.violation.UserId, true)
			require.NoError(t, err)
			assert.Equal(t, tc.status, stored.Status)
		})
	}
}
