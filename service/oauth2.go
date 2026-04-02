package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const (
	DefaultOAuthClientID          = "2a348c87-bae1-4756-a62f-b2e97200fd6d"
	DefaultOAuthClientName        = "Cherry Studio Public Client"
	DefaultOAuthRedirectURI       = "cherrystudio://oauth/callback"
	OAuthAuthorizationCodeTTL     = int64(300)
	OAuthAccessTokenTTL           = int64(3600)
	OAuthRefreshTokenTTL          = int64(30 * 24 * 3600)
	OAuthCodeChallengeMethodS256  = "S256"
	OAuthGrantTypeAuthorization   = "authorization_code"
	OAuthGrantTypeRefreshToken    = "refresh_token"
	OAuthTokenTypeBearer          = "Bearer"
	OAuthSigningAlgorithm         = "RS256"
	OAuthAuthorizationEndpoint    = "/oauth2/auth"
	OAuthTokenEndpoint            = "/oauth2/token"
	OAuthRevocationEndpoint       = "/oauth2/revoke"
	OAuthUserinfoEndpoint         = "/oauth2/userinfo"
	OAuthJWKSURI                  = "/oauth2/jwks"
)

var supportedOAuthScopes = []string{
	"openid",
	"profile",
	"email",
	"offline_access",
	"balance:read",
	"usage:read",
	"tokens:read",
	"tokens:write",
}

type OAuthError struct {
	Code        string
	Description string
	StatusCode  int
}

func (e *OAuthError) Error() string {
	if e == nil {
		return ""
	}
	return e.Description
}

type OAuthTokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken *string `json:"refresh_token,omitempty"`
	TokenType    string  `json:"token_type"`
	ExpiresIn    int64   `json:"expires_in"`
	IDToken      *string `json:"id_token,omitempty"`
	Scope        string  `json:"scope,omitempty"`
}

type ValidatedOAuthAccessToken struct {
	User   *model.User
	Token  *model.OAuthAccessToken
	Scopes []string
}

var (
	oauthSigningKeyOnce sync.Once
	oauthSigningKey     *rsa.PrivateKey
	oauthSigningKeyKID  string
	oauthSigningKeyErr  error
)

func oauthError(code string, status int, description string) *OAuthError {
	return &OAuthError{
		Code:        code,
		Description: description,
		StatusCode:  status,
	}
}

func normalizeScopeList(raw string) ([]string, string, error) {
	allowedSet := make(map[string]struct{}, len(supportedOAuthScopes))
	for _, scope := range supportedOAuthScopes {
		allowedSet[scope] = struct{}{}
	}
	seen := make(map[string]struct{}, len(supportedOAuthScopes))
	requested := make([]string, 0, len(supportedOAuthScopes))
	for _, part := range strings.Fields(strings.TrimSpace(raw)) {
		if _, ok := allowedSet[part]; !ok {
			return nil, "", oauthError("invalid_scope", 400, fmt.Sprintf("unsupported scope: %s", part))
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		requested = append(requested, part)
	}
	if len(requested) == 0 {
		requested = []string{"openid", "profile", "email"}
	}
	ordered := make([]string, 0, len(requested))
	for _, scope := range supportedOAuthScopes {
		if _, ok := seen[scope]; ok {
			ordered = append(ordered, scope)
			continue
		}
		if len(raw) == 0 && (scope == "openid" || scope == "profile" || scope == "email") {
			ordered = append(ordered, scope)
		}
	}
	return ordered, strings.Join(ordered, " "), nil
}

func scopeSet(scopes []string) map[string]struct{} {
	set := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		set[scope] = struct{}{}
	}
	return set
}

func hasScope(scopes []string, target string) bool {
	for _, scope := range scopes {
		if scope == target {
			return true
		}
	}
	return false
}

func ensureDefaultOAuthClient() error {
	return model.EnsureOAuthClient(&model.OAuthClient{
		Name:          DefaultOAuthClientName,
		ClientID:      DefaultOAuthClientID,
		RedirectURIs:  DefaultOAuthRedirectURI,
		AllowedScopes: strings.Join(supportedOAuthScopes, " "),
		IsPublic:      true,
		RequirePKCE:   true,
		Status:        model.OAuthClientStatusEnabled,
	})
}

