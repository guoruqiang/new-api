package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
	require.NoError(t, db.AutoMigrate(&model.OAuthClient{}, &model.OAuthAuthorizationCode{}))
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
