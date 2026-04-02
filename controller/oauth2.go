package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func oauthRequestScheme(c *gin.Context) string {
	if proto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); proto != "" {
		return proto
	}
	if c.Request.TLS != nil {
		return "https"
	}
	return "http"
}

func oauthIssuer(c *gin.Context) string {
	return service.ResolveOAuthIssuer(oauthRequestScheme(c), c.Request.Host)
}

func oauthErrorResponse(c *gin.Context, err error) {
	var oauthErr *service.OAuthError
	if errors.As(err, &oauthErr) {
		status := oauthErr.StatusCode
		if status == 0 {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{
			"error":             oauthErr.Code,
			"error_description": oauthErr.Description,
		})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{
		"error":             "server_error",
		"error_description": err.Error(),
	})
}

func redirectOAuthError(c *gin.Context, redirectURI string, errorCode string, description string, state string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             errorCode,
			"error_description": description,
		})
		return
	}
	q := u.Query()
	q.Set("error", errorCode)
	if description != "" {
		q.Set("error_description", description)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, u.String())
}

func buildLoginRedirectTarget(c *gin.Context) string {
	loginURL := &url.URL{
		Path: "/login",
	}
	query := loginURL.Query()
	query.Set("redirect", c.Request.URL.RequestURI())
	loginURL.RawQuery = query.Encode()
	return loginURL.String()
}

func OAuthAuthorize(c *gin.Context) {
	client, err := service.GetOAuthClient(c.Query("client_id"))
	if err != nil {
		oauthErrorResponse(c, err)
		return
	}
	redirectURI := c.Query("redirect_uri")
	state := c.Query("state")
	_, normalizedScopes, err := service.ValidateAuthorizationRequest(
		client,
		redirectURI,
		c.Query("response_type"),
		c.Query("scope"),
		c.Query("code_challenge"),
		c.Query("code_challenge_method"),
	)
	if err != nil {
		var oauthErr *service.OAuthError
		if errors.As(err, &oauthErr) && service.IsRedirectURIAllowed(client, redirectURI) {
			redirectOAuthError(c, redirectURI, oauthErr.Code, oauthErr.Description, state)
			return
		}
		oauthErrorResponse(c, err)
		return
	}

	session := sessions.Default(c)
	userIDRaw := session.Get("id")
	if userIDRaw == nil {
		c.Redirect(http.StatusFound, buildLoginRedirectTarget(c))
		return
	}
	userID, ok := userIDRaw.(int)
	if !ok || userID <= 0 {
		c.Redirect(http.StatusFound, buildLoginRedirectTarget(c))
		return
	}
	user, err := model.GetUserById(userID, false)
	if err != nil {
		redirectOAuthError(c, redirectURI, "access_denied", "user not found", state)
		return
	}
	if user.Status != common.UserStatusEnabled {
		redirectOAuthError(c, redirectURI, "access_denied", "user is disabled", state)
		return
	}

	code, err := service.CreateOAuthAuthorizationCode(
		user.Id,
		client,
		redirectURI,
		normalizedScopes,
		c.Query("code_challenge"),
		c.Query("code_challenge_method"),
	)
	if err != nil {
		redirectOAuthError(c, redirectURI, "server_error", err.Error(), state)
		return
	}
	u, err := url.Parse(redirectURI)
	if err != nil {
		oauthErrorResponse(c, err)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	c.Redirect(http.StatusFound, u.String())
}

func oauthClientCredentialsFromRequest(c *gin.Context) (string, *string) {
	clientID := strings.TrimSpace(c.PostForm("client_id"))
	clientSecretRaw := strings.TrimSpace(c.PostForm("client_secret"))
	if clientID == "" {
		if basicUser, basicPass, ok := c.Request.BasicAuth(); ok {
			clientID = strings.TrimSpace(basicUser)
			if clientSecretRaw == "" {
				clientSecretRaw = strings.TrimSpace(basicPass)
			}
		}
	}
	if clientSecretRaw == "" {
		return clientID, nil
	}
	return clientID, common.GetPointer(clientSecretRaw)
}

func OAuthToken(c *gin.Context) {
	clientID, clientSecret := oauthClientCredentialsFromRequest(c)
	if clientID == "" {
		oauthErrorResponse(c, &service.OAuthError{
			Code:        "invalid_client",
			Description: "client_id is required",
			StatusCode:  http.StatusUnauthorized,
		})
		return
	}
	client, err := service.AuthenticateOAuthClient(clientID, clientSecret)
	if err != nil {
		oauthErrorResponse(c, err)
		return
	}

	var response *service.OAuthTokenResponse
	switch strings.TrimSpace(c.PostForm("grant_type")) {
	case service.OAuthGrantTypeAuthorization:
		response, err = service.ExchangeAuthorizationCode(
			client,
			c.PostForm("code"),
			c.PostForm("redirect_uri"),
			c.PostForm("code_verifier"),
			oauthIssuer(c),
		)
	case service.OAuthGrantTypeRefreshToken:
		response, err = service.RefreshOAuthToken(
			client,
			c.PostForm("refresh_token"),
			oauthIssuer(c),
		)
	default:
		err = &service.OAuthError{
			Code:        "unsupported_grant_type",
			Description: "unsupported grant_type",
			StatusCode:  http.StatusBadRequest,
		}
	}
	if err != nil {
		oauthErrorResponse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func OAuthRevoke(c *gin.Context) {
	clientID, _ := oauthClientCredentialsFromRequest(c)
	_ = service.RevokeOAuthToken(clientID, c.PostForm("token"))
	c.Status(http.StatusOK)
}

func OAuthUserInfo(c *gin.Context) {
	authInfoRaw, exists := c.Get("oauth_access_token")
	if !exists {
		oauthErrorResponse(c, &service.OAuthError{
			Code:        "invalid_token",
			Description: "missing oauth access token context",
			StatusCode:  http.StatusUnauthorized,
		})
		return
	}
	authInfo := authInfoRaw.(*service.ValidatedOAuthAccessToken)
	c.JSON(http.StatusOK, service.BuildOAuthUserInfo(authInfo.User, authInfo.Scopes))
}

func OAuthDiscovery(c *gin.Context) {
	c.JSON(http.StatusOK, service.BuildOAuthDiscoveryDocument(oauthIssuer(c)))
}

func OAuthJWKS(c *gin.Context) {
	jwks, err := service.GetOAuthJWKS()
	if err != nil {
		oauthErrorResponse(c, err)
		return
	}
	c.JSON(http.StatusOK, jwks)
}

func OAuthListUserTokens(c *gin.Context) {
	userID := c.GetInt("id")
	tokens, err := model.GetActiveUserTokensForOAuth(userID, 20)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]gin.H, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, gin.H{
			"token": "sk-" + token.GetFullKey(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"data": items,
	})
}

func OAuthBalance(c *gin.Context) {
	userID := c.GetInt("id")
	quota, err := model.GetUserQuota(userID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	usedQuota, err := model.GetUserUsedQuota(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"quota":      quota,
			"used_quota": usedQuota,
		},
	})
}

func OAuthModelsAlias(c *gin.Context) {
	ListModels(c, constant.ChannelTypeOpenAI)
}
