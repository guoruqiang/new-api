package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
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
		{name: "bypass unknown", bypass: []string{"missing"}},
		{name: "bypass auto", bypass: []string{"auto"}},
		{name: "bypass duplicate", bypass: []string{namespace, namespace}},
		{name: "bypass and exempt", exempt: []string{namespace}, bypass: []string{namespace}},
		{name: "bypass and bound", bypass: []string{namespace}, bindings: []risk.GroupBinding{{Namespace: namespace, PolicyKey: risk.BaselinePolicyKey, PolicyRevision: 1, Mode: risk.ModeObserve}}},
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
			assert.Empty(t, view.Payload.BypassNamespaces)
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
	cfg.Retention = risk.Retention{EventsDays: 17, CountersDays: 29}
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

func TestRiskPreviewAndScopedRecordActions(t *testing.T) {
	db, namespace, other := setupRiskManagementTest(t)
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
		w := riskManagementRequest(t, PreviewRisk, http.MethodPost, "/api/risk/preview", nil, risk.PreviewRequest{GroupName: "default", ViaAuto: viaAuto, UserID: 9, Body: `{"model":"test","messages":[{"role":"user","content":"riskblock"}]}`})
		var response struct {
			Success bool
			Data    risk.PreviewResult
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		require.True(t, response.Success, w.Body.String())
		assert.Equal(t, !viaAuto, response.Data.Exempt)
		assert.Equal(t, viaAuto, response.Data.Audited)
		if viaAuto {
			assert.Equal(t, risk.OutcomeBlock, response.Data.Decision.Outcome)
		}
	}
	for _, table := range []any{&model.RiskSubjectBlock{}, &model.RiskRefusal{}, &model.RiskEvent{}, &model.RiskCounter{}} {
		var count int64
		require.NoError(t, db.Model(table).Count(&count).Error)
		assert.Zero(t, count, "preview must not write %T", table)
	}
	for _, ns := range []string{namespace, other} {
		require.NoError(t, model.RememberRiskRefusal(&model.RiskRefusal{Namespace: ns, UserId: 9, Model: "test", Fingerprint: "fingerprint", FirstSeenAt: 1, LastSeenAt: 1}))
		require.NoError(t, model.BlockRiskSubject(&model.RiskSubjectBlock{Namespace: ns, SubjectType: "user", SubjectValue: "9", LastSeenAt: 1}))
	}
	w := riskManagementRequest(t, ClearRiskRefusals, http.MethodPost, "/api/risk/refusals/clear", nil, riskClearRequest{Namespace: namespace})
	assert.Contains(t, w.Body.String(), `"success":false`)
	w = riskManagementRequest(t, ClearRiskRefusals, http.MethodPost, "/api/risk/refusals/clear", nil, riskClearRequest{Namespace: namespace, UserID: -1, Confirm: true})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	_, count, err := model.ListRiskRefusals(model.RiskListFilter{Namespace: namespace}, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "invalid user filter must not clear the entire group")
	w = riskManagementRequest(t, ClearRiskRefusals, http.MethodPost, "/api/risk/refusals/clear", nil, riskClearRequest{Namespace: namespace, Confirm: true})
	assert.Contains(t, w.Body.String(), `"removed":1`)
	w = riskManagementRequest(t, ClearRiskBlocks, http.MethodPost, "/api/risk/blocks/clear", nil, riskClearRequest{Namespace: namespace, UserID: 9, Confirm: true})
	assert.Equal(t, http.StatusBadRequest, w.Code, "group block clearing must not silently ignore a user filter")
	current, err := model.GetActiveRiskBlock(namespace, "user", "9", common.GetTimestamp())
	require.NoError(t, err)
	require.NotNil(t, current)
	w = riskManagementRequest(t, ClearRiskBlocks, http.MethodPost, "/api/risk/blocks/clear", nil, riskClearRequest{Namespace: namespace, Confirm: true})
	assert.Contains(t, w.Body.String(), `"released":1`)
	remaining, total, err := model.ListRiskRefusals(model.RiskListFilter{Namespace: other}, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, remaining, 1)
	block, err := model.GetActiveRiskBlock(other, "user", "9", common.GetTimestamp())
	require.NoError(t, err)
	assert.NotNil(t, block)
}

