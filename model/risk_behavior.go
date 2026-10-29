package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	RiskReviewPending   = "pending"
	RiskReviewConfirmed = "confirmed"
	RiskReviewDismissed = "dismissed"
	RiskReviewRevoked   = "revoked"
	RiskReviewDuplicate = "duplicate"
)

var ErrRiskReviewTransition = errors.New("invalid risk review transition")

// RiskReview is the durable, deduplicated review record. Raw request events
// remain immutable; only a confirmed enforce-mode review contributes points.
type RiskReview struct {
	Id                       int    `json:"id"`
	RequestKey               string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_risk_review_request"`
	DuplicateOf              int    `json:"-"`
	RequestId                string `json:"request_id" gorm:"type:varchar(64)"`
	CreatedAt                int64  `json:"created_at" gorm:"bigint;index:idx_risk_review_scope,priority:3"`
	Namespace                string `json:"namespace" gorm:"type:varchar(32);index:idx_risk_review_scope,priority:1"`
	GroupName                string `json:"group_name" gorm:"type:varchar(128)"`
	UserId                   int    `json:"user_id" gorm:"index:idx_risk_review_scope,priority:2"`
	TokenId                  int    `json:"token_id"`
	JudgmentSource           string `json:"judgment_source" gorm:"type:varchar(12)"`
	ScreeningScore           int    `json:"screening_score"`
	ScreeningReason          string `json:"screening_reason" gorm:"type:varchar(64)"`
	ConfigRevision           int64  `json:"config_revision" gorm:"bigint"`
	OriginalRequest          string `json:"original_request" gorm:"size:1048576"`
	OriginalRequestTruncated bool   `json:"original_request_truncated"`
	OriginalRequestHash      string `json:"original_request_hash" gorm:"type:varchar(64)"`
	NotificationStatus       string `json:"notification_status" gorm:"type:varchar(12);index:idx_risk_review_notification"`
	NotificationAt           int64  `json:"notification_at" gorm:"bigint"`
	NotificationError        string `json:"notification_error" gorm:"type:varchar(48)"`
	Mode                     string `json:"mode" gorm:"type:varchar(12)"`
	Layer                    string `json:"layer" gorm:"type:varchar(16)"`
	Category                 string `json:"category" gorm:"type:varchar(32)"`
	Reason                   string `json:"reason" gorm:"type:varchar(64)"`
	Model                    string `json:"model" gorm:"type:varchar(128)"`
	ProviderModel            string `json:"provider_model" gorm:"type:varchar(120)"`
	Scores                   string `json:"scores" gorm:"type:text"`
	PolicyKey                string `json:"policy_key" gorm:"type:varchar(64)"`
	PolicyRevision           int    `json:"policy_revision"`
	Fingerprint              string `json:"fingerprint" gorm:"type:varchar(64)"`
	IncidentKey              string `json:"incident_key" gorm:"type:varchar(64)"`
	Evidence                 string `json:"evidence" gorm:"size:131072"`
	EvidenceTruncated        bool   `json:"evidence_truncated"`
	Coverage                 string `json:"coverage" gorm:"type:varchar(16)"`
	Status                   string `json:"status" gorm:"type:varchar(12);index:idx_risk_review_status"`
	Points                   int    `json:"points"`
	Revision                 int    `json:"revision"`
	ReviewedAt               int64  `json:"reviewed_at" gorm:"bigint"`
	ReviewedBy               int    `json:"reviewed_by"`
	ReviewNote               string `json:"review_note" gorm:"type:varchar(500)"`
	History                  string `json:"history" gorm:"type:text"`
	BlockReviewIds           string `json:"-" gorm:"type:text"`
}

type RiskBehaviorPolicy struct {
	WindowMinutes          int           `json:"window_minutes"`
	Threshold              int           `json:"threshold"`
	BlockTTLMinutes        int           `json:"block_ttl_minutes"`
	ExpectedChangeRevision int64         `json:"-"`
	ExpectedConfigRevision int64         `json:"-"`
	ExpectedProvider       *RiskProvider `json:"-"`
}

func (p RiskBehaviorPolicy) normalized() RiskBehaviorPolicy {
	if p.WindowMinutes <= 0 {
		p.WindowMinutes = 10
	}
	if p.Threshold <= 0 {
		p.Threshold = 6
	}
	if p.BlockTTLMinutes <= 0 {
		p.BlockTTLMinutes = 30
	}
	return p
}

