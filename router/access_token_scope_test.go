package router

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/service/risk"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// accessTokenExemptRoutes are /api routes that never reach dashboard
// authentication, so a personal access token has no rule to satisfy there.
var accessTokenExemptRoutes = []string{
	// Anonymous pages and setup.
	"GET /api/setup",
	"POST /api/setup",
	"GET /api/status",
	"GET /api/uptime/status",
	"GET /api/notice",
	"GET /api/user-agreement",
	"GET /api/privacy-policy",
	"GET /api/about",
	"GET /api/home_page_content",
	"GET /api/ratio_config",
	"GET /api/user/groups",

	// Anonymous account entry points.
	"GET /api/verification",
	"GET /api/reset_password",
	"POST /api/user/reset",
	"POST /api/user/register",
	"GET /api/user/login/encryption-key",
	"POST /api/user/login",
	"POST /api/user/login/2fa",
	"POST /api/user/login/verify",
	"POST /api/user/login/passkey/begin",
	"POST /api/user/login/passkey/finish",
	"POST /api/user/passkey/login/begin",
	"POST /api/user/passkey/login/finish",
	"GET /api/oauth/wechat",
	"GET /api/oauth/telegram/login",
	"GET /api/oauth/telegram/bind/:flow_token",

	// Browser session cookie endpoints.
	"POST /api/user/auth/refresh",
	"POST /api/user/auth/logout",

	// Payment callbacks.
	"POST /api/stripe/webhook",
	"POST /api/creem/webhook",
	"POST /api/waffo/webhook",
	"POST /api/waffo-pancake/webhook/:env",
	"GET /api/user/epay/notify",
	"POST /api/user/epay/notify",
	"GET /api/subscription/epay/notify",
	"POST /api/subscription/epay/notify",
	"GET /api/subscription/epay/return",
	"POST /api/subscription/epay/return",

	// API key authentication (TokenAuthReadOnly).
	"GET /api/usage/token/",
	"GET /api/log/token",

	// OAuth provider access tokens use their own scope checks.
	"GET /api/v1/oauth/tokens",
	"GET /api/v1/oauth/balance",
}

// accessTokenHelperRoutes are declared through handlePermissionRoute outside
// channelPermissionRoutes.
var accessTokenHelperRoutes = map[string]authz.Permission{
	"GET /api/task_plugin_options":                    authz.TaskPluginBind,
	"GET /api/audit":                                  authz.AuditRead,
	"GET /api/risk/overview":                          authz.RiskRead,
	"GET /api/risk/defaults":                          authz.RiskRead,
	"GET /api/risk/config":                            authz.RiskRead,
	"PUT /api/risk/config":                            authz.RiskWrite,
	"GET /api/risk/groups":                            authz.RiskRead,
	"GET /api/risk/policies":                          authz.RiskRead,
	"POST /api/risk/policies":                         authz.RiskWrite,
	"GET /api/risk/policies/:key/revisions":           authz.RiskRead,
	"GET /api/risk/policies/:key/revisions/:revision": authz.RiskRead,
	"GET /api/risk/providers":                         authz.RiskRead,
	"POST /api/risk/providers":                        authz.RiskWrite,
	"PUT /api/risk/providers/:id":                     authz.RiskWrite,
	"GET /api/risk/events":                            authz.RiskRead,
	"GET /api/risk/events/:id":                        authz.RiskRead,
	"GET /api/risk/violations":                        authz.RiskRead,
	"GET /api/risk/violations/:id":                    authz.RiskRead,
	"POST /api/risk/violations/:id/resolve":           authz.RiskWrite,
	"POST /api/risk/users/:id/ban":                    authz.RiskUnblock,
	"POST /api/risk/preview":                          authz.RiskWrite,
	"POST /api/risk/providers/models":                 authz.RiskWrite,
}