func GetOAuthClient(clientID string) (*model.OAuthClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, oauthError("invalid_client", 401, "client_id is required")
	}
	if err := ensureDefaultOAuthClient(); err != nil {
		return nil, err
	}
	client, err := model.GetOAuthClientByClientID(clientID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, oauthError("invalid_client", 401, "unknown client_id")
		}
		return nil, err
	}
	if client.Status != model.OAuthClientStatusEnabled {
		return nil, oauthError("invalid_client", 401, "client is disabled")
	}
	return client, nil
}

func AuthenticateOAuthClient(clientID string, clientSecret *string) (*model.OAuthClient, error) {
	client, err := GetOAuthClient(clientID)
	if err != nil {
		return nil, err
	}
	if client.IsPublic {
		return client, nil
	}
	if client.ClientSecret == nil || clientSecret == nil || subtle.ConstantTimeCompare([]byte(*client.ClientSecret), []byte(*clientSecret)) != 1 {
		return nil, oauthError("invalid_client", 401, "client authentication failed")
	}
	return client, nil
}

func clientRedirectURIs(client *model.OAuthClient) []string {
	normalized := strings.ReplaceAll(client.RedirectURIs, ",", "\n")
	parts := strings.Split(normalized, "\n")
	uris := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			uris = append(uris, part)
		}
	}
	return uris
}

func IsRedirectURIAllowed(client *model.OAuthClient, redirectURI string) bool {
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" || client == nil {
		return false
	}
	for _, allowed := range clientRedirectURIs(client) {
		if redirectURI == allowed {
			return true
		}
	}
	return false
}

func ValidateAuthorizationRequest(client *model.OAuthClient, redirectURI string, responseType string, scopeRaw string, codeChallenge string, codeChallengeMethod string) ([]string, string, error) {
	if client == nil {
		return nil, "", oauthError("invalid_client", 401, "client not found")
	}
	if !IsRedirectURIAllowed(client, redirectURI) {
		return nil, "", oauthError("invalid_request", 400, "redirect_uri mismatch")
	}
	if strings.TrimSpace(responseType) != "code" {
		return nil, "", oauthError("unsupported_response_type", 400, "only response_type=code is supported")
	}
	scopes, normalized, err := normalizeScopeList(scopeRaw)
	if err != nil {
		return nil, "", err
	}
	allowedScopes, _, err := normalizeScopeList(client.AllowedScopes)
	if err != nil {
		return nil, "", err
	}
	allowedSet := scopeSet(allowedScopes)
	for _, scope := range scopes {
		if _, ok := allowedSet[scope]; !ok {
			return nil, "", oauthError("invalid_scope", 400, fmt.Sprintf("scope not allowed: %s", scope))
		}
	}
	if client.RequirePKCE || client.IsPublic {
		if strings.TrimSpace(codeChallenge) == "" {
			return nil, "", oauthError("invalid_request", 400, "code_challenge is required")
		}
		if strings.TrimSpace(codeChallengeMethod) != OAuthCodeChallengeMethodS256 {
			return nil, "", oauthError("invalid_request", 400, "only S256 code_challenge_method is supported")
		}
	}
	return scopes, normalized, nil
}

func CreateOAuthAuthorizationCode(userID int, client *model.OAuthClient, redirectURI string, normalizedScopes string, codeChallenge string, codeChallengeMethod string) (string, error) {
	rawCode, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		return "", err
	}
	record := &model.OAuthAuthorizationCode{
		CodeHash:            common.GenerateHMAC(rawCode),
		UserId:              userID,
		ClientID:            client.ClientID,
		RedirectURI:         redirectURI,
		Scopes:              normalizedScopes,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:           common.GetTimestamp() + OAuthAuthorizationCodeTTL,
	}
	if err := model.CreateOAuthAuthorizationCode(record); err != nil {
		return "", err
	}
	return rawCode, nil
}

