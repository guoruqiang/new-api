package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	OAuthClientStatusEnabled  = 1
	OAuthClientStatusDisabled = 2
)

type OAuthClient struct {
	Id            int            `json:"id"`
	Name          string         `json:"name" gorm:"type:varchar(128);not null;default:''"`
	ClientID      string         `json:"client_id" gorm:"column:client_id;type:varchar(128);uniqueIndex;not null"`
	ClientSecret  *string        `json:"client_secret,omitempty" gorm:"column:client_secret;type:varchar(255)"`
	RedirectURIs  string         `json:"redirect_uris" gorm:"column:redirect_uris;type:text;not null"`
	AllowedScopes string         `json:"allowed_scopes" gorm:"column:allowed_scopes;type:text;not null"`
	IsPublic      bool           `json:"is_public" gorm:"column:is_public;default:true"`
	RequirePKCE   bool           `json:"require_pkce" gorm:"column:require_pkce;default:true"`
	Status        int            `json:"status" gorm:"column:status;default:1"`
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

type OAuthAuthorizationCode struct {
	Id                  int            `json:"id"`
	CodeHash            string         `json:"code_hash" gorm:"column:code_hash;type:char(64);uniqueIndex;not null"`
	UserId              int            `json:"user_id" gorm:"column:user_id;index;not null"`
	ClientID            string         `json:"client_id" gorm:"column:client_id;type:varchar(128);index;not null"`
	RedirectURI         string         `json:"redirect_uri" gorm:"column:redirect_uri;type:text;not null"`
	Scopes              string         `json:"scopes" gorm:"column:scopes;type:text;not null"`
	CodeChallenge       string         `json:"code_challenge" gorm:"column:code_challenge;type:varchar(255);not null"`
	CodeChallengeMethod string         `json:"code_challenge_method" gorm:"column:code_challenge_method;type:varchar(16);not null"`
	ExpiresAt           int64          `json:"expires_at" gorm:"column:expires_at;index;not null"`
	Used                bool           `json:"used" gorm:"column:used;default:false"`
	UsedAt              int64          `json:"used_at" gorm:"column:used_at;default:0"`
	DeletedAt           gorm.DeletedAt `gorm:"index"`
}

type OAuthAccessToken struct {
	Id             int            `json:"id"`
	TokenHash      string         `json:"token_hash" gorm:"column:token_hash;type:char(64);uniqueIndex;not null"`
	UserId         int            `json:"user_id" gorm:"column:user_id;index;not null"`
	ClientID       string         `json:"client_id" gorm:"column:client_id;type:varchar(128);index;not null"`
	Scopes         string         `json:"scopes" gorm:"column:scopes;type:text;not null"`
	ExpiresAt      int64          `json:"expires_at" gorm:"column:expires_at;index;not null"`
	RefreshTokenId *int           `json:"refresh_token_id,omitempty" gorm:"column:refresh_token_id;index"`
	Revoked        bool           `json:"revoked" gorm:"column:revoked;default:false"`
	RevokedAt      int64          `json:"revoked_at" gorm:"column:revoked_at;default:0"`
	LastUsedAt     int64          `json:"last_used_at" gorm:"column:last_used_at;default:0"`
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

type OAuthRefreshToken struct {
	Id        int            `json:"id"`
	TokenHash string         `json:"token_hash" gorm:"column:token_hash;type:char(64);uniqueIndex;not null"`
	UserId    int            `json:"user_id" gorm:"column:user_id;index;not null"`
	ClientID  string         `json:"client_id" gorm:"column:client_id;type:varchar(128);index;not null"`
	Scopes    string         `json:"scopes" gorm:"column:scopes;type:text;not null"`
	ExpiresAt int64          `json:"expires_at" gorm:"column:expires_at;index;not null"`
	Revoked   bool           `json:"revoked" gorm:"column:revoked;default:false"`
	RevokedAt int64          `json:"revoked_at" gorm:"column:revoked_at;default:0"`
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func hashOAuthSecret(raw string) string {
	return common.GenerateHMAC(raw)
}

func GetOAuthClientByClientID(clientID string) (*OAuthClient, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, errors.New("client_id 不能为空")
	}
	client := &OAuthClient{}
	err := DB.Where("client_id = ?", clientID).First(client).Error
	if err != nil {
		return nil, err
	}
	return client, nil
}

func EnsureOAuthClient(client *OAuthClient) error {
	if client == nil {
		return errors.New("oauth client 不能为空")
	}
	existing := &OAuthClient{}
	err := DB.Where("client_id = ?", client.ClientID).First(existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DB.Create(client).Error
	}
	if err != nil {
		return err
	}
	return nil
}

func CreateOAuthAuthorizationCode(code *OAuthAuthorizationCode) error {
	if code == nil {
		return errors.New("oauth authorization code 不能为空")
	}
	return DB.Create(code).Error
}

func GetOAuthAuthorizationCodeByCode(rawCode string) (*OAuthAuthorizationCode, error) {
	if strings.TrimSpace(rawCode) == "" {
		return nil, errors.New("authorization code 不能为空")
	}
	code := &OAuthAuthorizationCode{}
	err := DB.Where("code_hash = ?", hashOAuthSecret(rawCode)).First(code).Error
	if err != nil {
		return nil, err
	}
	return code, nil
}

func MarkOAuthAuthorizationCodeUsed(tx *gorm.DB, id int, usedAt int64) error {
	if tx == nil {
		tx = DB
	}
	result := tx.Model(&OAuthAuthorizationCode{}).
		Where("id = ? AND used = ?", id, false).
		Updates(map[string]any{
			"used":    true,
			"used_at": usedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("authorization code 已被使用")
	}
	return nil
}

func CreateOAuthAccessToken(tx *gorm.DB, token *OAuthAccessToken) error {
	if token == nil {
		return errors.New("oauth access token 不能为空")
	}
	if tx == nil {
		tx = DB
	}
	return tx.Create(token).Error
}

func CreateOAuthRefreshToken(tx *gorm.DB, token *OAuthRefreshToken) error {
	if token == nil {
		return errors.New("oauth refresh token 不能为空")
	}
	if tx == nil {
		tx = DB
	}
	return tx.Create(token).Error
}

func UpdateOAuthAccessTokenRefreshID(tx *gorm.DB, accessTokenID int, refreshTokenID int) error {
	if tx == nil {
		tx = DB
	}
	return tx.Model(&OAuthAccessToken{}).
		Where("id = ?", accessTokenID).
		Update("refresh_token_id", refreshTokenID).Error
}

func GetOAuthAccessTokenByRawToken(rawToken string) (*OAuthAccessToken, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, errors.New("access token 不能为空")
	}
	token := &OAuthAccessToken{}
	err := DB.Where("token_hash = ?", hashOAuthSecret(rawToken)).First(token).Error
	if err != nil {
		return nil, err
	}
	return token, nil
}

func GetOAuthRefreshTokenByRawToken(rawToken string) (*OAuthRefreshToken, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, errors.New("refresh token 不能为空")
	}
	token := &OAuthRefreshToken{}
	err := DB.Where("token_hash = ?", hashOAuthSecret(rawToken)).First(token).Error
	if err != nil {
		return nil, err
	}
	return token, nil
}

func TouchOAuthAccessTokenLastUsed(id int, ts int64) error {
	return DB.Model(&OAuthAccessToken{}).
		Where("id = ?", id).
		Update("last_used_at", ts).Error
}

func RevokeOAuthAccessToken(tx *gorm.DB, id int, revokedAt int64) error {
	if tx == nil {
		tx = DB
	}
	return tx.Model(&OAuthAccessToken{}).
		Where("id = ? AND revoked = ?", id, false).
		Updates(map[string]any{
			"revoked":    true,
			"revoked_at": revokedAt,
		}).Error
}

func RevokeOAuthRefreshToken(tx *gorm.DB, id int, revokedAt int64) error {
	if tx == nil {
		tx = DB
	}
	return tx.Model(&OAuthRefreshToken{}).
		Where("id = ? AND revoked = ?", id, false).
		Updates(map[string]any{
			"revoked":    true,
			"revoked_at": revokedAt,
		}).Error
}

func GetActiveUserTokensForOAuth(userId int, limit int) ([]*Token, error) {
	if limit <= 0 {
		limit = 20
	}
	now := common.GetTimestamp()
	tokens := make([]*Token, 0, limit)
	err := DB.Where("user_id = ? AND status = ? AND (expired_time = ? OR expired_time > ?)", userId, common.TokenStatusEnabled, -1, now).
		Where(DB.Where("unlimited_quota = ?", true).Or("remain_quota > ?", 0)).
		Order("id desc").
		Limit(limit).
		Find(&tokens).Error
	return tokens, err
}
