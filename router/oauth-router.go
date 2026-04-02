package router

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func SetOAuthRouter(router *gin.Engine) {
	oauthRouter := router.Group("")
	oauthRouter.Use(middleware.RouteTag("oauth"))
	{
		oauthRouter.GET("/oauth2/auth", controller.OAuthAuthorize)
		oauthRouter.POST("/oauth2/token", controller.OAuthToken)
		oauthRouter.POST("/oauth2/revoke", controller.OAuthRevoke)
		oauthRouter.GET("/oauth2/userinfo", middleware.OAuthAccessTokenAuth("openid"), controller.OAuthUserInfo)
		oauthRouter.GET("/oauth2/jwks", controller.OAuthJWKS)
		oauthRouter.GET("/.well-known/openid-configuration", controller.OAuthDiscovery)
	}

	modelsRouter := router.Group("/models")
	modelsRouter.Use(middleware.RouteTag("relay"))
	modelsRouter.Use(middleware.TokenAuth())
	{
		modelsRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeOpenAI)
		})
	}
}