// Exercise real HTTP requests through the production risk gate and then the
// management handlers. No request is sent to a business upstream.
func TestRiskReviewHTTPConfirmationAndRestrictions(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, namespace, other := setupRiskManagementTest(t)
	payload := risk.DefaultPolicyPayload()
	payload.Distill.Enabled, payload.Model.Enabled = false, false
	payload.HardRegex.Rules = []risk.HardRule{
		{Category: "nsfw", Source: "risk-nsfw", Enabled: true, Action: risk.RuleActionBlock},
		{Category: "custom", Source: "risk-general", Enabled: true, Action: risk.RuleActionBlock},
	}
	policyRevision, configRevision := 0, int64(0)
	publish := func(mode string) {
		t.Helper()
		policy, problems, err := risk.CreatePolicyRevision("review-http", "Review HTTP", policyRevision, payload, 1)
		require.NoError(t, err)
		require.Empty(t, problems)
		policyRevision = policy.Revision
		cfg := risk.DefaultConfigPayload()
		cfg.Enabled = true
		cfg.DefaultBinding = risk.Binding{PolicyKey: policy.PolicyKey, PolicyRevision: policy.Revision, Mode: mode}
		configRevision, problems, err = risk.PublishConfig(configRevision, cfg, 1)
		require.NoError(t, err)
		require.Empty(t, problems)
	}
	publish(risk.ModeEnforce)
	router := gin.New()
	router.POST("/v1/responses", func(c *gin.Context) {
		group := c.GetHeader("X-Test-Group")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		info := &relaycommon.RelayInfo{UserId: 77, TokenId: 9, UserGroup: group, OriginModelName: "test", RequestId: c.GetHeader("X-Test-Request"), RelayFormat: types.RelayFormatOpenAIResponses, RelayMode: relayconstant.RelayModeResponses}
		decision := risk.CheckAttempt(c, info, risk.AttemptInput{Phase: risk.PhaseHTTPFirst, ChannelID: 1, ChannelType: constant.ChannelTypeOpenAI, Body: body})
		if decision != nil {
			c.JSON(decision.StatusCode, gin.H{"code": decision.GetErrorCode()})
			return
		}
		c.Status(http.StatusNoContent)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	post := func(id, group, modelName, input string, status int) {
		t.Helper()
		body, err := common.Marshal(map[string]any{"model": modelName, "input": input})
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Request", id)
		req.Header.Set("X-Test-Group", group)
		res, err := server.Client().Do(req)
		require.NoError(t, err)
		response, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, status, res.StatusCode, string(response))
	}
	candidate := func(id string) model.RiskReview {
		t.Helper()
		rows, total, err := model.ListRiskReviews(model.RiskReviewFilter{RequestId: id}, 0, 10)
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		return rows[0]
	}
	resolve := func(row model.RiskReview, action, category, note string, wantStatus int) model.RiskReviewResult {
		t.Helper()
		w := riskManagementRequest(t, ResolveRiskReview, http.MethodPost, "/api/risk/reviews/resolve", gin.Params{{Key: "id", Value: fmt.Sprint(row.Id)}}, map[string]any{"revision": row.Revision, "action": action, "category": category, "note": note})
		require.Equal(t, wantStatus, w.Code, w.Body.String())
		var result struct{ Data model.RiskReviewResult }
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result))
		return result.Data
	}
	post("first", "default", "test", "risk-nsfw first", http.StatusForbidden)
	first := candidate("first")
	assert.Equal(t, model.RiskReviewPending, first.Status)
	assert.Zero(t, first.Points)
	post("first", "default", "test", "risk-nsfw first", http.StatusForbidden)
	post("client-retry", "default", "other-model", "risk-nsfw first", http.StatusForbidden)
	_, total, err := model.ListRiskReviews(model.RiskReviewFilter{}, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total, "client and server retries must not create new punishable incidents")
	post("benign-before", "default", "test", "hello", http.StatusNoContent)
	list := riskManagementRequest(t, GetRiskReviews, http.MethodGet, "/api/risk/reviews?status=pending", nil, nil)
	assert.NotContains(t, list.Body.String(), "risk-nsfw first", "lists must not include stored evidence")
	detail := riskManagementRequest(t, GetRiskReview, http.MethodGet, "/api/risk/reviews/1", gin.Params{{Key: "id", Value: fmt.Sprint(first.Id)}}, nil)
	assert.Contains(t, detail.Body.String(), "risk-nsfw first")
	resolve(first, "confirm", "nsfw", "", http.StatusBadRequest)
	confirmed := resolve(first, "confirm", "nsfw", "Verified the current user's intent", http.StatusOK)
	assert.Equal(t, 3, confirmed.Score.Score)
	assert.Nil(t, confirmed.Block)
	repeated := resolve(first, "confirm", "nsfw", "Verified the current user's intent", http.StatusOK)
	assert.Equal(t, 3, repeated.Score.Score)
	post("second", "default", "test", "risk-nsfw second", http.StatusForbidden)
	second := resolve(candidate("second"), "confirm", "nsfw", "Independent second incident", http.StatusOK)
	assert.Equal(t, 6, second.Score.Score)
	assert.Nil(t, second.Block, "automatic restrictions are off by default even at the threshold")
	post("benign-at-threshold", "default", "test", "hello", http.StatusNoContent)
	payload.Behavior.AutoBlockEnabled = true
	payload.Refusal.Enabled, payload.Refusal.SubjectBlockEnabled = false, false
	publish(risk.ModeEnforce)
	post("third", "default", "test", "risk-general third", http.StatusForbidden)
	third := resolve(candidate("third"), "confirm", "custom", "Verified a lower-weight incident", http.StatusOK)
	assert.Equal(t, 7, third.Score.Score)
	require.NotNil(t, third.Block)
	assert.Equal(t, "token", third.Block.SubjectType)
	assert.Equal(t, "9", third.Block.SubjectValue)
	assert.EqualValues(t, 30*60, third.Block.ExpiresAt-third.Block.LastSeenAt)
	post("restricted", "default", "test", "hello", http.StatusForbidden)
	post("other-group", "other", "test", "hello", http.StatusForbidden)
	assert.NotEqual(t, namespace, other)
	_, beforeRevoke, err := model.ListRiskReviews(model.RiskReviewFilter{}, 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 3, beforeRevoke, "a block replay is not another violation")
	revoked := resolve(*confirmed.Review, "revoke", "nsfw", "Context was misunderstood", http.StatusOK)
	assert.Equal(t, 4, revoked.Score.Score)
	post("after-revoke", "default", "test", "hello", http.StatusNoContent)
	resolve(first, "confirm", "nsfw", "Stale confirmation", http.StatusConflict)
	publish(risk.ModeObserve)
	post("observation", "default", "test", "risk-nsfw observation", http.StatusNoContent)
	observed := candidate("observation")
	publish(risk.ModeEnforce)
	observation := resolve(observed, "confirm", "nsfw", "Observation stays non-punitive", http.StatusOK)
	assert.Zero(t, observation.Review.Points)
	assert.Equal(t, 4, observation.Score.Score)
	payload.Evidence.StoreReviewEvidence = false
	publish(risk.ModeEnforce)
	post("no-evidence", "default", "test", "risk-nsfw no evidence", http.StatusForbidden)
	noEvidence := candidate("no-evidence")
	resolve(noEvidence, "confirm", "nsfw", "Insufficient evidence", http.StatusBadRequest)
	dismissed := resolve(noEvidence, "dismiss", "nsfw", "Cannot establish user intent", http.StatusOK)
	assert.Equal(t, model.RiskReviewDismissed, dismissed.Review.Status)
	truncated, _, err := model.CreateRiskReviewCandidate(&model.RiskReview{RequestId: "truncated", Namespace: namespace, UserId: 77, Mode: risk.ModeEnforce, Evidence: "Context only", EvidenceTruncated: true}, payload.Behavior.Policy(), common.GetTimestamp())
	require.NoError(t, err)
	resolve(*truncated, "confirm", "nsfw", "Missing the actual triggering evidence", http.StatusBadRequest)
	payload.Behavior.Enabled = false
	publish(risk.ModeEnforce)
	post("scoring-disabled", "default", "test", "risk-nsfw disabled", http.StatusForbidden)
	var count int64
	require.NoError(t, db.Model(&model.RiskReview{}).Where("request_id = ?", "scoring-disabled").Count(&count).Error)
	assert.EqualValues(t, 1, count, "disabling behavior penalties must not discard a violating request's review evidence")
}

