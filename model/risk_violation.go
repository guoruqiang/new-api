package model

import (
	"errors"

	"gorm.io/gorm"
)

// ErrRiskViolationTransition reports a review action that no longer applies.
var ErrRiskViolationTransition = errors.New("invalid risk violation transition")

const (
	RiskViolationPending = "pending"
	RiskViolationUpheld  = "upheld"
	RiskViolationRevoked = "revoked"

	RiskActionObserved     = "observed"
	RiskActionRejected     = "rejected"
	RiskActionBanSuggested = "ban_suggested"
	RiskActionAutoBanned   = "auto_banned"

	riskScoreFull          = 100
	riskRecoveryIdleDays   = 7
	riskRecoveryPointsDay  = 5
	riskDuplicateWindowSec = 600
)

// RiskViolation is one request that the decision model judged violating,
// with the submitted JSON kept as evidence for review and appeals.
// RequestBody is MEDIUMTEXT on MySQL and VARCHAR(10485760), PostgreSQL's
// largest varchar, elsewhere; stored evidence is capped at 3 MB.
type RiskViolation struct {
	Id                   int    `json:"id"`
	RequestId            string `json:"request_id" gorm:"type:varchar(64);uniqueIndex:idx_risk_violation_request,priority:1"`
	ContentHash          string `json:"content_hash" gorm:"type:varchar(64);uniqueIndex:idx_risk_violation_request,priority:2;index:idx_risk_violation_dedup,priority:2"`
	UserId               int    `json:"user_id" gorm:"index:idx_risk_violation_dedup,priority:1"`
	TokenId              int    `json:"token_id"`
	GroupName            string `json:"group_name" gorm:"type:varchar(128)"`
	Namespace            string `json:"namespace" gorm:"type:varchar(32)"`
	Mode                 string `json:"mode" gorm:"type:varchar(12)"`
	Model                string `json:"model" gorm:"type:varchar(128)"`
	Category             string `json:"category" gorm:"type:varchar(32)"`
	Keyword              string `json:"keyword" gorm:"type:varchar(160)"`
	Rule                 string `json:"rule" gorm:"type:varchar(255)"`
	ProviderModel        string `json:"provider_model" gorm:"type:varchar(120)"`
	Scores               string `json:"scores" gorm:"type:text"`
	Deduction            int    `json:"deduction"`
	ScoreBefore          int    `json:"score_before"`
	ScoreAfter           int    `json:"score_after"`
	Action               string `json:"action" gorm:"type:varchar(16)"`
	DuplicateOf          int    `json:"duplicate_of"`
	Status               string `json:"status" gorm:"type:varchar(12);index:idx_risk_violation_status"`
	ReviewedBy           int    `json:"reviewed_by"`
	ReviewedAt           int64  `json:"reviewed_at" gorm:"bigint"`
	ReviewNote           string `json:"review_note" gorm:"type:varchar(500)"`
	NotificationStatus   string `json:"notification_status" gorm:"type:varchar(12)"`
	RequestBody          string `json:"request_body,omitempty" gorm:"size:10485760"`
	RequestBodyBytes     int    `json:"request_body_bytes"`
	RequestBodyTruncated bool   `json:"request_body_truncated"`
	CreatedAt            int64  `json:"created_at" gorm:"bigint;index:idx_risk_violation_dedup,priority:3;index:idx_risk_violation_time"`
}

