package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// RiskSignal stores only screening metadata. A signal is not a confirmed
// violation, and consuming it never adds punishment points.
type RiskSignal struct {
	Id             int    `json:"id"`
	RequestKey     string `json:"-" gorm:"type:varchar(64);uniqueIndex:idx_risk_signal_request"`
	DuplicateOf    int    `json:"-"`
	Namespace      string `json:"namespace" gorm:"type:varchar(32);index:idx_risk_signal_scope,priority:1"`
	UserId         int    `json:"user_id" gorm:"index:idx_risk_signal_scope,priority:2"`
	TokenId        int    `json:"token_id" gorm:"index:idx_risk_signal_scope,priority:3"`
	RequestId      string `json:"request_id" gorm:"type:varchar(64)"`
	IncidentKey    string `json:"incident_key" gorm:"type:varchar(64)"`
	PolicyKey      string `json:"policy_key" gorm:"type:varchar(64)"`
	PolicyRevision int    `json:"policy_revision"`
	Points         int    `json:"points"`
	Reason         string `json:"reason" gorm:"type:varchar(64)"`
	Category       string `json:"category" gorm:"type:varchar(32)"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;index:idx_risk_signal_scope,priority:4"`
	ConsumedAt     int64  `json:"consumed_at" gorm:"bigint"`
	AuditTriggered bool   `json:"audit_triggered"`
}

type RiskScreeningPolicy struct {
	Threshold     int `json:"threshold"`
	WindowMinutes int `json:"window_minutes"`
}

type RiskScreeningResult struct {
	Score             int  `json:"score"`
	DistinctSignals   int  `json:"distinct_signals"`
	ShouldAudit       bool `json:"should_audit"`
	PreviouslyAudited bool `json:"previously_audited"`
}

func RecordRiskSignal(input *RiskSignal, policy RiskScreeningPolicy, now int64) (RiskScreeningResult, error) {
	if input != nil && input.Points == 0 {
		return PreviewRiskSignal(input, policy, now)
	}
	var result RiskScreeningResult
	err := riskTransaction(func(tx *gorm.DB) error {
		if _, err := lockRiskState(tx); err != nil {
			return err
		}
		var err error
		result, err = riskScreening(tx, input, policy, now, true)
		return err
	})
	return result, err
}

func PreviewRiskSignal(input *RiskSignal, policy RiskScreeningPolicy, now int64) (RiskScreeningResult, error) {
	return riskScreening(DB, input, policy, now, false)
}

// riskScreening shares the exact threshold and duplicate decisions between
// the read-only preview and the serialized production transaction.
func riskScreening(tx *gorm.DB, input *RiskSignal, policy RiskScreeningPolicy, now int64, write bool) (RiskScreeningResult, error) {
	result := RiskScreeningResult{}
	if input == nil || input.Namespace == "" || input.UserId <= 0 || input.TokenId <= 0 || input.Points < 0 || input.Points > 100 || (input.Points > 0 && (input.RequestId == "" || input.IncidentKey == "")) {
		return result, ErrRiskReviewTransition
	}
	if policy.Threshold <= 0 {
		policy.Threshold = 3
	}
	if policy.WindowMinutes <= 0 {
		policy.WindowMinutes = 10
	}
	since := now - int64(policy.WindowMinutes)*60
	scope := func() *gorm.DB {
		return tx.Where("namespace = ? AND user_id = ? AND token_id = ? AND policy_key = ? AND policy_revision = ?", input.Namespace, input.UserId, input.TokenId, input.PolicyKey, input.PolicyRevision)
	}
	var signals []RiskSignal
	if err := scope().Select("id", "points").Where("duplicate_of = 0 AND consumed_at = 0 AND created_at > ? AND created_at <= ?", since, now).Find(&signals).Error; err != nil {
		return result, err
	}
	for _, signal := range signals {
		result.Score += signal.Points
	}
	result.DistinctSignals = len(signals)
	if input.Points == 0 {
		return result, nil
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%d\x00%s", input.Namespace, input.UserId, input.TokenId, input.PolicyKey, input.PolicyRevision, input.RequestId)))
	key := hex.EncodeToString(hash[:])
	var existing RiskSignal
	err := tx.Select("id", "duplicate_of", "created_at", "audit_triggered").Where("request_key = ?", key).First(&existing).Error
	if err == nil {
		if existing.DuplicateOf > 0 {
			id := existing.DuplicateOf
			existing = RiskSignal{}
			if err := tx.Select("id", "created_at", "audit_triggered").First(&existing, id).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return result, err
			}
		}
		result.PreviouslyAudited = existing.AuditTriggered && existing.CreatedAt > since
		result.ShouldAudit = result.PreviouslyAudited
		return result, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	err = scope().Select("id", "audit_triggered").Where("duplicate_of = 0 AND incident_key = ? AND created_at > ? AND created_at <= ?", input.IncidentKey, since, now).Order("id desc").First(&existing).Error
	if err == nil {
		result.PreviouslyAudited, result.ShouldAudit = existing.AuditTriggered, existing.AuditTriggered
		if write {
			alias := RiskSignal{RequestKey: key, Namespace: input.Namespace, UserId: input.UserId, TokenId: input.TokenId, RequestId: input.RequestId, IncidentKey: input.IncidentKey, PolicyKey: input.PolicyKey, PolicyRevision: input.PolicyRevision, CreatedAt: now, DuplicateOf: existing.Id, ConsumedAt: now}
			if err := tx.Create(&alias).Error; err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	row := *input
	row.Id, row.DuplicateOf, row.CreatedAt, row.ConsumedAt, row.RequestKey = 0, 0, now, 0, key
	result.Score += row.Points
	result.DistinctSignals++
	result.ShouldAudit = result.Score >= policy.Threshold
	row.AuditTriggered = result.ShouldAudit
	if !write {
		return result, nil
	}
	if result.ShouldAudit {
		row.ConsumedAt = now
	}
	if err := tx.Create(&row).Error; err != nil {
		return result, err
	}
	if result.ShouldAudit && len(signals) > 0 {
		ids := make([]int, 0, len(signals))
		for _, signal := range signals {
			ids = append(ids, signal.Id)
		}
		if err := tx.Model(&RiskSignal{}).Where("id IN ?", ids).Update("consumed_at", now).Error; err != nil {
			return result, err
		}
	}
	return result, nil
}