type RiskBehaviorScore struct {
	Score           int `json:"score"`
	ConfirmedEvents int `json:"confirmed_events"`
	WindowMinutes   int `json:"window_minutes"`
	Threshold       int `json:"threshold"`
	BlockTTLMinutes int `json:"block_ttl_minutes"`
}

type RiskReviewResult struct {
	Review *RiskReview       `json:"review"`
	Score  RiskBehaviorScore `json:"score"`
	Block  *RiskSubjectBlock `json:"block,omitempty"`
}

type RiskReviewFilter struct {
	Namespace string
	UserId    int
	TokenId   int
	Status    string
	RequestId string
}

// CreateRiskReviewCandidate never modifies a prior review, including a
// dismissed or revoked review. Concurrent request retries cannot add points.
func CreateRiskReviewCandidate(input *RiskReview, policy RiskBehaviorPolicy, now int64) (*RiskReview, bool, error) {
	if input == nil || input.RequestId == "" || input.Namespace == "" || input.UserId <= 0 {
		return nil, false, ErrRiskReviewTransition
	}
	policy = policy.normalized()
	var saved RiskReview
	created := false
	err := riskTransaction(func(tx *gorm.DB) error {
		created = false
		saved = RiskReview{}
		if _, err := lockRiskState(tx); err != nil {
			return err
		}
		requestKey := fmt.Sprintf("%s\x00%d\x00%s", input.Namespace, input.UserId, input.RequestId)
		if input.TokenId > 0 {
			requestKey = fmt.Sprintf("%s\x00%d\x00%d\x00%s", input.Namespace, input.UserId, input.TokenId, input.RequestId)
		}
		hash := sha256.Sum256([]byte(requestKey))
		key := hex.EncodeToString(hash[:])
		err := tx.Where("request_key = ?", key).First(&saved).Error
		if err == nil {
			if saved.DuplicateOf != 0 {
				originalID := saved.DuplicateOf
				saved = RiskReview{}
				return tx.First(&saved, originalID).Error
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if input.IncidentKey != "" || input.Fingerprint != "" {
			query := tx.Where("namespace = ? AND user_id = ? AND token_id = ? AND created_at >= ? AND duplicate_of = 0", input.Namespace, input.UserId, input.TokenId, now-int64(policy.WindowMinutes)*60)
			if input.IncidentKey != "" {
				query = query.Where("incident_key = ?", input.IncidentKey)
			} else {
				query = query.Where("fingerprint = ?", input.Fingerprint)
			}
			err = query.Order("id desc").First(&saved).Error
			if err == nil {
				alias := RiskReview{RequestKey: key, RequestId: input.RequestId, Namespace: input.Namespace, UserId: input.UserId, TokenId: input.TokenId, Fingerprint: input.Fingerprint, IncidentKey: input.IncidentKey, CreatedAt: now, DuplicateOf: saved.Id, Status: RiskReviewDuplicate, Revision: 1}
				return tx.Create(&alias).Error
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		saved = *input
		saved.Id = 0
		saved.DuplicateOf = 0
		saved.RequestKey = key
		saved.CreatedAt = now
		saved.Status, saved.Points, saved.Revision = RiskReviewPending, 0, 1
		saved.ReviewedAt, saved.ReviewedBy, saved.ReviewNote, saved.History, saved.BlockReviewIds = 0, 0, "", "[]", ""
		saved.NotificationStatus, saved.NotificationAt, saved.NotificationError = "", 0, ""
		if original := []rune(saved.OriginalRequest); len(original) > 262144 {
			saved.OriginalRequest = string(original[:262144])
			saved.OriginalRequestTruncated = true
		}
		if saved.OriginalRequestHash == "" && input.OriginalRequest != "" {
			hash := sha256.Sum256([]byte(input.OriginalRequest))
			saved.OriginalRequestHash = hex.EncodeToString(hash[:])
		}
		if evidence := []rune(saved.Evidence); len(evidence) > 32768 {
			saved.Evidence = string(evidence[:32768])
			saved.EvidenceTruncated = true
		}
		if err := tx.Create(&saved).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return &saved, created, err
}

func GetRiskReview(id int) (*RiskReview, error) {
	var row RiskReview
	if err := DB.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func ListRiskReviews(filter RiskReviewFilter, offset, limit int) ([]RiskReview, int64, error) {
	query := DB.Model(&RiskReview{}).Where("duplicate_of = 0")
	if filter.Namespace != "" {
		query = query.Where("namespace = ?", filter.Namespace)
	}
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.TokenId > 0 {
		query = query.Where("token_id = ?", filter.TokenId)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.RequestId != "" {
		query = query.Where("request_id = ?", filter.RequestId)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]RiskReview, 0)
	err := query.Omit("evidence", "original_request", "history", "block_review_ids").Order("id desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func riskBehaviorScore(tx *gorm.DB, namespace string, userID, tokenID int, policy RiskBehaviorPolicy, now int64) (RiskBehaviorScore, []RiskReview, error) {
	policy = policy.normalized()
	result := RiskBehaviorScore{WindowMinutes: policy.WindowMinutes, Threshold: policy.Threshold, BlockTTLMinutes: policy.BlockTTLMinutes}
	var rows []RiskReview
	query := tx.Select("id", "points").Where("namespace = ? AND user_id = ? AND token_id = ? AND status = ? AND mode = ? AND created_at > ? AND created_at <= ?", namespace, userID, tokenID, RiskReviewConfirmed, "enforce", now-int64(policy.WindowMinutes)*60, now)
	if tokenID > 0 {
		// Check every contributing row in SQL without loading its evidence.
		// Legacy or partially written confirmations cannot trigger a token ban.
		query = query.Where("evidence <> '' AND original_request <> '' AND evidence_truncated = ? AND original_request_truncated = ?", false, false)
	}
	err := query.Find(&rows).Error
	for _, row := range rows {
		result.Score += row.Points
	}
	result.ConfirmedEvents = len(rows)
	return result, rows, err
}

func GetRiskBehaviorScore(namespace string, userID int, policy RiskBehaviorPolicy, now int64) (RiskBehaviorScore, error) {
	return GetRiskTokenBehaviorScore(namespace, userID, 0, policy, now)
}

func GetRiskTokenBehaviorScore(namespace string, userID, tokenID int, policy RiskBehaviorPolicy, now int64) (RiskBehaviorScore, error) {
	score, _, err := riskBehaviorScore(DB, namespace, userID, tokenID, policy, now)
	return score, err
}

func riskModelReviewComplete(row *RiskReview) bool {
	complete := row.TokenId > 0 && row.Mode == "enforce" && row.Category != "" && row.ProviderModel != "" && row.Scores != "" &&
		riskReviewEvidenceComplete(row) &&
		(row.Coverage == "complete" || row.Coverage == "tail_window")
	if !complete {
		return false
	}
	var scores map[string]float64
	if common.UnmarshalJsonStr(row.Scores, &scores) != nil || len(scores) == 0 {
		return false
	}
	for _, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return false
		}
	}
	return true
}

func riskReviewEvidenceComplete(row *RiskReview) bool {
	return row.Evidence != "" && !row.EvidenceTruncated && row.OriginalRequest != "" && !row.OriginalRequestTruncated
}

// CreateRiskModelReview accepts only a service-confirmed violating verdict.
// Evidence is committed first, so a failed confirmation leaves a reviewable
// pending record. Replays cannot resurrect a withdrawn or already reviewed hit.
func CreateRiskModelReview(input *RiskReview, policy RiskBehaviorPolicy, autoConfirm, allowAutoBlock bool, now int64) (*RiskReviewResult, error) {
	if input == nil || input.Category == "" || input.ProviderModel == "" || input.Scores == "" {
		return nil, ErrRiskReviewTransition
	}
	row, _, err := CreateRiskReviewCandidate(input, policy, now)
	if err != nil {
		return nil, err
	}
	if autoConfirm && riskModelReviewComplete(input) && riskModelReviewComplete(row) && row.Status == RiskReviewPending {
		judgment := *input
		return resolveRiskReview(row.Id, row.Revision, 0, "confirm", judgment.Category, "model_confirmed", policy, allowAutoBlock, now, &judgment, -1)
	}
	score, err := GetRiskTokenBehaviorScore(row.Namespace, row.UserId, row.TokenId, policy, now)
	return &RiskReviewResult{Review: row, Score: score}, err
}

// GetActiveRiskTokenBlock is intentionally namespace-independent: a blocked
// API token cannot escape its restriction by selecting another routing group.
func GetActiveRiskTokenBlock(userID, tokenID int, now int64) (*RiskSubjectBlock, error) {
	if userID <= 0 || tokenID <= 0 {
		return nil, nil
	}
	var row RiskSubjectBlock
	err := DB.Where("user_id = ? AND subject_type = ? AND subject_value = ? AND released_at = 0", userID, "token", strconv.Itoa(tokenID)).Where("expires_at = 0 OR expires_at > ?", now).Order("expires_at = 0 DESC").Order("expires_at desc").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ClaimRiskReviewNotification never retries an abandoned sending state. A
// process crash after SMTP accepted a message must not create repeated mail.
func ClaimRiskReviewNotification(now int64) (*RiskReview, error) {
	var claimed *RiskReview
	err := riskTransaction(func(tx *gorm.DB) error {
		claimed = nil
		if _, err := lockRiskState(tx); err != nil {
			return err
		}
		if err := tx.Model(&RiskReview{}).Where("notification_status = ? AND notification_at < ?", "sending", now-300).Updates(map[string]any{"notification_status": "failed", "notification_error": "claim_expired", "notification_at": now}).Error; err != nil {
			return err
		}
		var row RiskReview
		err := tx.Omit("evidence", "original_request", "history", "block_review_ids").Where("status = ? AND mode = ? AND notification_status = ?", RiskReviewConfirmed, "enforce", "pending").Order("id asc").First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		row.NotificationStatus, row.NotificationAt = "sending", now
		if err := tx.Model(&row).Updates(map[string]any{"notification_status": "sending", "notification_at": now}).Error; err != nil {
			return err
		}
		claimed = &row
		return nil
	})
	return claimed, err
}

func FinishRiskReviewNotification(id int, status, errorCode string, now int64) error {
	if status != "sent" && status != "failed" && status != "skipped" {
		return ErrRiskReviewTransition
	}
	switch errorCode {
	case "", "no_email", "mail_send_failed", "mail_disabled", "review_withdrawn", "claim_expired", "notification_disabled", "user_not_found":
	case "recipient_lookup_failed", "no_bound_email", "invalid_bound_email", "review_lookup_failed", "review_no_longer_confirmed", "restriction_lookup_failed", "email_delivery_failed":
	default:
		return ErrRiskReviewTransition
	}
	return DB.Model(&RiskReview{}).Where("id = ? AND notification_status = ? AND status = ? AND mode = ?", id, "sending", RiskReviewConfirmed, "enforce").Updates(map[string]any{"notification_status": status, "notification_error": errorCode, "notification_at": now}).Error
}

type riskReviewHistory struct {
	Action          string `json:"action"`
	At              int64  `json:"at"`
	OperatorId      int    `json:"operator_id"`
	Category        string `json:"category"`
	Note            string `json:"note"`
	Revision        int    `json:"revision"`
	Source          string `json:"source,omitempty"`
	BlockTTLMinutes int    `json:"block_ttl_minutes,omitempty"`
}

// ResolveRiskReview is the sole scoring transition. State, score and resulting
// block change atomically. The revision also prevents old review jobs from
// resurrecting a dismissed or revoked decision.
func ResolveRiskReview(id, expectedRevision, operatorID int, action, category, note string, policy RiskBehaviorPolicy, allowAutoBlock bool, now int64) (*RiskReviewResult, error) {
	return resolveRiskReview(id, expectedRevision, operatorID, action, category, note, policy, allowAutoBlock, now, nil, -1)
}

func BanRiskReviewToken(id, expectedRevision, operatorID int, category, note string, ttlMinutes int, policy RiskBehaviorPolicy, now int64) (*RiskReviewResult, error) {
	if ttlMinutes < 0 || ttlMinutes > 1440 {
		return nil, ErrRiskReviewTransition
	}
	return resolveRiskReview(id, expectedRevision, operatorID, "ban", category, note, policy, false, now, nil, ttlMinutes)
}

func resolveRiskReview(id, expectedRevision, operatorID int, action, category, note string, policy RiskBehaviorPolicy, allowAutoBlock bool, now int64, judgment *RiskReview, manualTTL int) (*RiskReviewResult, error) {
	policy = policy.normalized()
	modelConfirm := judgment != nil
	if (!modelConfirm && operatorID <= 0) || len([]rune(note)) > 500 || len(category) > 32 {
		return nil, ErrRiskReviewTransition
	}
	category = strings.TrimSpace(category)
	var result RiskReviewResult
	err := riskTransaction(func(tx *gorm.DB) error {
		result = RiskReviewResult{}
		state, err := lockRiskState(tx)
		if err != nil {
			return err
		}
		if policy.ExpectedConfigRevision > 0 {
			var revision int64
			if err := tx.Model(&RiskConfig{}).Select("COALESCE(MAX(revision), 0)").Scan(&revision).Error; err != nil {
				return err
			}
			if revision != policy.ExpectedConfigRevision {
				return ErrRiskRevisionConflict
			}
		} else if !modelConfirm && policy.ExpectedChangeRevision != 0 && state.ChangeRevision != policy.ExpectedChangeRevision {
			return ErrRiskRevisionConflict
		}
		if modelConfirm && policy.ExpectedProvider != nil {
			expected := policy.ExpectedProvider
			var provider RiskProvider
			if err := tx.First(&provider, expected.Id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrRiskRevisionConflict
				}
				return err
			}
			if provider.ProviderType != expected.ProviderType || provider.BaseURL != expected.BaseURL || provider.Model != expected.Model || provider.Proxy != expected.Proxy || provider.KeysRevision != expected.KeysRevision {
				return ErrRiskRevisionConflict
			}
		}
		var row RiskReview
		if err := tx.First(&row, id).Error; err != nil {
			return err
		}
		var history []riskReviewHistory
		if err := common.UnmarshalJsonStr(row.History, &history); err != nil {
			return err
		}
		if row.Revision != expectedRevision {
			if len(history) == 0 {
				return ErrRiskRevisionConflict
			}
			last := history[len(history)-1]
			if row.Revision != expectedRevision+1 || last.Action != action || last.OperatorId != operatorID || last.Category != category || last.Note != note || last.BlockTTLMinutes != max(manualTTL, 0) {
				return ErrRiskRevisionConflict
			}
			result.Review = &row
			result.Score, _, err = riskBehaviorScore(tx, row.Namespace, row.UserId, row.TokenId, policy, now)
			return err
		}
		if modelConfirm {
			// A replay can finish before the original model call and create the
			// pending row first. Adopt the trusted call's judgment only when the
			// policy and complete evidence identify exactly the same incident.
			if row.Namespace != judgment.Namespace || row.UserId != judgment.UserId || row.TokenId != judgment.TokenId ||
				row.Layer != "model" || judgment.Layer != "model" || row.Mode != judgment.Mode ||
				row.IncidentKey != judgment.IncidentKey || (row.IncidentKey == "" && row.RequestId != judgment.RequestId) ||
				row.PolicyKey != judgment.PolicyKey || row.PolicyRevision != judgment.PolicyRevision || row.ConfigRevision != judgment.ConfigRevision ||
				row.ProviderModel != judgment.ProviderModel || row.OriginalRequest != judgment.OriginalRequest || row.Evidence != judgment.Evidence ||
				!riskModelReviewComplete(judgment) {
				return ErrRiskReviewTransition
			}
			row.Scores, row.Coverage = judgment.Scores, judgment.Coverage
			row.ScreeningScore, row.ScreeningReason = judgment.ScreeningScore, judgment.ScreeningReason
		}
		switch action {
		case "confirm", "ban":
			if category == "" || (action == "confirm" && row.Status != RiskReviewPending) || (action == "ban" && (row.TokenId <= 0 || (row.Status != RiskReviewPending && row.Status != RiskReviewConfirmed))) {
				return ErrRiskReviewTransition
			}
			if row.TokenId <= 0 || !riskReviewEvidenceComplete(&row) || (action == "ban" && row.Mode != "enforce") {
				return ErrRiskReviewTransition
			}
			if modelConfirm && !riskModelReviewComplete(&row) {
				return ErrRiskReviewTransition
			}
			row.Status, row.Category, row.Points = RiskReviewConfirmed, category, 0
			row.JudgmentSource = "manual"
			if modelConfirm {
				row.JudgmentSource = "model"
			}
			if row.Mode == "enforce" && row.NotificationStatus == "" {
				row.NotificationStatus = "pending"
			}
			if row.Mode == "enforce" {
				row.Points = 1
				if category == "nsfw" || category == "jailbreak" {
					row.Points = 3
				}
			}
		case "dismiss":
			if row.Status != RiskReviewPending {
				return ErrRiskReviewTransition
			}
			row.Status, row.Points = RiskReviewDismissed, 0
		case "revoke":
			if row.Status != RiskReviewConfirmed {
				return ErrRiskReviewTransition
			}
			row.Status, row.Points = RiskReviewRevoked, 0
		default:
			return ErrRiskReviewTransition
		}
		if (action == "dismiss" || action == "revoke") && (row.NotificationStatus == "pending" || row.NotificationStatus == "sending") {
			row.NotificationStatus, row.NotificationAt, row.NotificationError = "skipped", now, "review_withdrawn"
		}
		row.Revision++
		row.ReviewedAt, row.ReviewedBy, row.ReviewNote = now, operatorID, note
		history = append(history, riskReviewHistory{Action: action, At: now, OperatorId: operatorID, Category: category, Note: note, Revision: row.Revision, Source: row.JudgmentSource, BlockTTLMinutes: max(manualTTL, 0)})
		encoded, err := common.Marshal(history)
		if err != nil {
			return err
		}
		row.History = string(encoded)
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if action == "dismiss" || action == "revoke" {
			row.Evidence, row.OriginalRequest = "", ""
			if err := tx.Model(&row).Updates(map[string]any{"evidence": "", "original_request": ""}).Error; err != nil {
				return err
			}
			var aliases []RiskReview
			if err := tx.Select("request_id", "fingerprint").Where("duplicate_of = ?", row.Id).Find(&aliases).Error; err != nil {
				return err
			}
			requests, fingerprints := []string{row.RequestId}, []string{}
			if row.Fingerprint != "" {
				fingerprints = append(fingerprints, row.Fingerprint)
			}
			for _, alias := range aliases {
				requests = append(requests, alias.RequestId)
				if alias.Fingerprint != "" {
					fingerprints = append(fingerprints, alias.Fingerprint)
				}
			}
			if err := tx.Model(&RiskEvent{}).Where("namespace = ? AND user_id = ? AND request_id IN ?", row.Namespace, row.UserId, requests).Update("preview", "").Error; err != nil {
				return err
			}
			refusals := tx.Where("namespace = ? AND user_id = ?", row.Namespace, row.UserId)
			if len(fingerprints) > 0 {
				refusals = refusals.Where("fingerprint IN ? OR source_request_id IN ?", fingerprints, requests)
			} else {
				refusals = refusals.Where("source_request_id IN ?", requests)
			}
			if err := refusals.Delete(&RiskRefusal{}).Error; err != nil {
				return err
			}
			var block RiskSubjectBlock
			err := tx.Where("namespace = ? AND subject_type = ? AND subject_value = ? AND released_at = 0", row.Namespace, "token", strconv.Itoa(row.TokenId)).First(&block).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil && (block.Reason == "behavior_score" || block.Reason == "manual_review") {
				triggerID, parseErr := strconv.Atoi(strings.TrimPrefix(block.SourceRequestId, "risk-review:"))
				if parseErr != nil {
					return parseErr
				}
				var trigger RiskReview
				if err := tx.First(&trigger, triggerID).Error; err != nil {
					return err
				}
				var ids []int
				if err := common.UnmarshalJsonStr(trigger.BlockReviewIds, &ids); err != nil {
					return err
				}
				for _, reviewID := range ids {
					if reviewID == row.Id {
						if err := tx.Model(&RiskSubjectBlock{}).Where("id = ?", block.Id).Updates(map[string]any{"released_at": now, "released_by": operatorID}).Error; err != nil {
							return err
						}
						break
					}
				}
			}
		}
		score, contributing, err := riskBehaviorScore(tx, row.Namespace, row.UserId, row.TokenId, policy, now)
		if err != nil {
			return err
		}
		result.Review, result.Score = &row, score
		if row.TokenId > 0 && riskReviewEvidenceComplete(&row) && (action == "ban" || (action == "confirm" && row.Mode == "enforce" && allowAutoBlock && row.CreatedAt > now-int64(policy.WindowMinutes)*60 && score.Score >= policy.Threshold)) {
			if action == "confirm" {
				var active RiskSubjectBlock
				err := tx.Where("user_id = ? AND subject_type = ? AND subject_value = ? AND released_at = 0", row.UserId, "token", strconv.Itoa(row.TokenId)).Where("expires_at = 0 OR expires_at > ?", now).First(&active).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err == nil {
					result.Block = &active
					if modelConfirm {
						return nil
					}
					return bumpRiskState(tx, state, false)
				}
			}
			var namespace RiskGroupNamespace
			err := tx.Where("namespace = ? AND active = ?", row.Namespace, true).First(&namespace).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil || action == "ban" {
				var block RiskSubjectBlock
				err := tx.Where("namespace = ? AND subject_type = ? AND subject_value = ?", row.Namespace, "token", strconv.Itoa(row.TokenId)).First(&block).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				active := err == nil && block.ReleasedAt == 0 && (block.ExpiresAt == 0 || block.ExpiresAt > now)
				if !active || (action == "ban" && (manualTTL == 0 || block.ExpiresAt != 0)) {
					block.Namespace, block.SubjectType, block.SubjectValue, block.UserId = row.Namespace, "token", strconv.Itoa(row.TokenId), row.UserId
					block.Reason, block.SourceRequestId, block.Fingerprint = "behavior_score", fmt.Sprintf("risk-review:%d", row.Id), row.Fingerprint
					block.ReleasedAt, block.ReleasedBy, block.LastSeenAt, block.ExpiresAt = 0, 0, now, now+int64(policy.BlockTTLMinutes)*60
					if action == "ban" {
						block.Reason = "manual_review"
						if manualTTL == 0 {
							block.ExpiresAt = 0
						} else {
							block.ExpiresAt = now + int64(manualTTL)*60
						}
						contributing = []RiskReview{{Id: row.Id}}
					}
					if block.Id == 0 {
						block.FirstSeenAt = now
					}
					if err := tx.Save(&block).Error; err != nil {
						return err
					}
					ids := make([]int, 0, len(contributing))
					for _, contributor := range contributing {
						ids = append(ids, contributor.Id)
					}
					encoded, err := common.Marshal(ids)
					if err != nil {
						return err
					}
					row.BlockReviewIds = string(encoded)
					if err := tx.Model(&row).Update("block_review_ids", row.BlockReviewIds).Error; err != nil {
						return err
					}
				}
				result.Block = &block
			}
		}
		if modelConfirm {
			return nil
		}
		return bumpRiskState(tx, state, false)
	})
	return &result, err
}

// cleanupRiskReviews applies the event retention policy to evidence and its
// aliases together. Keep review evidence while a derived block is still live,
// so the administrator can revoke its contributing decisions consistently.
func cleanupRiskReviews(now, before int64) (int64, error) {
	var total int64
	lastID := 0
	for {
		var nextID, batchLength int
		var removed int64
		err := riskTransaction(func(tx *gorm.DB) error {
			removed = 0
			if _, err := lockRiskState(tx); err != nil {
				return err
			}
			var rows []RiskReview
			if err := tx.Select("id", "namespace", "user_id", "token_id").Where("duplicate_of = 0 AND created_at < ? AND id > ?", before, lastID).Order("id asc").Limit(1000).Find(&rows).Error; err != nil {
				return err
			}
			batchLength = len(rows)
			if batchLength == 0 {
				return nil
			}
			namespaces := make([]string, 0, len(rows))
			seen := make(map[string]bool, len(rows))
			for _, row := range rows {
				if !seen[row.Namespace] {
					namespaces = append(namespaces, row.Namespace)
					seen[row.Namespace] = true
				}
			}
			var blocks []RiskSubjectBlock
			if err := tx.Select("namespace", "subject_value").Where("namespace IN ? AND subject_type = ? AND reason IN ? AND released_at = 0", namespaces, "token", []string{"behavior_score", "manual_review"}).Where("expires_at = 0 OR expires_at > ?", now).Find(&blocks).Error; err != nil {
				return err
			}
			live := make(map[string]bool, len(blocks))
			for _, block := range blocks {
				live[block.Namespace+"\x00"+block.SubjectValue] = true
			}
			ids := make([]int, 0, len(rows))
			for _, row := range rows {
				nextID = row.Id
				if !live[row.Namespace+"\x00"+strconv.Itoa(row.TokenId)] {
					ids = append(ids, row.Id)
				}
			}
			if len(ids) == 0 {
				return nil
			}
			result := tx.Where("id IN ? OR duplicate_of IN ?", ids, ids).Delete(&RiskReview{})
			removed = result.RowsAffected
			return result.Error
		})
		if err != nil {
			return total, err
		}
		total += removed
		if batchLength < 1000 {
			return total, nil
		}
		lastID = nextID
	}
}
