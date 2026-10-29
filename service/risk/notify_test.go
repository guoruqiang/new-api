package risk

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRiskViolationEmailDelivery(t *testing.T) {
	require.NoError(t, i18n.Init())
	previousDB, previousType := model.DB, common.MainDatabaseType()
	previousMemory, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.MemoryCacheEnabled, common.RedisEnabled = previousMemory, previousRedis
		require.NoError(t, pool.Close())
	})
	require.NoError(t, db.AutoMigrate(model.RiskModels()...))
	require.NoError(t, db.AutoMigrate(&model.User{}))
	now := int64(1800000000)
	for i, tc := range []struct {
		name, email, mode, status, initial, wantStatus, errorCode string
		deliveryError                                             bool
		wantCalls                                                 int
	}{
		{"confirmed", "member@example.test", ModeEnforce, model.RiskReviewConfirmed, "pending", "sent", "", false, 1},
		{"no email", "", ModeEnforce, model.RiskReviewConfirmed, "pending", "skipped", "no_bound_email", false, 0},
		{"invalid recipient", "one@example.test;two@example.test", ModeEnforce, model.RiskReviewConfirmed, "pending", "skipped", "invalid_bound_email", false, 0},
		{"transport failure", "member@example.test", ModeEnforce, model.RiskReviewConfirmed, "pending", "failed", "email_delivery_failed", true, 1},
		{"pending review", "member@example.test", ModeEnforce, model.RiskReviewPending, "", "", "", false, 0},
		{"observation", "member@example.test", ModeObserve, model.RiskReviewConfirmed, "", "", "", false, 0},
		{"already sent", "member@example.test", ModeEnforce, model.RiskReviewConfirmed, "sent", "sent", "", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &model.User{Username: fmt.Sprintf("mail-%d", i), AffCode: fmt.Sprintf("mail-%d", i), Email: tc.email, Setting: `{"language":"en"}`}
			require.NoError(t, db.Create(user).Error)
			row := &model.RiskReview{
				RequestKey: fmt.Sprintf("mail-request-%d", i), RequestId: fmt.Sprintf("request-%d", i),
				Namespace: "scope", UserId: user.Id, TokenId: 100 + i, Mode: tc.mode, Status: tc.status,
				Category: "nsfw", Revision: 1, NotificationStatus: tc.initial, CreatedAt: now,
				Evidence: "sensitive review text", OriginalRequest: "private original request",
			}
			require.NoError(t, db.Create(row).Error)
			if tc.wantCalls > 0 {
				require.NoError(t, db.Create(&model.RiskSubjectBlock{
					Namespace: "other-scope", UserId: user.Id, SubjectType: "token", SubjectValue: fmt.Sprint(row.TokenId),
					ExpiresAt: now + 1800,
				}).Error)
			}
			calls := 0
			send := func(subject, recipient, body string) error {
				calls++
				assert.Equal(t, tc.email, recipient)
				assert.Contains(t, subject, "Content policy notice")
				assert.Contains(t, body, row.RequestId)
				assert.Contains(t, body, fmt.Sprint(row.TokenId))
				assert.Contains(t, body, "temporarily restricted")
				assert.NotContains(t, body, row.Evidence)
				assert.NotContains(t, body, row.OriginalRequest)
				if tc.deliveryError {
					return errors.New("SMTP response containing private-credential")
				}
				return nil
			}
			_, err := dispatchReviewNotification(send, now)
			require.NoError(t, err)
			_, err = dispatchReviewNotification(send, now+1)
			require.NoError(t, err)
			assert.Equal(t, tc.wantCalls, calls, "repeating the worker must not duplicate email delivery")
			require.NoError(t, db.First(row, row.Id).Error)
			assert.Equal(t, tc.wantStatus, row.NotificationStatus)
			assert.Equal(t, tc.errorCode, row.NotificationError)
			assert.NotContains(t, row.NotificationError, "private-credential")
			assert.Equal(t, tc.status, row.Status, "mail failure cannot change enforcement")
		})
	}
}