func verifyCodeChallenge(codeVerifier string, storedChallenge string, method string) error {
	if strings.TrimSpace(codeVerifier) == "" {
		return oauthError("invalid_request", 400, "code_verifier is required")
	}
	if method != OAuthCodeChallengeMethodS256 {
		return oauthError("invalid_grant", 400, "unsupported code_challenge_method")
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	calculated := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(calculated), []byte(storedChallenge)) != 1 {
		return oauthError("invalid_grant", 400, "PKCE verification failed")
	}
	return nil
}

func issueOAuthTokensTx(tx *gorm.DB, user *model.User, client *model.OAuthClient, normalizedScopes string, issuer string) (*OAuthTokenResponse, error) {
	accessTokenRaw, err := common.GenerateRandomCharsKey(64)
	if err != nil {
		return nil, err
	}
	accessTokenRecord := &model.OAuthAccessToken{
		TokenHash: common.GenerateHMAC(accessTokenRaw),
		UserId:    user.Id,
		ClientID:  client.ClientID,
		Scopes:    normalizedScopes,
		ExpiresAt: common.GetTimestamp() + OAuthAccessTokenTTL,
	}
	if err := model.CreateOAuthAccessToken(tx, accessTokenRecord); err != nil {
		return nil, err
	}

	response := &OAuthTokenResponse{
		AccessToken: accessTokenRaw,
		TokenType:   OAuthTokenTypeBearer,
		ExpiresIn:   OAuthAccessTokenTTL,
		Scope:       normalizedScopes,
	}

	scopes, _, err := normalizeScopeList(normalizedScopes)
	if err != nil {
		return nil, err
	}
	if hasScope(scopes, "offline_access") {
		refreshTokenRaw, err := common.GenerateRandomCharsKey(64)
		if err != nil {
			return nil, err
		}
		refreshTokenRecord := &model.OAuthRefreshToken{
			TokenHash: common.GenerateHMAC(refreshTokenRaw),
			UserId:    user.Id,
			ClientID:  client.ClientID,
			Scopes:    normalizedScopes,
			ExpiresAt: common.GetTimestamp() + OAuthRefreshTokenTTL,
		}
		if err := model.CreateOAuthRefreshToken(tx, refreshTokenRecord); err != nil {
			return nil, err
		}
		if err := model.UpdateOAuthAccessTokenRefreshID(tx, accessTokenRecord.Id, refreshTokenRecord.Id); err != nil {
			return nil, err
		}
		response.RefreshToken = common.GetPointer(refreshTokenRaw)
	}
	if hasScope(scopes, "openid") {
		idToken, err := SignOAuthIDToken(user, client.ClientID, scopes, issuer)
		if err != nil {
			return nil, err
		}
		response.IDToken = common.GetPointer(idToken)
	}
	return response, nil
}

