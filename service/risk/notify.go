package risk

import (
	"context"
	"fmt"
	"html"
	"net/mail"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
)

// Violation emails are sent by one background worker so mail delivery never
// delays a request. Notices still queued at shutdown are not sent.
var violationNotices = struct {
	mu      sync.Mutex
	queue   chan model.RiskViolation
	done    chan struct{}
	stopped bool
}{queue: make(chan model.RiskViolation, 1000), done: make(chan struct{})}

var sendViolationEmail = common.SendEmail

// StartReviewNotifications starts the email worker; main calls it once.
func StartReviewNotifications() {
	go func() {
		defer close(violationNotices.done)
		for v := range violationNotices.queue {
			status := deliverViolationNotice(v)
			if err := model.SetRiskViolationNotification(v.Id, status); err != nil {
				common.SysError(common.LogText("risk violation email notification processing failed"))
			}
		}
	}()
}

func StopReviewNotifications(ctx context.Context) error {
	violationNotices.mu.Lock()
	if !violationNotices.stopped {
		violationNotices.stopped = true
		close(violationNotices.queue)
	}
	violationNotices.mu.Unlock()
	select {
	case <-violationNotices.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func queueViolationNotice(v model.RiskViolation) {
	violationNotices.mu.Lock()
	defer violationNotices.mu.Unlock()
	if !violationNotices.stopped {
		select {
		case violationNotices.queue <- v:
			return
		default:
		}
	}
	if err := model.SetRiskViolationNotification(v.Id, "failed"); err != nil {
		common.SysError(common.LogText("risk violation email notification processing failed"))
	}
}

// deliverViolationNotice emails the user about one violation and returns the
// notification status to store.
func deliverViolationNotice(v model.RiskViolation) string {
	email, err := model.GetUserEmail(v.UserId)
	if err != nil {
		return "failed"
	}
	email = strings.TrimSpace(email)
	if _, err := mail.ParseAddress(email); email == "" || err != nil {
		return "skipped"
	}
	lang := model.GetUserLanguage(v.UserId)
	args := map[string]any{
		"SystemName": html.EscapeString(common.SystemName), "RequestID": html.EscapeString(v.RequestId),
		"Category": html.EscapeString(v.Category), "Deduction": v.Deduction, "Score": v.ScoreAfter, "ViolationID": v.Id,
	}
	lines := []string{i18n.Translate(lang, i18n.MsgRiskEmailViolation, args)}
	switch {
	case v.Action == model.RiskActionAutoBanned:
		lines = append(lines, i18n.Translate(lang, i18n.MsgRiskEmailBanned, args))
	case v.ScoreAfter == 0:
		lines = append(lines, i18n.Translate(lang, i18n.MsgRiskEmailZeroScore, args))
	}
	lines = append(lines, i18n.Translate(lang, i18n.MsgRiskEmailAppeal, args))
	var body strings.Builder
	for _, line := range lines {
		fmt.Fprintf(&body, "<p>%s</p>", line)
	}
	subject := i18n.Translate(lang, i18n.MsgRiskEmailSubject, map[string]any{"SystemName": common.SystemName})
	if err := sendViolationEmail(subject, email, body.String()); err != nil {
		return "failed"
	}
	return "sent"
}
