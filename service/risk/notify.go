package risk

import (
	"context"
	"fmt"
	"html"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
)

// Notification claims are durable. Email delivery is deliberately outside the
// enforcement transaction: a mail outage must not undo a confirmed violation.
var reviewNotifications = struct {
	start sync.Once
	stop  sync.Once
	done  chan struct{}
	quit  chan struct{}
}{done: make(chan struct{}), quit: make(chan struct{})}

func StartReviewNotifications() {
	reviewNotifications.start.Do(func() {
		go func() {
			defer close(reviewNotifications.done)
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-reviewNotifications.quit:
					return
				case <-ticker.C:
					// Bound each batch so shutdown is checked between deliveries.
					for range 20 {
						select {
						case <-reviewNotifications.quit:
							return
						default:
						}
						sent, err := dispatchReviewNotification(common.SendEmail, common.GetTimestamp())
						if err != nil {
							// Do not log SMTP responses, addresses, or request contents.
							common.SysError(common.LogText("risk review email notification processing failed"))
							break
						}
						if !sent {
							break
						}
					}
				}
			}
		}()
	})
}

func StopReviewNotifications(ctx context.Context) error {
	reviewNotifications.stop.Do(func() { close(reviewNotifications.quit) })
	select {
	case <-reviewNotifications.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dispatchReviewNotification processes at most one confirmed incident. Tests
// inject only the external email transport; claims and state use the real DB.
func dispatchReviewNotification(send func(string, string, string) error, now int64) (bool, error) {
	row, err := model.ClaimRiskReviewNotification(now)
	if err != nil || row == nil {
		return false, err
	}
	email, err := model.GetUserEmail(row.UserId)
	if err != nil {
		return true, model.FinishRiskReviewNotification(row.Id, "failed", "recipient_lookup_failed", now)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return true, model.FinishRiskReviewNotification(row.Id, "skipped", "no_bound_email", now)
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return true, model.FinishRiskReviewNotification(row.Id, "skipped", "invalid_bound_email", now)
	}
	current, err := model.GetRiskReview(row.Id)
	if err != nil {
		return true, model.FinishRiskReviewNotification(row.Id, "failed", "review_lookup_failed", now)
	}
	if current.Status != model.RiskReviewConfirmed || current.Mode != ModeEnforce {
		return true, model.FinishRiskReviewNotification(row.Id, "skipped", "review_no_longer_confirmed", now)
	}
	block, err := model.GetActiveRiskTokenBlock(row.UserId, row.TokenId, now)
	if err != nil {
		return true, model.FinishRiskReviewNotification(row.Id, "failed", "restriction_lookup_failed", now)
	}
	lang := model.GetUserLanguage(row.UserId)
	args := map[string]any{
		"SystemName": html.EscapeString(common.SystemName), "RequestID": html.EscapeString(row.RequestId),
		"Category": html.EscapeString(row.Category), "TokenID": row.TokenId, "ReviewID": row.Id,
	}
	stateKey := i18n.MsgRiskEmailNoRestriction
	if block != nil {
		stateKey = i18n.MsgRiskEmailTemporaryRestriction
		args["ExpiresAt"] = time.Unix(block.ExpiresAt, 0).UTC().Format(time.RFC3339)
		if block.ExpiresAt == 0 {
			stateKey = i18n.MsgRiskEmailPermanentRestriction
		}
	}
	subject := i18n.Translate(lang, i18n.MsgRiskEmailSubject, map[string]any{"SystemName": common.SystemName})
	body := fmt.Sprintf("<p>%s</p><p>%s</p><p>%s</p>",
		i18n.Translate(lang, i18n.MsgRiskEmailViolation, args),
		i18n.Translate(lang, stateKey, args),
		i18n.Translate(lang, i18n.MsgRiskEmailAppeal, args))
	if err := send(subject, email, body); err != nil {
		return true, model.FinishRiskReviewNotification(row.Id, "failed", "email_delivery_failed", now)
	}
	return true, model.FinishRiskReviewNotification(row.Id, "sent", "", now)
}