func TestRiskManualTokenRestrictionRequiresEvidenceAndPreservesOtherTokens(t *testing.T) {
	db, namespace, _ := setupRiskManagementTest(t)
	now := common.GetTimestamp()
	makeReview := func(id string, tokenID int) *model.RiskReview {
		t.Helper()
		row, _, err := model.CreateRiskReviewCandidate(&model.RiskReview{
			RequestId: id, Namespace: namespace, UserId: 77, TokenId: tokenID,
			Mode: risk.ModeEnforce, Category: "nsfw", Evidence: "flagged review text",
			OriginalRequest: "[user] flagged original text", Coverage: "full",
			OriginalRequestHash: "request-hash", ConfigRevision: risk.CurrentSnapshot().ConfigRevision,
		}, risk.DefaultPolicyPayload().Behavior.Policy(), now)
		require.NoError(t, err)
		return row
	}
	row := makeReview("manual-ban", 901)
	body := map[string]any{"revision": row.Revision, "action": "ban", "category": "nsfw", "note": "Verified the complete request", "ttl_minutes": 0}
	action := func(row *model.RiskReview, body map[string]any) *httptest.ResponseRecorder {
		return riskManagementRequest(t, ResolveRiskReview, http.MethodPost, "/api/risk/reviews/resolve", gin.Params{{Key: "id", Value: fmt.Sprint(row.Id)}}, body)
	}
	result := action(row, body)
	require.Equal(t, http.StatusOK, result.Code, result.Body.String())
	block, err := model.GetActiveRiskTokenBlock(77, 901, now)
	require.NoError(t, err)
	require.NotNil(t, block)
	assert.Zero(t, block.ExpiresAt)
	unrelated, err := model.GetActiveRiskTokenBlock(77, 902, now)
	require.NoError(t, err)
	assert.Nil(t, unrelated)
	result = action(row, body)
	require.Equal(t, http.StatusOK, result.Code, "retry must be idempotent: %s", result.Body.String())
	require.NoError(t, db.First(row, row.Id).Error)
	result = action(row, map[string]any{"revision": row.Revision, "action": "revoke", "category": "nsfw", "note": "Appeal accepted"})
	require.Equal(t, http.StatusOK, result.Code, result.Body.String())
	block, err = model.GetActiveRiskTokenBlock(77, 901, now)
	require.NoError(t, err)
	assert.Nil(t, block)
	for _, tc := range []struct {
		name      string
		token     int
		mode      string
		original  string
		truncated bool
		duration  any
	}{
		{"missing token", 0, risk.ModeEnforce, "content", false, 30},
		{"observe", 902, risk.ModeObserve, "content", false, 30},
		{"missing request", 903, risk.ModeEnforce, "", false, 30},
		{"truncated request", 904, risk.ModeEnforce, "content", true, 30},
		{"negative duration", 905, risk.ModeEnforce, "content", false, -1},
		{"missing duration", 906, risk.ModeEnforce, "content", false, nil},
		{"excessive duration", 907, risk.ModeEnforce, "content", false, 1441},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := makeReview(tc.name, tc.token)
			require.NoError(t, db.Model(record).Updates(map[string]any{"mode": tc.mode, "original_request": tc.original, "original_request_truncated": tc.truncated}).Error)
			body := map[string]any{"revision": record.Revision, "action": "ban", "category": "nsfw", "note": "Review completed", "ttl_minutes": tc.duration}
			response := action(record, body)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			var blocks int64
			require.NoError(t, db.Model(&model.RiskSubjectBlock{}).Where("subject_type = ? AND subject_value = ? AND released_at = 0", "token", fmt.Sprint(tc.token)).Count(&blocks).Error)
			assert.Zero(t, blocks)
		})
	}
}