func ExchangeAuthorizationCode(client *model.OAuthClient, rawCode string, redirectURI string, codeVerifier string, issuer string) (*OAuthTokenResponse, error) {
	if strings.TrimSpace(rawCode) == "" {
		return nil, oauthError("invalid_grant", 400, "authorization code is required")
	}
	record, err := model.GetOAuthAuthorizationCodeByCode(rawCode)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, oauthError("invalid_grant", 400, "authorization code is invalid")
		}
		return nil, err
	}
	if record.ClientID != client.ClientID {
		return nil, oauthError("invalid_grant", 400, "authorization code does not belong to client")
	}
	if redirectURI != record.RedirectURI {
		return nil, oauthError("invalid_grant", 400, "redirect_uri mismatch")
	}
	if record.Used {
		return nil, oauthError("invalid_grant", 400, "authorization code already used")
	}
	if record.ExpiresAt < common.GetTimestamp() {
		return nil, oauthError("invalid_grant", 400, "authorization code expired")
	}
	if err := verifyCodeChallenge(codeVerifier, record.CodeChallenge, record.CodeChallengeMethod); err != nil {
		return nil, err
	}
	user, err := model.GetUserById(record.UserId, false)
	if err != nil {
		return nil, oauthError("invalid_grant", 400, "user not found")
	}
	if user.Status != common.UserStatusEnabled {
		return nil, oauthError("invalid_grant", 400, "user is disabled")
	}

	var response *OAuthTokenResponse
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		if err := model.MarkOAuthAuthorizationCodeUsed(tx, record.Id, now); err != nil {
			return oauthError("invalid_grant", 400, "authorization code already used")
		}
		var txErr error
		response, txErr = issueOAuthTokensTx(tx, user, client, record.Scopes, issuer)
		return txErr
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func RefreshOAuthToken(client *model.OAuthClient, rawRefreshToken string, issuer string) (*OAuthTokenResponse, error) {
	if strings.TrimSpace(rawRefreshToken) == "" {
		return nil, oauthError("invalid_grant", 400, "refresh token is required")
	}
	refreshToken, err := model.GetOAuthRefreshTokenByRawToken(rawRefreshToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, oauthError("invalid_grant", 400, "refresh token is invalid")
		}
		return nil, err
	}
	if refreshToken.ClientID != client.ClientID {
		return nil, oauthError("invalid_grant", 400, "refresh token does not belong to client")
	}
	if refreshToken.Revoked {
		return nil, oauthError("invalid_grant", 400, "refresh token revoked")
	}
	if refreshToken.ExpiresAt < common.GetTimestamp() {
		return nil, oauthError("invalid_grant", 400, "refresh token expired")
	}
	user, err := model.GetUserById(refreshToken.UserId, false)
	if err != nil {
		return nil, oauthError("invalid_grant", 400, "user not found")
	}
	if user.Status != common.UserStatusEnabled {
		return nil, oauthError("invalid_grant", 400, "user is disabled")
	}

	var response *OAuthTokenResponse
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		if err := model.RevokeOAuthRefreshToken(tx, refreshToken.Id, now); err != nil {
			return err
		}
		if err := tx.Model(&model.OAuthAccessToken{}).
			Where("refresh_token_id = ? AND revoked = ?", refreshToken.Id, false).
			Updates(map[string]any{
				"revoked":    true,
				"revoked_at": now,
			}).Error; err != nil {
			return err
		}
		var txErr error
		response, txErr = issueOAuthTokensTx(tx, user, client, refreshToken.Scopes, issuer)
		return txErr
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func ValidateOAuthAccessToken(rawToken string, requiredScopes []string) (*ValidatedOAuthAccessToken, error) {
	record, err := model.GetOAuthAccessTokenByRawToken(rawToken)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, oauthError("invalid_token", 401, "access token is invalid")
		}
		return nil, err
	}
	if record.Revoked {
		return nil, oauthError("invalid_token", 401, "access token revoked")
	}
	if record.ExpiresAt < common.GetTimestamp() {
		return nil, oauthError("invalid_token", 401, "access token expired")
	}
	user, err := model.GetUserById(record.UserId, false)
	if err != nil {
		return nil, oauthError("invalid_token", 401, "user not found")
	}
	if user.Status != common.UserStatusEnabled {
		return nil, oauthError("invalid_token", 401, "user is disabled")
	}
	scopes, _, err := normalizeScopeList(record.Scopes)
	if err != nil {
		return nil, err
	}
	scopeLookup := scopeSet(scopes)
	for _, required := range requiredScopes {
		if _, ok := scopeLookup[required]; !ok {
			return nil, oauthError("insufficient_scope", 403, fmt.Sprintf("missing required scope: %s", required))
		}
	}
	_ = model.TouchOAuthAccessTokenLastUsed(record.Id, common.GetTimestamp())
	return &ValidatedOAuthAccessToken{
		User:   user,
		Token:  record,
		Scopes: scopes,
	}, nil
}

func RevokeOAuthToken(clientID string, rawToken string) error {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil
	}
	now := common.GetTimestamp()
	if accessToken, err := model.GetOAuthAccessTokenByRawToken(rawToken); err == nil {
		if clientID == "" || accessToken.ClientID == clientID {
			return model.RevokeOAuthAccessToken(nil, accessToken.Id, now)
		}
		return nil
	}
	if refreshToken, err := model.GetOAuthRefreshTokenByRawToken(rawToken); err == nil {
		if clientID == "" || refreshToken.ClientID == clientID {
			return model.RevokeOAuthRefreshToken(nil, refreshToken.Id, now)
		}
	}
	return nil
}