// RiskUserScore is a user's reputation as of the last deduction. The current
// score adds the recovery earned since then and is never stored.
type RiskUserScore struct {
	UserId     int   `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Score      int   `json:"score"`
	ScoredAt   int64 `json:"scored_at" gorm:"bigint"`
	Violations int   `json:"violations"`
}

// Current is the score at now: full without a row; otherwise the stored score
// plus five points a day from the seventh day after the last deduction.
func (s RiskUserScore) Current(now int64) int {
	if s.UserId == 0 {
		return riskScoreFull
	}
	recovered := 0
	if days := (now - s.ScoredAt) / 86400; days >= riskRecoveryIdleDays {
		recovered = int(days-riskRecoveryIdleDays+1) * riskRecoveryPointsDay
	}
	return min(riskScoreFull, s.Score+recovered)
}

// RecordRiskViolation stores a model-confirmed violation once per request and
// content, reporting whether it was new. In enforce mode it deducts points; a
// violation while the score is already zero is a ban, which the caller
// applies when autoBan is set. With notify, a new non-duplicate enforce-mode
// violation is marked for an email notice.
func RecordRiskViolation(v *RiskViolation, deduction int, autoBan, notify bool, now int64) (bool, bool, error) {
	created, ban := false, false
	err := riskTransaction(func(tx *gorm.DB) error {
		created, ban = false, false
		var existing RiskViolation
		err := tx.Omit("request_body").Where("request_id = ? AND content_hash = ?", v.RequestId, v.ContentHash).First(&existing).Error
		if err == nil {
			*v = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var score RiskUserScore
		if err := lockForUpdate(tx).Where("user_id = ?", v.UserId).Limit(1).Find(&score).Error; err != nil {
			return err
		}
		before := score.Current(now)
		v.ScoreBefore, v.ScoreAfter, v.Deduction = before, before, 0
		v.Id, v.DuplicateOf, v.Status, v.CreatedAt, v.NotificationStatus = 0, 0, RiskViolationPending, now, ""
		v.Action = RiskActionObserved
		if v.Mode == "enforce" {
			v.Action = RiskActionRejected
			var first RiskViolation
			err := tx.Select("id").Where("user_id = ? AND content_hash = ? AND created_at > ? AND duplicate_of = 0 AND mode = ? AND status <> ?",
				v.UserId, v.ContentHash, now-riskDuplicateWindowSec, "enforce", RiskViolationRevoked).Order("id desc").First(&first).Error
			switch {
			case err == nil:
				v.DuplicateOf = first.Id
			case !errors.Is(err, gorm.ErrRecordNotFound):
				return err
			default:
				if before == 0 {
					v.Action = RiskActionBanSuggested
					if autoBan {
						v.Action, ban = RiskActionAutoBanned, true
					}
				}
				// Record the points actually removed, so revoking refunds
				// exactly that much even when the score was already low.
				v.ScoreAfter = max(0, before-deduction)
				v.Deduction = before - v.ScoreAfter
				if score.UserId == 0 {
					score = RiskUserScore{UserId: v.UserId, Score: v.ScoreAfter, ScoredAt: now, Violations: 1}
					if err := tx.Create(&score).Error; err != nil {
						return err
					}
				} else if err := tx.Model(&RiskUserScore{}).Where("user_id = ?", v.UserId).Updates(map[string]any{
					"score": v.ScoreAfter, "scored_at": now, "violations": gorm.Expr("violations + 1"),
				}).Error; err != nil {
					return err
				}
				if notify {
					v.NotificationStatus = "pending"
				}
			}
		}
		created = true
		return tx.Create(v).Error
	})
	return created, ban, err
}

// SetRiskViolationAction records that an automatic ban was not applied.
func SetRiskViolationAction(id int, action string) error {
	return DB.Model(&RiskViolation{}).Where("id = ?", id).Update("action", action).Error
}

func SetRiskViolationNotification(id int, status string) error {
	return DB.Model(&RiskViolation{}).Where("id = ?", id).Update("notification_status", status).Error
}

func GetRiskUserScore(userID int, now int64) (int, error) {
	var score RiskUserScore
	err := DB.Where("user_id = ?", userID).Limit(1).Find(&score).Error
	return score.Current(now), err
}

type RiskViolationFilter struct {
	Status    string
	UserId    int
	RequestId string
}

func ListRiskViolations(filter RiskViolationFilter, offset, limit int) ([]RiskViolation, int64, error) {
	query := DB.Model(&RiskViolation{})
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.RequestId != "" {
		query = query.Where("request_id = ?", filter.RequestId)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]RiskViolation, 0)
	err := query.Omit("request_body").Order("id desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func GetRiskViolation(id int) (*RiskViolation, error) {
	var row RiskViolation
	if err := DB.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func CountPendingRiskViolations() (int64, error) {
	var count int64
	err := DB.Model(&RiskViolation{}).Where("status = ?", RiskViolationPending).Count(&count).Error
	return count, err
}

// ResolveRiskViolation upholds a pending violation or revokes a pending or
// upheld one. Revoking returns its deduction to the user's score.
func ResolveRiskViolation(id, operatorID int, action, note string, now int64) (*RiskViolation, error) {
	var row RiskViolation
	err := riskTransaction(func(tx *gorm.DB) error {
		if err := tx.Omit("request_body").First(&row, id).Error; err != nil {
			return err
		}
		next := ""
		switch {
		case action == "uphold" && row.Status == RiskViolationPending:
			next = RiskViolationUpheld
		case action == "revoke" && row.Status != RiskViolationRevoked:
			next = RiskViolationRevoked
		default:
			return ErrRiskViolationTransition
		}
		result := tx.Model(&RiskViolation{}).Where("id = ? AND status = ?", id, row.Status).Updates(map[string]any{
			"status": next, "reviewed_by": operatorID, "reviewed_at": now, "review_note": note,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrRiskViolationTransition
		}
		if next == RiskViolationRevoked && row.Deduction > 0 {
			if err := tx.Model(&RiskUserScore{}).Where("user_id = ?", row.UserId).Update("score", gorm.Expr("score + ?", row.Deduction)).Error; err != nil {
				return err
			}
		}
		row.Status, row.ReviewedBy, row.ReviewedAt, row.ReviewNote = next, operatorID, now, note
		return nil
	})
	return &row, err
}
