package middleware

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func oauthBearerToken(c *gin.Context) string {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader == "" {
		return ""
	}
	if len(authHeader) >= 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return authHeader
}

func OAuthAccessTokenAuth(requiredScopes ...string) func(c *gin.Context) {
	return func(c *gin.Context) {
		token := oauthBearerToken(c)
		if token == "" {
			c.Header("WWW-Authenticate", `Bearer error="invalid_token", error_description="missing bearer token"`)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":             "invalid_token",
				"error_description": "missing bearer token",
			})
			c.Abort()
			return
		}
		authInfo, err := service.ValidateOAuthAccessToken(token, requiredScopes)
		if err != nil {
			if oauthErr, ok := err.(*service.OAuthError); ok {
				c.Header("WWW-Authenticate", `Bearer error="`+oauthErr.Code+`", error_description="`+oauthErr.Description+`"`)
				c.JSON(oauthErr.StatusCode, gin.H{
					"error":             oauthErr.Code,
					"error_description": oauthErr.Description,
				})
				c.Abort()
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": err.Error(),
			})
			c.Abort()
			return
		}
		c.Set("id", authInfo.User.Id)
		c.Set("oauth_access_token", authInfo)
		c.Set("oauth_scopes", authInfo.Scopes)
		c.Set("oauth_client_id", authInfo.Token.ClientID)
		c.Next()
	}
}
