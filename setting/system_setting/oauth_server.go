package system_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	DefaultOAuthServerClientID    = "2a348c87-bae1-4756-a62f-b2e97200fd6d"
	DefaultOAuthServerClientName  = "Cherry Studio Public Client"
	DefaultOAuthServerRedirectURI = "cherrystudio://oauth/callback"
	DefaultOAuthServerScopes      = "openid profile email offline_access balance:read usage:read tokens:read tokens:write"
)

type OAuthServerSettings struct {
	Enabled       bool   `json:"enabled"`
	ClientName    string `json:"client_name"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	RedirectURIs  string `json:"redirect_uris"`
	AllowedScopes string `json:"allowed_scopes"`
	IsPublic      bool   `json:"is_public"`
	RequirePKCE   bool   `json:"require_pkce"`
}

var defaultOAuthServerSettings = OAuthServerSettings{
	Enabled:       true,
	ClientName:    DefaultOAuthServerClientName,
	ClientID:      DefaultOAuthServerClientID,
	ClientSecret:  "",
	RedirectURIs:  DefaultOAuthServerRedirectURI,
	AllowedScopes: DefaultOAuthServerScopes,
	IsPublic:      true,
	RequirePKCE:   true,
}

func init() {
	config.GlobalConfig.Register("oauth_server", &defaultOAuthServerSettings)
}

func GetOAuthServerSettings() *OAuthServerSettings {
	if strings.TrimSpace(defaultOAuthServerSettings.ClientName) == "" {
		defaultOAuthServerSettings.ClientName = DefaultOAuthServerClientName
	}
	if strings.TrimSpace(defaultOAuthServerSettings.ClientID) == "" {
		defaultOAuthServerSettings.ClientID = DefaultOAuthServerClientID
	}
	if strings.TrimSpace(defaultOAuthServerSettings.RedirectURIs) == "" {
		defaultOAuthServerSettings.RedirectURIs = DefaultOAuthServerRedirectURI
	}
	if strings.TrimSpace(defaultOAuthServerSettings.AllowedScopes) == "" {
		defaultOAuthServerSettings.AllowedScopes = DefaultOAuthServerScopes
	}
	return &defaultOAuthServerSettings
}