func TestRiskRoutesRequireAccountPermissionAndScopedLiveToken(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousLog := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousMaster := common.RedisEnabled, common.IsMasterNode
	previousRateLimit, previousCritical := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable
	previousSecret, previousSnapshot := common.SessionSecret, risk.CurrentSnapshot()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled, common.IsMasterNode = false, true
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = false, false
	common.SessionSecret = "risk-review-route-session-test"
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled, common.IsMasterNode = previousRedis, previousMaster
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = previousRateLimit, previousCritical
		common.SessionSecret = previousSecret
		risk.InstallSnapshot(previousSnapshot)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserAccessToken{}, &model.UserSession{}, &model.Option{}, &model.AuditLog{}, &model.CasbinRule{}, &model.AuthzRole{}))
	require.NoError(t, db.AutoMigrate(model.RiskModels()...))
	require.NoError(t, authz.Init(db))
	admin := &model.User{Username: "risk-route-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "risk-route-admin", AuthVersion: 1}
	root := &model.User{Username: "risk-route-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "risk-route-root", AuthVersion: 1}
	require.NoError(t, db.Create(admin).Error)
	require.NoError(t, db.Create(root).Error)
	newToken := func(userID int, expires int64, scopes ...string) (string, *model.UserAccessToken) {
		suffix, err := common.GenerateRandomCharsKey(43)
		require.NoError(t, err)
		raw := model.AccessTokenPrefix + suffix
		pat := &model.UserAccessToken{Name: "risk test", TokenHash: model.AccessTokenFingerprint(raw), TokenHint: model.AccessTokenHint(raw), ExpiresAt: expires}
		require.NoError(t, pat.SetScopes(scopes))
		require.NoError(t, model.CreateUserAccessToken(userID, pat, service.AccessTokenMaxPerUser))
		return raw, pat
	}
	adminRead, _ := newToken(admin.Id, 0, "risk:read")
	rootRead, _ := newToken(root.Id, 0, "risk:read")
	rootWrite, _ := newToken(root.Id, 0, "risk:write")
	rootUnblock, _ := newToken(root.Id, 0, "risk:unblock")
	rootWriteUnblock, _ := newToken(root.Id, 0, "risk:write", "risk:unblock")
	adminWriteUnblock, _ := newToken(admin.Id, 0, "risk:write", "risk:unblock")
	wrongScope, _ := newToken(root.Id, 0, "profile:read")
	expired, _ := newToken(root.Id, time.Now().Unix()-1, "risk:read")
	revoked, revokedRow := newToken(root.Id, 0, "risk:read")
	require.NoError(t, db.Delete(revokedRow).Error)
	engine := newAccessTokenScopeTestEngine(t)
	request := func(method, path, raw, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if raw != "" {
			req.Header.Set("Authorization", "Bearer "+raw)
		}
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}
	for _, tc := range []struct {
		name, token string
		status      int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"root reader", rootRead, http.StatusOK},
		{"root missing token scope", wrongScope, http.StatusForbidden},
		{"admin denied by default", adminRead, http.StatusForbidden},
		{"expired", expired, http.StatusUnauthorized},
		{"revoked", revoked, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := request(http.MethodGet, "/api/risk/defaults", tc.token, "")
			assert.Equal(t, tc.status, response.Code, response.Body.String())
		})
	}
	for key := range accessTokenHelperRoutes {
		method, path, _ := strings.Cut(key, " ")
		if !strings.HasPrefix(path, "/api/risk/") {
			continue
		}
		path = strings.NewReplacer(":revision", "1", ":key", "baseline", ":id", "1").Replace(path)
		response := request(method, path, wrongScope, `{"api_keys":["provider-secret"],"text":"private-risk-input"}`)
		assert.Equal(t, http.StatusForbidden, response.Code, key+" "+response.Body.String())
		assert.Contains(t, response.Body.String(), "ACCESS_TOKEN_SCOPE_DENIED", key)
		if method != http.MethodGet {
			assert.Equal(t, http.StatusForbidden, request(method, path, rootRead, "{}").Code, key)
		}
	}
	require.NoError(t, authz.SetUserPermissions(admin.Id, authz.PermissionsMap{authz.ResourceRisk: {authz.ActionRead: true}}))
	assert.Equal(t, http.StatusOK, request(http.MethodGet, "/api/risk/defaults", adminRead, "").Code)
	require.NoError(t, authz.SetUserPermissions(admin.Id, authz.PermissionsMap{authz.ResourceRisk: {authz.ActionRead: false}}))
	assert.Equal(t, http.StatusForbidden, request(http.MethodGet, "/api/risk/defaults", adminRead, "").Code, "token scope cannot retain revoked account permission")
	assert.Equal(t, http.StatusForbidden, request(http.MethodPost, "/api/risk/users/1/ban", rootWrite, `{"action":"ban","note":"x"}`).Code, "configuration permission does not ban accounts")
	assert.Equal(t, http.StatusForbidden, request(http.MethodPut, "/api/risk/config", rootUnblock, "{}").Code, "unblock permission does not publish configuration")
	now := time.Now().Unix()
	session := &model.UserSession{
		SID: "risk-review-browser-session", UserID: admin.Id, Version: 1, UserAuthVersion: admin.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "risk-session-refresh-hash", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600,
	}
	require.NoError(t, model.CreateUserSession(session))
	sessionToken, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: admin.Id, SessionID: session.SID, UserAuthVersion: session.UserAuthVersion, SessionVersion: session.Version})
	require.NoError(t, err)
	for i, tc := range []struct {
		name, token string
		unblock     bool
		status      int
	}{
		{"root PAT needs unblock scope", rootWrite, false, http.StatusForbidden},
		{"root PAT with unblock scope", rootUnblock, false, http.StatusOK},
		{"admin PAT also needs account unblock", adminWriteUnblock, false, http.StatusForbidden},
		{"admin PAT with both grants", adminWriteUnblock, true, http.StatusOK},
		{"browser session needs account unblock", sessionToken, false, http.StatusForbidden},
		{"browser session with the grant", sessionToken, true, http.StatusOK},
		{"browser session loses revoked account permission", sessionToken, false, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, authz.SetUserPermissions(admin.Id, authz.PermissionsMap{authz.ResourceRisk: {authz.ActionWrite: true, authz.ActionUnblock: tc.unblock}}))
			target := &model.User{Username: "risk-target-" + strconv.Itoa(i), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "risk-target-" + strconv.Itoa(i), AuthVersion: 1}
			require.NoError(t, db.Create(target).Error)
			response := request(http.MethodPost, "/api/risk/users/"+strconv.Itoa(target.Id)+"/ban", tc.token, `{"action":"ban","note":"Human reviewed the original request"}`)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.NoError(t, db.First(target, target.Id).Error)
			if tc.status == http.StatusOK {
				assert.Contains(t, response.Body.String(), `"success":true`)
				assert.Equal(t, common.UserStatusDisabled, target.Status)
			} else {
				assert.Equal(t, common.UserStatusEnabled, target.Status, "a denied ban must not disable the account")
			}
		})
	}
	created := request(http.MethodPost, "/api/risk/providers", rootWrite, `{"name":"audit-provider","provider_type":"jev","base_url":"https://example.test","api_keys":["provider-secret"]}`)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	require.Contains(t, created.Body.String(), `"success":true`)
	assert.NotContains(t, created.Body.String(), "provider-secret")
	var providerAudit model.AuditLog
	require.NoError(t, db.Where("action = ?", "risk.provider_create").First(&providerAudit).Error)
	assert.True(t, providerAudit.Success)
	assert.Equal(t, root.Id, providerAudit.UserId)
	var logs []model.AuditLog
	require.NoError(t, db.Find(&logs).Error)
	require.NotEmpty(t, logs)
	encoded, err := common.Marshal(logs)
	require.NoError(t, err)
	for _, secret := range []string{adminRead, rootRead, rootWrite, rootUnblock, rootWriteUnblock, adminWriteUnblock, sessionToken, wrongScope, expired, revoked, "provider-secret", "private-risk-input"} {
		assert.NotContains(t, string(encoded), secret)
	}
}

func newAccessTokenScopeTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// A permission route that is also in the static table panics here.
	require.NotPanics(t, func() {
		SetApiRouter(engine)
		SetOAuthRouter(engine)
		SetDashboardRouter(engine)
		SetRelayRouter(engine)
		SetTaskPluginProtocolRouter(engine)
		SetVideoRouter(engine)
		SetTaskRouter(engine)
	})
	return engine
}

func TestAccessTokenRouteRulesCoverEveryDashboardRoute(t *testing.T) {
	engine := newAccessTokenScopeTestEngine(t)
	exempt := map[string]bool{}
	for _, key := range accessTokenExemptRoutes {
		exempt[key] = true
	}
	registered := map[string]bool{}
	panel := map[string]bool{}
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/") && !strings.HasPrefix(route.Path, "/pg/") {
			continue
		}
		key := route.Method + " " + route.Path
		registered[key] = true
		if !exempt[key] {
			panel[key] = true
		}
	}
	for _, key := range accessTokenExemptRoutes {
		assert.True(t, registered[key], "exempt route %s is no longer registered", key)
	}

	declared := map[string]int{}
	for _, key := range middleware.AccessTokenRouteRuleKeys() {
		declared[key]++
	}
	for key := range panel {
		assert.Equal(t, 1, declared[key], "panel route %s must be declared exactly once", key)
	}
	for key := range declared {
		assert.True(t, panel[key], "declared route %s is not a registered panel route", key)
	}

	permissionRoutes := map[string]authz.Permission{}
	for _, route := range channelPermissionRoutes {
		permissionRoutes[route.method+" "+joinPaths("/api/channel", route.path)] = route.permission
	}
	for key, permission := range accessTokenHelperRoutes {
		permissionRoutes[key] = permission
	}
	for key, permission := range permissionRoutes {
		rule, ok := middleware.AccessTokenRouteRule(key)
		require.True(t, ok, key)
		assert.Equal(t, "scope", rule.Kind(), key)
		assert.Equal(t, service.AccessTokenScopeOf(permission), rule.Scope(), key)
	}
}

