package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	RiskJudgmentVersion    = 3
	RiskViolationDeduction = 20
)

type RiskAssessment struct {
	Category         string             `json:"category"`
	Severity         string             `json:"severity"`
	SeverityStatus   string             `json:"severity_status"`
	SafetyScore      float64            `json:"safety_score"`
	SeverityScores   map[string]float64 `json:"severity_scores,omitempty"`
	AppliedDeduction int                `json:"applied_deduction"`
	Reason           string             `json:"reason,omitempty"`
}

type RiskReputationSummary struct {
	Score           int   `json:"score"`
	InitialScore    int   `json:"initial_score"`
	BanThreshold    int   `json:"ban_threshold"`
	ConfirmedEvents int   `json:"confirmed_events"`
	TotalDeducted   int   `json:"total_deducted"`
	RecoveredPoints int   `json:"recovered_points"`
	LastViolationAt int64 `json:"last_violation_at"`
	NextRecoveryAt  int64 `json:"next_recovery_at"`
}

// RiskReputationEntry contains no request text and survives evidence retention.
// ReviewId is an audit reference, not a foreign key: evidence may be pruned.
type RiskReputationEntry struct {
	Id          int    `json:"id"`
	EventKey    string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_risk_reputation_event"`
	UserId      int    `json:"user_id" gorm:"index:idx_risk_reputation_user,priority:1"`
	TokenId     int    `json:"token_id"`
	IncidentKey string `json:"-" gorm:"type:varchar(64)"`
	ReviewId    int    `json:"review_id" gorm:"index:idx_risk_reputation_review"`
	Namespace   string `json:"namespace" gorm:"type:varchar(32)"`
	Category    string `json:"category" gorm:"type:varchar(32)"`
	Severity    string `json:"severity" gorm:"type:varchar(12)"`
	Deduction   int    `json:"deduction"`
	ConfirmedAt int64  `json:"confirmed_at" gorm:"bigint;index:idx_risk_reputation_user,priority:2"`
	RevokedAt   int64  `json:"revoked_at" gorm:"bigint"`
	RevokedBy   int    `json:"revoked_by"`
}

