package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupOAuthAuthorizeControllerTest(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousSettings := *system_setting.GetOAuthServerSettings()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.OAuthClient{}, &model.OAuthAuthorizationCode{}, &model.User{}, &model.UserSession{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)

	settings := system_setting.GetOAuthServerSettings()
	settings.Enabled = true
	settings.ClientName = "OAuth bridge test"
	settings.ClientID = "oauth-bridge-client"
	settings.ClientSecret = ""
	settings.RedirectURIs = "cherrystudio://oauth/callback"
	settings.AllowedScopes = "openid profile"
	settings.IsPublic = true
	settings.RequirePKCE = true

	t.Cleanup(func() {
		*system_setting.GetOAuthServerSettings() = previousSettings
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
	})
}

func TestOAuthAuthorizeReturnsAuthenticatedFrontendRedirect(t *testing.T) {
	setupOAuthAuthorizeControllerTest(t)
	requestURL := "/api/oauth2/auth?client_id=oauth-bridge-client&redirect_uri=cherrystudio%3A%2F%2Foauth%2Fcallback&response_type=code&scope=openid%20profile&code_challenge=test-challenge&code_challenge_method=S256&state=test-state"
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, requestURL, nil)
	context.Set("id", 42)

	OAuthAuthorize(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			RedirectURI string `json:"redirect_uri"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	redirectURI, err := url.Parse(response.Data.RedirectURI)
	require.NoError(t, err)
	assert.Equal(t, "cherrystudio", redirectURI.Scheme)
	assert.Equal(t, "test-state", redirectURI.Query().Get("state"))
	assert.NotEmpty(t, redirectURI.Query().Get("code"))

	var code model.OAuthAuthorizationCode
	require.NoError(t, model.DB.First(&code).Error)
	assert.Equal(t, 42, code.UserId)
	assert.Equal(t, "openid profile", code.Scopes)
}

func TestOAuthAuthorizeRequiresActiveDashboardSession(t *testing.T) {
	for _, state := range []string{"active", "missing", "malformed", "revoked", "expired", "disabled"} {
		t.Run(state, func(t *testing.T) {
			setupOAuthAuthorizeControllerTest(t)
			previousRedis := common.RedisEnabled
			previousSecret := common.SessionSecret
			previousActiveLimit := common.UserSessionActiveLimit
			previousIssuanceLimit := common.UserSessionIssuanceLimit
			previousWindow := common.UserSessionIssuanceWindowSeconds
			common.RedisEnabled = false
			common.SessionSecret = "oauth-authorize-session-test-secret"
			common.UserSessionActiveLimit = common.DefaultUserSessionActiveLimit
			common.UserSessionIssuanceLimit = common.DefaultUserSessionIssuanceLimit
			common.UserSessionIssuanceWindowSeconds = int64(common.DefaultUserSessionIssuanceWindowSeconds)
			t.Cleanup(func() {
				common.RedisEnabled = previousRedis
				common.SessionSecret = previousSecret
				common.UserSessionActiveLimit = previousActiveLimit
				common.UserSessionIssuanceLimit = previousIssuanceLimit
				common.UserSessionIssuanceWindowSeconds = previousWindow
			})
			user := &model.User{Username: "oauth-session-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
			require.NoError(t, model.DB.Create(user).Error)
			session, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "oauth-test")
			require.NoError(t, err)
			token := session.AccessToken
			switch state {
			case "missing":
				token = ""
			case "malformed":
				token = "invalid-token"
			case "revoked":
				revoked, err := model.RevokeUserSession(user.Id, session.Session.SID, "test")
				require.NoError(t, err)
				require.True(t, revoked)
			case "expired":
				require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", session.Session.SID).Update("expires_at", 1).Error)
			case "disabled":
				require.NoError(t, model.DB.Model(user).Update("status", common.UserStatusDisabled).Error)
			}
			router := gin.New()
			router.POST("/api/oauth2/auth", middleware.UserAuth(), OAuthAuthorize)
			request := httptest.NewRequest(http.MethodPost, "/api/oauth2/auth?client_id=oauth-bridge-client&redirect_uri=cherrystudio%3A%2F%2Foauth%2Fcallback&response_type=code&scope=openid%20profile&code_challenge=test-challenge&code_challenge_method=S256&state=test-state&user_id=999", nil)
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			var codes []model.OAuthAuthorizationCode
			require.NoError(t, model.DB.Find(&codes).Error)
			if state != "active" {
				assert.Equal(t, http.StatusUnauthorized, recorder.Code)
				assert.Empty(t, codes)
				return
			}
			assert.Equal(t, http.StatusOK, recorder.Code)
			require.Len(t, codes, 1)
			assert.Equal(t, user.Id, codes[0].UserId)
			assert.Equal(t, "openid profile", codes[0].Scopes)
		})
	}
}