func TestAccessTokenCatalogCoversEveryPermission(t *testing.T) {
	catalog := service.AccessTokenCatalog(1, common.RoleRootUser)
	scopes := map[string]bool{}
	resources := map[string]int{}
	for _, group := range catalog {
		for _, resource := range group.Resources {
			resources[resource.Resource]++
			for _, action := range resource.Actions {
				scopes[resource.Resource+":"+action.Action] = true
			}
		}
	}
	for _, permission := range authz.AllPermissions() {
		assert.True(t, scopes[service.AccessTokenScopeOf(permission)], "root catalog lacks %s", service.AccessTokenScopeOf(permission))
	}
	for resource, count := range resources {
		assert.Equal(t, 1, count, "resource %s appears in more than one catalog entry", resource)
	}
}

func TestAccessTokenVerificationScopesAreAllListed(t *testing.T) {
	files, err := filepath.Glob("../service/*.go")
	require.NoError(t, err)
	fileSet := token.NewFileSet()
	var scopes []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, file, nil, 0)
		require.NoError(t, err)
		for _, declaration := range parsed.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if !strings.HasPrefix(name.Name, "VerificationScope") || i >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					require.True(t, ok, name.Name)
					scope, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					scopes = append(scopes, scope)
				}
			}
		}
	}
	require.NotEmpty(t, scopes)
	for _, scope := range scopes {
		_, listed := service.AccessTokenVerificationScope(scope)
		assert.True(t, listed, "verification scope %s has no access token rule", scope)
	}
}