func ResolveOAuthIssuer(requestScheme string, requestHost string) string {
	serverAddress := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if serverAddress != "" {
		return serverAddress
	}
	requestScheme = strings.TrimSpace(requestScheme)
	if requestScheme == "" {
		requestScheme = "http"
	}
	return fmt.Sprintf("%s://%s", requestScheme, requestHost)
}

func ensureOAuthSigningKey() error {
	oauthSigningKeyOnce.Do(func() {
		oauthSigningKey, oauthSigningKeyErr = rsa.GenerateKey(rand.Reader, 2048)
		if oauthSigningKeyErr != nil {
			return
		}
		sum := sha256.Sum256(oauthSigningKey.PublicKey.N.Bytes())
		oauthSigningKeyKID = base64.RawURLEncoding.EncodeToString(sum[:8])
	})
	return oauthSigningKeyErr
}

func oauthJWKMap() (map[string]any, error) {
	if err := ensureOAuthSigningKey(); err != nil {
		return nil, err
	}
	n := base64.RawURLEncoding.EncodeToString(oauthSigningKey.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(bigEndianBytes(oauthSigningKey.PublicKey.E))
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"kid": oauthSigningKeyKID,
		"alg": OAuthSigningAlgorithm,
		"n":   n,
		"e":   e,
	}, nil
}

func bigEndianBytes(v int) []byte {
	if v == 0 {
		return []byte{0}
	}
	out := make([]byte, 0, 8)
	for v > 0 {
		out = append([]byte{byte(v & 0xff)}, out...)
		v >>= 8
	}
	return out
}

func SignOAuthIDToken(user *model.User, audience string, scopes []string, issuer string) (string, error) {
	if err := ensureOAuthSigningKey(); err != nil {
		return "", err
	}
	now := common.GetTimestamp()
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": strconv.Itoa(user.Id),
		"aud": audience,
		"iat": now,
		"exp": now + OAuthAccessTokenTTL,
	}
	if hasScope(scopes, "profile") {
		name := strings.TrimSpace(user.DisplayName)
		if name == "" {
			name = user.Username
		}
		claims["name"] = name
		claims["preferred_username"] = user.Username
	}
	if hasScope(scopes, "email") && strings.TrimSpace(user.Email) != "" {
		claims["email"] = user.Email
		claims["email_verified"] = true
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = oauthSigningKeyKID
	return token.SignedString(oauthSigningKey)
}

func GetOAuthJWKS() (map[string]any, error) {
	jwk, err := oauthJWKMap()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"keys": []map[string]any{jwk},
	}, nil
}

func BuildOAuthUserInfo(user *model.User, scopes []string) map[string]any {
	result := map[string]any{
		"sub": strconv.Itoa(user.Id),
	}
	if hasScope(scopes, "profile") {
		name := strings.TrimSpace(user.DisplayName)
		if name == "" {
			name = user.Username
		}
		result["name"] = name
		result["preferred_username"] = user.Username
	}
	if hasScope(scopes, "email") && strings.TrimSpace(user.Email) != "" {
		result["email"] = user.Email
		result["email_verified"] = true
	}
	return result
}

func BuildOAuthDiscoveryDocument(issuer string) map[string]any {
	return map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + OAuthAuthorizationEndpoint,
		"token_endpoint":                        issuer + OAuthTokenEndpoint,
		"revocation_endpoint":                   issuer + OAuthRevocationEndpoint,
		"userinfo_endpoint":                     issuer + OAuthUserinfoEndpoint,
		"jwks_uri":                              issuer + OAuthJWKSURI,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{OAuthGrantTypeAuthorization, OAuthGrantTypeRefreshToken},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{OAuthSigningAlgorithm},
		"scopes_supported":                      supportedOAuthScopes,
		"code_challenge_methods_supported":      []string{OAuthCodeChallengeMethodS256},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
	}
}
