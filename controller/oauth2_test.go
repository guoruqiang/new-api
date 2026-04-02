package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	oauthmiddleware "github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type oauthTokenEndpointResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

type oauthTokensResourceResponse struct {
	Data []struct {
		Token string `json:"token"`
	} `json:"data"`
}

type oauthBalanceResourceResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Quota     int `json:"quota"`
		UsedQuota int `json:"used_quota"`
	} `json:"data"`
}

func setupOAuthControllerTestServer(t *testing.T) (*httptest.Server, *gorm.DB) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	model.DB = db
	model.LOG_DB = db

	if err := db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.OAuthClient{},
		&model.OAuthAuthorizationCode{},
		&model.OAuthAccessToken{},
		&model.OAuthRefreshToken{},
	); err != nil {
		t.Fatalf("failed to migrate oauth tables: %v", err)
	}

	user := &model.User{
		Id:          1,
		Username:    "oauth-user",
		Password:    "test-password",
		DisplayName: "OAuth User",
		Email:       "oauth@example.com",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		Quota:       500000,
		UsedQuota:   123,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	apiToken := &model.Token{
		UserId:         user.Id,
		Name:           "oauth-resource-token",
		Key:            "resourceToken1234567890",
		Status:         common.TokenStatusEnabled,
		CreatedTime:    1,
		AccessedTime:   1,
		ExpiredTime:    -1,
		RemainQuota:    1000,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := db.Create(apiToken).Error; err != nil {
		t.Fatalf("failed to create api token: %v", err)
	}

	engine := gin.New()
	store := cookie.NewStore([]byte("oauth-test-secret"))
	store.Options(sessions.Options{Path: "/", MaxAge: 3600, HttpOnly: true})
	engine.Use(sessions.Sessions("session", store))
	engine.GET("/test/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set("username", user.Username)
		session.Set("role", user.Role)
		session.Set("status", user.Status)
		session.Set("group", user.Group)
		_ = session.Save()
		c.Status(http.StatusNoContent)
	})
	engine.GET("/oauth2/auth", OAuthAuthorize)
	engine.POST("/oauth2/token", OAuthToken)
	engine.POST("/oauth2/revoke", OAuthRevoke)
	engine.GET("/api/v1/oauth/tokens", oauthmiddleware.OAuthAccessTokenAuth("tokens:read"), OAuthListUserTokens)
	engine.GET("/api/v1/oauth/balance", oauthmiddleware.OAuthAccessTokenAuth("balance:read"), OAuthBalance)

	server := httptest.NewServer(engine)
	t.Cleanup(func() {
		server.Close()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return server, db
}

func oauthPKCEPair(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func oauthHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func oauthLogin(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	resp, err := client.Get(baseURL + "/test/login")
	if err != nil {
		t.Fatalf("failed to login test user: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from test login, got %d", resp.StatusCode)
	}
}

func oauthAuthorize(t *testing.T, client *http.Client, baseURL string, verifier string, state string) string {
	t.Helper()
	authURL := baseURL + "/oauth2/auth?" + url.Values{
		"client_id":             {service.DefaultOAuthClientID},
		"redirect_uri":          {service.DefaultOAuthRedirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid profile email offline_access balance:read usage:read tokens:read tokens:write"},
		"state":                 {state},
		"code_challenge":        {oauthPKCEPair(verifier)},
		"code_challenge_method": {service.OAuthCodeChallengeMethodS256},
	}.Encode()
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatalf("failed to call oauth2/auth: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from oauth2/auth, got %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("failed to parse redirect location: %v", err)
	}
	if gotState := u.Query().Get("state"); gotState != state {
		t.Fatalf("expected state %q, got %q", state, gotState)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("expected authorization code in redirect location: %s", location)
	}
	return code
}

func oauthExchangeCode(t *testing.T, client *http.Client, baseURL string, code string, verifier string) oauthTokenEndpointResponse {
	t.Helper()
	form := url.Values{
		"grant_type":    {service.OAuthGrantTypeAuthorization},
		"client_id":     {service.DefaultOAuthClientID},
		"code":          {code},
		"redirect_uri":  {service.DefaultOAuthRedirectURI},
		"code_verifier": {verifier},
	}
	resp, err := client.PostForm(baseURL+"/oauth2/token", form)
	if err != nil {
		t.Fatalf("failed to exchange authorization code: %v", err)
	}
	defer resp.Body.Close()
	var payload oauthTokenEndpointResponse
	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		t.Fatalf("failed to decode token response: %v", err)
	}
	return payload
}

func oauthRefresh(t *testing.T, client *http.Client, baseURL string, refreshToken string) oauthTokenEndpointResponse {
	t.Helper()
	resp, err := client.PostForm(baseURL+"/oauth2/token", url.Values{
		"grant_type":    {service.OAuthGrantTypeRefreshToken},
		"client_id":     {service.DefaultOAuthClientID},
		"refresh_token": {refreshToken},
	})
	if err != nil {
		t.Fatalf("failed to refresh oauth token: %v", err)
	}
	defer resp.Body.Close()
	var payload oauthTokenEndpointResponse
	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		t.Fatalf("failed to decode refresh response: %v", err)
	}
	return payload
}

func TestOAuthAuthorizationCodeFlowSuccess(t *testing.T) {
	server, _ := setupOAuthControllerTestServer(t)
	client := oauthHTTPClient(t)
	oauthLogin(t, client, server.URL)

	code := oauthAuthorize(t, client, server.URL, "verifier-success", "state-success")
	tokenRes := oauthExchangeCode(t, client, server.URL, code, "verifier-success")
	if tokenRes.AccessToken == "" {
		t.Fatalf("expected access_token in token response")
	}
	if tokenRes.RefreshToken == "" {
		t.Fatalf("expected refresh_token in token response")
	}
	if tokenRes.IDToken == "" {
		t.Fatalf("expected id_token in token response")
	}
	if tokenRes.TokenType != service.OAuthTokenTypeBearer {
		t.Fatalf("expected bearer token type, got %q", tokenRes.TokenType)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/oauth/tokens", nil)
	if err != nil {
		t.Fatalf("failed to create oauth tokens request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to request oauth tokens resource: %v", err)
	}
	defer resp.Body.Close()
	var tokensRes oauthTokensResourceResponse
	if err := common.DecodeJson(resp.Body, &tokensRes); err != nil {
		t.Fatalf("failed to decode oauth tokens resource: %v", err)
	}
	if len(tokensRes.Data) != 1 || tokensRes.Data[0].Token != "sk-resourceToken1234567890" {
		t.Fatalf("unexpected oauth tokens resource payload: %+v", tokensRes.Data)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/v1/oauth/balance", nil)
	if err != nil {
		t.Fatalf("failed to create oauth balance request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenRes.AccessToken)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to request oauth balance resource: %v", err)
	}
	defer resp.Body.Close()
	var balanceRes oauthBalanceResourceResponse
	if err := common.DecodeJson(resp.Body, &balanceRes); err != nil {
		t.Fatalf("failed to decode oauth balance resource: %v", err)
	}
	if !balanceRes.Success || balanceRes.Data.Quota != 500000 || balanceRes.Data.UsedQuota != 123 {
		t.Fatalf("unexpected oauth balance payload: %+v", balanceRes)
	}
}

func TestOAuthAuthorizationCodeRejectsWrongVerifier(t *testing.T) {
	server, _ := setupOAuthControllerTestServer(t)
	client := oauthHTTPClient(t)
	oauthLogin(t, client, server.URL)

	code := oauthAuthorize(t, client, server.URL, "verifier-correct", "state-pkce")
	tokenRes := oauthExchangeCode(t, client, server.URL, code, "verifier-wrong")
	if tokenRes.Error != "invalid_grant" {
		t.Fatalf("expected invalid_grant for wrong verifier, got %+v", tokenRes)
	}
}

func TestOAuthAuthorizationCodeSingleUse(t *testing.T) {
	server, _ := setupOAuthControllerTestServer(t)
	client := oauthHTTPClient(t)
	oauthLogin(t, client, server.URL)

	code := oauthAuthorize(t, client, server.URL, "verifier-once", "state-once")
	firstRes := oauthExchangeCode(t, client, server.URL, code, "verifier-once")
	if firstRes.AccessToken == "" {
		t.Fatalf("expected first token exchange to succeed")
	}
	secondRes := oauthExchangeCode(t, client, server.URL, code, "verifier-once")
	if secondRes.Error != "invalid_grant" {
		t.Fatalf("expected invalid_grant on code reuse, got %+v", secondRes)
	}
}

func TestOAuthRefreshTokenRotation(t *testing.T) {
	server, _ := setupOAuthControllerTestServer(t)
	client := oauthHTTPClient(t)
	oauthLogin(t, client, server.URL)

	code := oauthAuthorize(t, client, server.URL, "verifier-refresh", "state-refresh")
	initial := oauthExchangeCode(t, client, server.URL, code, "verifier-refresh")
	if initial.RefreshToken == "" {
		t.Fatalf("expected initial refresh token")
	}
	refreshed := oauthRefresh(t, client, server.URL, initial.RefreshToken)
	if refreshed.AccessToken == "" || refreshed.RefreshToken == "" {
		t.Fatalf("expected rotated access and refresh tokens, got %+v", refreshed)
	}
	if refreshed.RefreshToken == initial.RefreshToken {
		t.Fatalf("expected refresh token rotation, old=%q new=%q", initial.RefreshToken, refreshed.RefreshToken)
	}
	reused := oauthRefresh(t, client, server.URL, initial.RefreshToken)
	if reused.Error != "invalid_grant" {
		t.Fatalf("expected invalid_grant when reusing rotated refresh token, got %+v", reused)
	}
}