func TestRiskTieredHTTPModelAndTokenEnforcement(t *testing.T) {
	require.NoError(t, i18n.Init())
	db, namespace, _ := setupRiskManagementTest(t)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			State     string         `json:"state"`
			Questions map[string]any `json:"questions"`
		}
		if err := common.DecodeJson(r.Body, &body); err != nil {
			http.Error(w, "bad payload", 400)
			return
		}
		answers := map[string]any{}
		for key := range body.Questions {
			score := 0.99
			if key == "nsfw" && strings.Contains(body.State, "flagged") {
				score = 0.01
			}
			answers[key] = map[string]any{"noul": score}
		}
		encoded, err := common.Marshal(map[string]any{"answers": answers})
		if err != nil {
			http.Error(w, "encode failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(provider.Close)
	keys := []string{"test-provider-key"}
	saved, problems, err := risk.SaveProvider(0, risk.ProviderInput{Name: "tiered-http", ProviderType: "jev", BaseURL: provider.URL, Model: "test-audit", APIKeys: &keys})
	require.NoError(t, err)
	require.Empty(t, problems)
	payload := risk.DefaultPolicyPayload()
	payload.Distill.Enabled = false
	payload.HardRegex.Rules = []risk.HardRule{
		{Category: "nsfw", Source: "risk-nsfw", Enabled: true, Action: risk.RuleActionReview},
		{Category: "custom", Source: "weak", Enabled: true, Action: risk.RuleActionSignal},
	}
	payload.Model.Enabled, payload.Model.ProviderID, payload.Model.Model = true, saved.Id, "test-audit"
	payload.Behavior.AutoConfirmEnabled, payload.Behavior.AutoBlockEnabled = true, true
	policy, problems, err := risk.CreatePolicyRevision("tiered-http", "Tiered HTTP", 0, payload, 1)
	require.NoError(t, err)
	require.Empty(t, problems)
	cfg := risk.DefaultConfigPayload()
	cfg.Enabled = true
	cfg.DefaultBinding = risk.Binding{PolicyKey: policy.PolicyKey, PolicyRevision: policy.Revision, Mode: risk.ModeEnforce}
	_, problems, err = risk.PublishConfig(0, cfg, 1)
	require.NoError(t, err)
	require.Empty(t, problems)
	router := gin.New()
	router.POST("/v1/responses", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(400)
			return
		}
		tokenID, _ := strconv.Atoi(c.GetHeader("X-Test-Token"))
		group := c.GetHeader("X-Test-Group")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
		info := &relaycommon.RelayInfo{UserId: 88, TokenId: tokenID, UserGroup: group, OriginModelName: "test", RequestId: c.GetHeader("X-Test-Request"), RelayFormat: types.RelayFormatOpenAIResponses, RelayMode: relayconstant.RelayModeResponses}
		verdict := risk.CheckAttempt(c, info, risk.AttemptInput{Phase: risk.PhaseHTTPFirst, ChannelID: 1, ChannelType: constant.ChannelTypeOpenAI, Body: body})
		if verdict != nil {
			c.JSON(verdict.StatusCode, gin.H{"code": verdict.GetErrorCode()})
			return
		}
		c.Status(http.StatusNoContent)
	})
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)
	post := func(id, text, group string, tokenID, status int) {
		t.Helper()
		encoded, err := common.Marshal(map[string]any{"model": "test", "input": text})
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/responses", bytes.NewReader(encoded))
		require.NoError(t, err)
		req.Header.Set("X-Test-Request", id)
		req.Header.Set("X-Test-Token", strconv.Itoa(tokenID))
		req.Header.Set("X-Test-Group", group)
		req.Header.Set("Authorization", "Bearer transport-credential-not-evidence")
		res, err := gateway.Client().Do(req)
		require.NoError(t, err)
		received, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, status, res.StatusCode, string(received))
	}
	post("ordinary", "normal request body never stored", "default", 31, http.StatusNoContent)
	assert.Zero(t, calls.Load())
	post("weak-1", "weak first", "default", 31, http.StatusNoContent)
	post("weak-2", "weak second", "default", 31, http.StatusNoContent)
	assert.Zero(t, calls.Load())
	post("weak-3", "weak third", "default", 31, http.StatusNoContent)
	assert.EqualValues(t, 1, calls.Load())
	post("after-safe", "another normal request", "default", 31, http.StatusNoContent)
	assert.EqualValues(t, 1, calls.Load(), "consumed hints must not send all later requests to the model")
	var count int64
	require.NoError(t, db.Model(&model.RiskReview{}).Count(&count).Error)
	assert.Zero(t, count, "safe model verdicts must not create stored request evidence")
	post("bad-1", "risk-nsfw flagged first", "default", 31, http.StatusForbidden)
	post("bad-1-retry", "risk-nsfw flagged first", "default", 31, http.StatusForbidden)
	rows, total, err := model.ListRiskReviews(model.RiskReviewFilter{TokenId: 31}, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, model.RiskReviewConfirmed, rows[0].Status)
	assert.Equal(t, 3, rows[0].Points)
	post("bad-2", "risk-nsfw flagged second", "default", 31, http.StatusForbidden)
	block, err := model.GetActiveRiskTokenBlock(88, 31, common.GetTimestamp())
	require.NoError(t, err)
	require.NotNil(t, block)
	assert.Positive(t, block.ExpiresAt)
	post("blocked-other-group", "ordinary", "other", 31, http.StatusForbidden)
	post("other-token", "ordinary", "default", 32, http.StatusNoContent)
	rows, total, err = model.ListRiskReviews(model.RiskReviewFilter{TokenId: 31}, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	for _, summary := range rows {
		row, err := model.GetRiskReview(summary.Id)
		require.NoError(t, err)
		assert.Contains(t, row.OriginalRequest, "flagged")
		assert.NotContains(t, row.OriginalRequest, "transport-credential-not-evidence")
		assert.Equal(t, "pending", row.NotificationStatus)
		assert.Equal(t, "model", row.JudgmentSource)
	}
	score, err := model.GetRiskTokenBehaviorScore(namespace, 88, 31, payload.Behavior.Policy(), common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, 6, score.Score)
}