func recordRiskReputation(tx *gorm.DB, row *RiskReview, now int64) error {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%d\x00%s", row.UserId, row.TokenId, row.RequestId)))
	key := hex.EncodeToString(hash[:])
	var existing RiskReputationEntry
	err := tx.Where("event_key = ?", key).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && row.IncidentKey != "" {
		err = tx.Where("user_id = ? AND token_id = ? AND incident_key = ? AND confirmed_at > ? AND confirmed_at <= ?", row.UserId, row.TokenId, row.IncidentKey, now-600, now).Order("id desc").First(&existing).Error
	}
	if err == nil {
		if existing.EventKey == key && existing.IncidentKey != "" && row.IncidentKey != existing.IncidentKey {
			return ErrRiskReviewTransition
		}
		if existing.RevokedAt != 0 {
			return ErrRiskReviewTransition
		}
		row.ReputationEntryId, row.Deduction = existing.Id, 0
		row.NotificationStatus, row.NotificationAt, row.NotificationError = "skipped", now, "duplicate_incident"
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	entry := RiskReputationEntry{EventKey: key, UserId: row.UserId, TokenId: row.TokenId, ReviewId: row.Id, IncidentKey: row.IncidentKey, Namespace: row.Namespace, Category: row.Category, Severity: row.Severity, Deduction: row.Deduction, ConfirmedAt: now}
	if err := tx.Create(&entry).Error; err != nil {
		return err
	}
	row.ReputationEntryId = entry.Id
	return nil
}

// riskReviewDeduction validates the model classification and applies the fixed
// charge once. Historical severity settings cannot change a new deduction.
func riskReviewDeduction(row *RiskReview, automatic bool) error {
	var assessments []RiskAssessment
	if automatic {
		if row.JudgmentVersion != RiskJudgmentVersion || common.UnmarshalJsonStr(row.Assessments, &assessments) != nil || len(assessments) == 0 || len(assessments) > 32 {
			return ErrRiskReviewTransition
		}
	} else {
		assessments = []RiskAssessment{{Category: row.Category}}
	}
	categoryPresent := false
	for i := range assessments {
		assessment := &assessments[i]
		switch assessment.Category {
		case "nsfw", "jailbreak", "cyber", "pentest", "reverse", "crack", "distill", "custom", "safety":
		default:
			return ErrRiskReviewTransition
		}
		if automatic && (assessment.SafetyScore < 0 || assessment.SafetyScore > 1) {
			return ErrRiskReviewTransition
		}
		if assessment.Category == row.Category {
			categoryPresent = true
		}
		assessment.Severity, assessment.SeverityStatus, assessment.SeverityScores = "", "", nil
		assessment.AppliedDeduction = RiskViolationDeduction
	}
	if !categoryPresent {
		return ErrRiskReviewTransition
	}
	if automatic {
		encoded, err := common.Marshal(assessments)
		if err != nil {
			return err
		}
		row.Assessments = string(encoded)
	}
	row.Severity, row.SeverityStatus, row.Deduction = "", "", RiskViolationDeduction
	row.JudgmentVersion = RiskJudgmentVersion
	return nil
}

// revokeRiskReputation replays history at each active restriction's trigger.
// Later unrelated violations cannot keep a false-positive restriction alive.
func revokeRiskReputation(tx *gorm.DB, row *RiskReview, operatorID int, now int64) error {
	revokedReviews := map[int]bool{row.Id: true}
	if row.ReputationEntryId > 0 {
		var linked []RiskReview
		if err := tx.Where("reputation_entry_id = ? AND user_id = ? AND token_id = ? AND id <> ? AND status = ?", row.ReputationEntryId, row.UserId, row.TokenId, row.Id, RiskReviewConfirmed).Find(&linked).Error; err != nil {
			return err
		}
		for _, other := range linked {
			other.Status, other.Points = RiskReviewRevoked, 0
			other.Revision++
			other.ReviewedAt, other.ReviewedBy, other.ReviewNote = now, operatorID, "linked incident revoked"
			if other.NotificationStatus == "pending" || other.NotificationStatus == "sending" {
				other.NotificationStatus, other.NotificationAt, other.NotificationError = "skipped", now, "review_withdrawn"
			}
			var history []riskReviewHistory
			if err := common.UnmarshalJsonStr(other.History, &history); err != nil {
				return err
			}
			history = append(history, riskReviewHistory{Action: "revoke", At: now, OperatorId: operatorID, Note: other.ReviewNote, Revision: other.Revision})
			encoded, err := common.Marshal(history)
			if err != nil {
				return err
			}
			other.History = string(encoded)
			if err := clearRiskReviewEvidence(tx, &other); err != nil {
				return err
			}
			if err := tx.Save(&other).Error; err != nil {
				return err
			}
			revokedReviews[other.Id] = true
		}
	}
	if err := tx.Model(&RiskReputationEntry{}).Where("(review_id = ? OR id = ?) AND user_id = ? AND revoked_at = 0", row.Id, row.ReputationEntryId, row.UserId).Updates(map[string]any{"revoked_at": now, "revoked_by": operatorID}).Error; err != nil {
		return err
	}
	var entries []RiskReputationEntry
	if err := tx.Where("user_id = ?", row.UserId).Order("confirmed_at asc").Order("id asc").Find(&entries).Error; err != nil {
		return err
	}
	var blocks []RiskSubjectBlock
	if err := tx.Where("user_id = ? AND subject_type = ? AND released_at = 0", row.UserId, "token").Where("expires_at = 0 OR expires_at > ?", now).Find(&blocks).Error; err != nil {
		return err
	}
	for _, block := range blocks {
		triggerReviewID, _ := strconv.Atoi(strings.TrimPrefix(block.SourceRequestId, "risk-review:"))
		release := block.Reason == "manual_review" && revokedReviews[triggerReviewID]
		if block.Reason == "behavior_score" {
			triggerID, err := strconv.Atoi(strings.TrimPrefix(block.SourceRequestId, "risk-review:"))
			if err != nil {
				continue
			}
			found := false
			for i, entry := range entries {
				if entry.ReviewId != triggerID {
					continue
				}
				found = true
				release = entry.RevokedAt != 0 || replayRiskReputation(entries[:i+1], entry.ConfirmedAt).Score > 0
				break
			}
			if !found {
				var trigger RiskReview
				err := tx.Select("id", "block_review_ids").First(&trigger, triggerID).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err == nil {
					var ids []int
					if err := common.UnmarshalJsonStr(trigger.BlockReviewIds, &ids); err != nil {
						return err
					}
					for _, id := range ids {
						if id == row.Id {
							release = true
							break
						}
					}
				}
			}
		}
		if release {
			basis, expires, automatic, err := strongestRiskBlockBasis(tx, &block, entries, now)
			if err != nil {
				return err
			}
			if basis != nil {
				reason := "manual_review"
				if automatic {
					reason = "behavior_score"
				}
				if err := tx.Model(&RiskSubjectBlock{}).Where("id = ?", block.Id).Updates(map[string]any{"reason": reason, "source_request_id": fmt.Sprintf("risk-review:%d", basis.Id), "fingerprint": basis.Fingerprint, "expires_at": expires}).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Model(&RiskSubjectBlock{}).Where("id = ?", block.Id).Updates(map[string]any{"released_at": now, "released_by": operatorID}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// recordRiskBlockBasis records only restrictions actually authorized at the
// confirmation/ban transition. A low score alone is never a restriction basis.
func recordRiskBlockBasis(tx *gorm.DB, row *RiskReview, blockID int, expires int64, automatic bool) error {
	var history []riskReviewHistory
	if err := common.UnmarshalJsonStr(row.History, &history); err != nil {
		return err
	}
	if len(history) == 0 || (automatic && expires <= 0) {
		return ErrRiskReviewTransition
	}
	last := &history[len(history)-1]
	last.BlockId, last.BlockExpiresAt, last.AutomaticBlock = blockID, expires, automatic
	encoded, err := common.Marshal(history)
	if err != nil {
		return err
	}
	row.History = string(encoded)
	ids, err := common.Marshal([]int{row.Id})
	if err != nil {
		return err
	}
	row.BlockReviewIds = string(ids)
	return tx.Model(row).Updates(map[string]any{"history": row.History, "block_review_ids": row.BlockReviewIds}).Error
}

// strongestRiskBlockBasis preserves independently authorized restrictions and
// their original expiry. Automatic bases must still reach zero when replayed.
func strongestRiskBlockBasis(tx *gorm.DB, block *RiskSubjectBlock, entries []RiskReputationEntry, now int64) (*RiskReview, int64, bool, error) {
	var rows []RiskReview
	if err := tx.Select("id", "namespace", "history", "fingerprint", "reputation_entry_id").Where("user_id = ? AND token_id = ? AND status = ? AND mode = ?", block.UserId, block.SubjectValue, RiskReviewConfirmed, "enforce").Order("id asc").Find(&rows).Error; err != nil {
		return nil, 0, false, err
	}
	var best *RiskReview
	var bestExpiry int64
	var bestAutomatic bool
	for i := range rows {
		var history []riskReviewHistory
		if err := common.UnmarshalJsonStr(rows[i].History, &history); err != nil {
			return nil, 0, false, err
		}
		for _, command := range history {
			if command.BanReleasedAt != 0 || (command.Action != "ban" && !command.AutomaticBlock) {
				continue
			}
			if command.BlockId != block.Id && !(command.BlockId == 0 && command.Action == "ban" && rows[i].Namespace == block.Namespace) {
				continue
			}
			expires := command.BlockExpiresAt
			if command.BlockId == 0 && command.BlockTTLMinutes > 0 {
				expires = command.At + int64(command.BlockTTLMinutes)*60
			}
			if command.AutomaticBlock {
				valid := false
				for index, entry := range entries {
					if entry.Id == rows[i].ReputationEntryId && entry.ReviewId == rows[i].Id && entry.Deduction > 0 && entry.RevokedAt == 0 {
						valid = replayRiskReputation(entries[:index+1], entry.ConfirmedAt).Score == 0
						break
					}
				}
				if !valid || expires <= 0 {
					continue
				}
			}
			if expires != 0 && expires <= now {
				continue
			}
			if best == nil || bestExpiry != 0 && (expires == 0 || expires > bestExpiry) {
				best, bestExpiry, bestAutomatic = &rows[i], expires, command.AutomaticBlock
			}
		}
	}
	return best, bestExpiry, bestAutomatic, nil
}

func releaseRiskBlockBases(tx *gorm.DB, block *RiskSubjectBlock, operatorID int, now int64, newDecision *RiskReview) error {
	if block.SubjectType != "token" || block.UserId <= 0 {
		return nil
	}
	var rows []RiskReview
	if err := tx.Select("id", "namespace", "history").Where("user_id = ? AND token_id = ? AND status = ?", block.UserId, block.SubjectValue, RiskReviewConfirmed).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var history []riskReviewHistory
		if err := common.UnmarshalJsonStr(row.History, &history); err != nil {
			return err
		}
		changed := false
		for i := range history {
			command := &history[i]
			if newDecision != nil && row.Id == newDecision.Id && command.Revision == newDecision.Revision {
				continue
			}
			linked := command.BlockId == block.Id || (command.BlockId == 0 && command.Action == "ban" && row.Namespace == block.Namespace)
			if linked && (command.Action == "ban" || command.AutomaticBlock) && command.BanReleasedAt == 0 {
				history[i].BanReleasedAt, history[i].BanReleasedBy = now, operatorID
				changed = true
			}
		}
		if !changed {
			continue
		}
		encoded, err := common.Marshal(history)
		if err != nil {
			return err
		}
		if err := tx.Model(&RiskReview{}).Where("id = ?", row.Id).Update("history", string(encoded)).Error; err != nil {
			return err
		}
		if newDecision != nil && row.Id == newDecision.Id {
			newDecision.History = string(encoded)
		}
	}
	return nil
}

func GetRiskUserReputation(userID int, now int64) (RiskReputationSummary, error) {
	return riskUserReputation(DB, userID, now)
}

func riskUserReputation(tx *gorm.DB, userID int, now int64) (RiskReputationSummary, error) {
	var entries []RiskReputationEntry
	err := tx.Where("user_id = ? AND revoked_at = 0 AND confirmed_at <= ?", userID, now).Order("confirmed_at asc").Order("id asc").Find(&entries).Error
	if err != nil {
		return RiskReputationSummary{}, err
	}
	return replayRiskReputation(entries, now), nil
}

func replayRiskReputation(entries []RiskReputationEntry, now int64) RiskReputationSummary {
	result := RiskReputationSummary{Score: 100, InitialScore: 100}
	for _, entry := range entries {
		if entry.RevokedAt != 0 || entry.ConfirmedAt > now {
			continue
		}
		recoverRiskReputation(&result, entry.ConfirmedAt)
		result.Score = max(0, result.Score-entry.Deduction)
		result.ConfirmedEvents++
		result.TotalDeducted += entry.Deduction
		result.LastViolationAt = entry.ConfirmedAt
		result.NextRecoveryAt = entry.ConfirmedAt + 7*86400
		if result.Score == 100 {
			result.NextRecoveryAt = 0
		}
	}
	recoverRiskReputation(&result, now)
	return result
}

func recoverRiskReputation(result *RiskReputationSummary, now int64) {
	if result.NextRecoveryAt == 0 || now < result.NextRecoveryAt {
		return
	}
	days := (now-result.NextRecoveryAt)/86400 + 1
	amount := min(int64(100-result.Score), min(days, 20)*5)
	result.Score += int(amount)
	result.RecoveredPoints += int(amount)
	result.NextRecoveryAt += days * 86400
	if result.Score == 100 {
		result.NextRecoveryAt = 0
	}
}
