package model

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	sqlitedriver "github.com/glebarez/go-sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Risk audit persistence. All tables live in the main database and use only
// portable GORM queries so SQLite, MySQL and PostgreSQL behave the same.

const riskStateGlobal = "global"

// ErrRiskRevisionConflict reports that another administrator published first.
var ErrRiskRevisionConflict = errors.New("risk configuration revision conflict")

// RiskState is the single control row that serializes risk writes and carries
// the monotonic revisions every node polls.
type RiskState struct {
	Id                int    `json:"id"`
	StateKey          string `json:"state_key" gorm:"type:varchar(16);uniqueIndex:idx_risk_state_key"`
	ChangeRevision    int64  `json:"change_revision" gorm:"bigint"`
	DirectoryRevision int64  `json:"directory_revision" gorm:"bigint"`
	UpdatedAt         int64  `json:"updated_at" gorm:"bigint"`
}

type RiskConfig struct {
	Id        int    `json:"id"`
	Revision  int64  `json:"revision" gorm:"bigint;uniqueIndex:idx_risk_config_revision"`
	Payload   string `json:"payload" gorm:"type:text"`
	CreatedBy int    `json:"created_by"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

type RiskPolicy struct {
	Id          int    `json:"id"`
	PolicyKey   string `json:"policy_key" gorm:"type:varchar(64);uniqueIndex:idx_risk_policy_rev,priority:1"`
	Revision    int    `json:"revision" gorm:"uniqueIndex:idx_risk_policy_rev,priority:2"`
	Name        string `json:"name" gorm:"type:varchar(128)"`
	Payload     string `json:"payload" gorm:"type:text"`
	Fingerprint string `json:"fingerprint" gorm:"type:varchar(64)"`
	CreatedBy   int    `json:"created_by"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint"`
}

type RiskProvider struct {
	Id           int    `json:"id"`
	Name         string `json:"name" gorm:"type:varchar(64);uniqueIndex:idx_risk_provider_name"`
	ProviderType string `json:"provider_type" gorm:"type:varchar(16)"`
	BaseURL      string `json:"base_url" gorm:"type:varchar(300)"`
	Model        string `json:"model" gorm:"type:varchar(120)"`
	Proxy        string `json:"proxy" gorm:"type:varchar(255)"`
	ApiKeys      string `json:"-" gorm:"type:text"`
	KeysRevision int    `json:"keys_revision"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt    int64  `json:"updated_at" gorm:"bigint"`
}

// KeyLines splits the stored keys into one entry per line.
func (p *RiskProvider) KeyLines() []string {
	lines := make([]string, 0)
	for line := range strings.SplitSeq(p.ApiKeys, "\n") {
		if text := strings.TrimSpace(line); text != "" {
			lines = append(lines, text)
		}
	}
	return lines
}

type RiskGroupNamespace struct {
	Id         int    `json:"id"`
	GroupName  string `json:"group_name" gorm:"type:varchar(128);uniqueIndex:idx_risk_ns_gen,priority:1;index:idx_risk_ns_active,priority:1"`
	Namespace  string `json:"namespace" gorm:"type:varchar(32);uniqueIndex:idx_risk_ns_value"`
	Generation int    `json:"generation" gorm:"uniqueIndex:idx_risk_ns_gen,priority:2"`
	Active     bool   `json:"active" gorm:"index:idx_risk_ns_active,priority:2"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint"`
	RetiredAt  int64  `json:"retired_at" gorm:"bigint"`
}

type RiskEvent struct {
	Id             int     `json:"id"`
	RequestId      string  `json:"request_id" gorm:"type:varchar(64);index:idx_risk_evt_request"`
	Attempt        int     `json:"attempt"`
	CreatedAt      int64   `json:"created_at" gorm:"bigint;index:idx_risk_evt_time;index:idx_risk_evt_ns_time,priority:2;index:idx_risk_evt_user_time,priority:2"`
	UserId         int     `json:"user_id" gorm:"index:idx_risk_evt_user_time,priority:1"`
	TokenId        int     `json:"token_id"`
	UserGroup      string  `json:"user_group" gorm:"type:varchar(64)"`
	TokenGroup     string  `json:"token_group" gorm:"type:varchar(64)"`
	EffectiveGroup string  `json:"effective_group" gorm:"type:varchar(64)"`
	Namespace      string  `json:"namespace" gorm:"type:varchar(32);index:idx_risk_evt_ns_time,priority:1"`
	GroupSource    string  `json:"group_source" gorm:"type:varchar(24)"`
	ChannelId      int     `json:"channel_id"`
	Model          string  `json:"model" gorm:"type:varchar(128)"`
	Route          string  `json:"route" gorm:"type:varchar(32)"`
	Mode           string  `json:"mode" gorm:"type:varchar(12)"`
	ConfigRevision int64   `json:"config_revision" gorm:"bigint"`
	PolicyKey      string  `json:"policy_key" gorm:"type:varchar(64)"`
	PolicyRevision int     `json:"policy_revision"`
	Layer          string  `json:"layer" gorm:"type:varchar(16)"`
	Outcome        string  `json:"outcome" gorm:"type:varchar(16)"`
	Reason         string  `json:"reason" gorm:"type:varchar(64)"`
	Category       string  `json:"category" gorm:"type:varchar(32)"`
	Keyword        string  `json:"keyword" gorm:"type:varchar(64)"`
	Rule           string  `json:"rule" gorm:"type:varchar(160)"`
	Scores         string  `json:"scores" gorm:"type:text"`
	ProviderModel  string  `json:"provider_model" gorm:"type:varchar(120)"`
	LatencyMs      int     `json:"latency_ms"`
	Coverage       string  `json:"coverage" gorm:"type:varchar(16)"`
	ErrorCode      string  `json:"error_code" gorm:"type:varchar(48)"`
	Preview        string  `json:"preview" gorm:"type:varchar(240)"`
	Fingerprint    string  `json:"fingerprint" gorm:"type:varchar(64)"`
	DeviceHash     string  `json:"device_hash" gorm:"type:varchar(64)"`
	Similarity     float64 `json:"similarity"`
}

type RiskCounter struct {
	Id        int    `json:"id"`
	Bucket    int64  `json:"bucket" gorm:"bigint;uniqueIndex:idx_risk_counter_key,priority:1"`
	Namespace string `json:"namespace" gorm:"type:varchar(32);uniqueIndex:idx_risk_counter_key,priority:2"`
	GroupName string `json:"group_name" gorm:"type:varchar(128);uniqueIndex:idx_risk_counter_key,priority:3"`
	Route     string `json:"route" gorm:"type:varchar(32);uniqueIndex:idx_risk_counter_key,priority:4"`
	Outcome   string `json:"outcome" gorm:"type:varchar(16);uniqueIndex:idx_risk_counter_key,priority:5"`
	Layer     string `json:"layer" gorm:"type:varchar(16);uniqueIndex:idx_risk_counter_key,priority:6"`
	Reason    string `json:"reason" gorm:"type:varchar(64);uniqueIndex:idx_risk_counter_key,priority:7"`
	IsFirst   int    `json:"is_first" gorm:"uniqueIndex:idx_risk_counter_key,priority:8"`
	Hits      int64  `json:"hits" gorm:"bigint"`
}

type RiskRefusal struct {
	Id                 int    `json:"id"`
	Namespace          string `json:"namespace" gorm:"type:varchar(32);uniqueIndex:idx_risk_refusal_key,priority:1;index:idx_risk_refusal_cand,priority:1"`
	UserId             int    `json:"user_id" gorm:"uniqueIndex:idx_risk_refusal_key,priority:2;index:idx_risk_refusal_cand,priority:2"`
	Model              string `json:"model" gorm:"type:varchar(128);index:idx_risk_refusal_cand,priority:3"`
	Fingerprint        string `json:"fingerprint" gorm:"type:varchar(64);uniqueIndex:idx_risk_refusal_key,priority:3"`
	FingerprintVersion int    `json:"fingerprint_version"`
	Signature          string `json:"-" gorm:"type:text"`
	SignatureVersion   int    `json:"signature_version"`
	Preview            string `json:"preview" gorm:"type:varchar(240)"`
	ErrorMessage       string `json:"error_message" gorm:"type:varchar(500)"`
	SourceRequestId    string `json:"source_request_id" gorm:"type:varchar(64)"`
	PolicyKey          string `json:"policy_key" gorm:"type:varchar(64)"`
	PolicyRevision     int    `json:"policy_revision"`
	HitCount           int    `json:"hit_count"`
	FirstSeenAt        int64  `json:"first_seen_at" gorm:"bigint"`
	LastSeenAt         int64  `json:"last_seen_at" gorm:"bigint;index:idx_risk_refusal_cand,priority:4"`
	ExpiresAt          int64  `json:"expires_at" gorm:"bigint"`
}

type RiskSubjectBlock struct {
	Id              int    `json:"id"`
	Namespace       string `json:"namespace" gorm:"type:varchar(32);uniqueIndex:idx_risk_block_key,priority:1"`
	SubjectType     string `json:"subject_type" gorm:"type:varchar(12);uniqueIndex:idx_risk_block_key,priority:2"`
	SubjectValue    string `json:"subject_value" gorm:"type:varchar(80);uniqueIndex:idx_risk_block_key,priority:3"`
	Reason          string `json:"reason" gorm:"type:varchar(80)"`
	Fingerprint     string `json:"fingerprint" gorm:"type:varchar(64)"`
	SourceRequestId string `json:"source_request_id" gorm:"type:varchar(64)"`
	HitCount        int    `json:"hit_count"`
	FirstSeenAt     int64  `json:"first_seen_at" gorm:"bigint"`
	LastSeenAt      int64  `json:"last_seen_at" gorm:"bigint"`
	ExpiresAt       int64  `json:"expires_at" gorm:"bigint"`
	ReleasedAt      int64  `json:"released_at" gorm:"bigint"`
	ReleasedBy      int    `json:"released_by"`
}

// RiskModels lists every risk table for AutoMigrate.
func RiskModels() []any {
	return []any{
		&RiskState{}, &RiskConfig{}, &RiskPolicy{}, &RiskProvider{}, &RiskGroupNamespace{},
		&RiskEvent{}, &RiskCounter{}, &RiskRefusal{}, &RiskSubjectBlock{}, &RiskReview{},
	}
}

func isRiskUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == 1555 || sqliteErr.Code() == 2067 // PRIMARYKEY, UNIQUE
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	var postgresErr *pgconn.PgError
	return errors.As(err, &postgresErr) && postgresErr.Code == "23505"
}

func isRiskRetryable(err error) bool {
	if isRiskUniqueViolation(err) {
		return true
	}
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == 5 || sqliteErr.Code()&0xff == 6 // BUSY, LOCKED
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1205 || mysqlErr.Number == 1213
	}
	var postgresErr *pgconn.PgError
	return errors.As(err, &postgresErr) && (postgresErr.Code == "40001" || postgresErr.Code == "40P01")
}

// riskTransaction reruns the whole transaction on a uniqueness or lock
// conflict; a failed PostgreSQL transaction is never continued.
func riskTransaction(fn func(tx *gorm.DB) error) error {
	var err error
	for range 4 {
		err = DB.Transaction(fn)
		if err == nil || errors.Is(err, ErrRiskRevisionConflict) || !isRiskRetryable(err) {
			return err
		}
	}
	return err
}

// lockRiskState locks the control row, creating it on first use.
func lockRiskState(tx *gorm.DB) (*RiskState, error) {
	var state RiskState
	err := lockForUpdate(tx).Where("state_key = ?", riskStateGlobal).First(&state).Error
	if err == nil {
		return &state, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	state = RiskState{StateKey: riskStateGlobal, UpdatedAt: common.GetTimestamp()}
	if err := tx.Create(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

func bumpRiskState(tx *gorm.DB, state *RiskState, directory bool) error {
	updates := map[string]any{
		"change_revision": state.ChangeRevision + 1,
		"updated_at":      common.GetTimestamp(),
	}
	if directory {
		updates["directory_revision"] = state.DirectoryRevision + 1
	}
	return tx.Model(&RiskState{}).Where("id = ?", state.Id).Updates(updates).Error
}

// GetRiskChangeRevision returns the revision nodes poll to detect changes.
func GetRiskChangeRevision() (int64, error) {
	var state RiskState
	err := DB.Where("state_key = ?", riskStateGlobal).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return state.ChangeRevision, err
}

func newRiskNamespace() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ReconcileRiskGroupNamespaces makes the active namespaces match the given
// group names: new groups get a fresh namespace, vanished groups are retired.
// A group re-created after deletion gets a new generation and namespace.
func ReconcileRiskGroupNamespaces(groupNames []string) error {
	return riskTransaction(func(tx *gorm.DB) error {
		state, err := lockRiskState(tx)
		if err != nil {
			return err
		}
		return reconcileRiskGroupNamespaces(tx, state, groupNames)
	})
}

// reconcileRiskGroupNamespaces runs with the control row already locked, so
// callers can read their persisted directory in the same transaction.
func reconcileRiskGroupNamespaces(tx *gorm.DB, state *RiskState, groupNames []string) error {
	wanted := make([]string, 0, len(groupNames))
	for _, name := range groupNames {
		if name != "" && name != "auto" && !slices.Contains(wanted, name) {
			wanted = append(wanted, name)
		}
	}
	var active []RiskGroupNamespace
	if err := tx.Where("active = ?", true).Find(&active).Error; err != nil {
		return err
	}
	now := common.GetTimestamp()
	changed := false
	have := make(map[string]bool, len(active))
	for _, row := range active {
		have[row.GroupName] = true
		if slices.Contains(wanted, row.GroupName) {
			continue
		}
		if err := tx.Model(&RiskGroupNamespace{}).Where("id = ?", row.Id).
			Updates(map[string]any{"active": false, "retired_at": now}).Error; err != nil {
			return err
		}
		changed = true
	}
	for _, name := range wanted {
		if have[name] {
			continue
		}
		var generation int
		if err := tx.Model(&RiskGroupNamespace{}).Where("group_name = ?", name).
			Select("COALESCE(MAX(generation), 0)").Scan(&generation).Error; err != nil {
			return err
		}
		namespace, err := newRiskNamespace()
		if err != nil {
			return err
		}
		row := RiskGroupNamespace{GroupName: name, Namespace: namespace, Generation: generation + 1, Active: true, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	return bumpRiskState(tx, state, true)
}

// RiskGroupRatioOptionKeys are the option keys that write the group directory.
var RiskGroupRatioOptionKeys = []string{"GroupRatio", "group_ratio_setting.group_ratio"}

// ReconcileRiskGroupsFromOption re-reads a just-saved group ratio option from
// the database so concurrent saves on different nodes cannot be reconciled
// from a stale in-memory directory.
func ReconcileRiskGroupsFromOption(key string) error {
	return riskTransaction(func(tx *gorm.DB) error {
		state, err := lockRiskState(tx)
		if err != nil {
			return err
		}
		var option Option
		if err := tx.Where(&Option{Key: key}).First(&option).Error; err != nil {
			return err
		}
		var ratios map[string]float64
		if err := common.UnmarshalJsonStr(option.Value, &ratios); err != nil {
			return err
		}
		names := make([]string, 0, len(ratios))
		for name := range ratios {
			names = append(names, name)
		}
		return reconcileRiskGroupNamespaces(tx, state, names)
	})
}

func reconcileRiskGroupsAfterSave(key string) {
	if !slices.Contains(RiskGroupRatioOptionKeys, key) {
		return
	}
	if err := ReconcileRiskGroupsFromOption(key); err != nil {
		common.SysError(common.LogText("risk group namespace reconcile failed: %s", err.Error()))
	}
}

func ListRiskGroupNamespaces(activeOnly bool) ([]RiskGroupNamespace, error) {
	var rows []RiskGroupNamespace
	query := DB.Order("group_name asc").Order("generation desc")
	if activeOnly {
		query = query.Where("active = ?", true)
	}
	return rows, query.Find(&rows).Error
}

// GetLatestRiskConfig returns nil when no configuration was ever published.
func GetLatestRiskConfig() (*RiskConfig, error) {
	var cfg RiskConfig
	err := DB.Order("revision desc").First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PublishRiskConfig inserts revision expected+1 unless someone else already
// published past expectedRevision.
func PublishRiskConfig(expectedRevision int64, payload string, operatorID int) (int64, error) {
	var published int64
	err := riskTransaction(func(tx *gorm.DB) error {
		state, err := lockRiskState(tx)
		if err != nil {
			return err
		}
		var current int64
		if err := tx.Model(&RiskConfig{}).Select("COALESCE(MAX(revision), 0)").Scan(&current).Error; err != nil {
			return err
		}
		if current != expectedRevision {
			return ErrRiskRevisionConflict
		}
		row := RiskConfig{Revision: current + 1, Payload: payload, CreatedBy: operatorID, CreatedAt: common.GetTimestamp()}
		if err := tx.Create(&row).Error; err != nil {
			if isRiskUniqueViolation(err) {
				return ErrRiskRevisionConflict
			}
			return err
		}
		published = row.Revision
		return bumpRiskState(tx, state, false)
	})
	return published, err
}

// CreateRiskPolicyRevision appends a revision without changing its bindings.
func CreateRiskPolicyRevision(key, name string, baseRevision int, payload, fingerprint string, operatorID int) (*RiskPolicy, error) {
	created, _, err := SaveRiskPolicyRevision(key, name, baseRevision, payload, fingerprint, operatorID, nil)
	return created, err
}

// RiskPolicyActivation builds the new configuration from the configuration
// read under the control-row lock. The callback must not access the database.
type RiskPolicyActivation struct {
	ExpectedConfigRevision int64
	BuildConfig            func(current *RiskConfig, policy *RiskPolicy) (string, error)
}

// SaveRiskPolicyRevision optionally publishes the policy and its bindings
// atomically. baseRevision must equal the latest stored revision.
func SaveRiskPolicyRevision(key, name string, baseRevision int, payload, fingerprint string, operatorID int, activation *RiskPolicyActivation) (*RiskPolicy, *RiskConfig, error) {
	var created RiskPolicy
	var published *RiskConfig
	err := riskTransaction(func(tx *gorm.DB) error {
		published = nil
		state, err := lockRiskState(tx)
		if err != nil {
			return err
		}
		var latest RiskPolicy
		err = tx.Where("policy_key = ?", key).Order("revision desc").First(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if latest.Revision != baseRevision {
			return ErrRiskRevisionConflict
		}
		var current RiskConfig
		if activation != nil {
			err := tx.Order("revision desc").First(&current).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if current.Revision != activation.ExpectedConfigRevision {
				return ErrRiskRevisionConflict
			}
		}
		changed := false
		if activation != nil && latest.Id > 0 && latest.Fingerprint == fingerprint && latest.Name == name {
			created = latest
		} else {
			created = RiskPolicy{PolicyKey: key, Revision: latest.Revision + 1, Name: name, Payload: payload, Fingerprint: fingerprint, CreatedBy: operatorID, CreatedAt: common.GetTimestamp()}
			if err := tx.Create(&created).Error; err != nil {
				if isRiskUniqueViolation(err) {
					return ErrRiskRevisionConflict
				}
				return err
			}
			changed = true
		}
		if activation != nil {
			configPayload, err := activation.BuildConfig(&current, &created)
			if err != nil {
				return err
			}
			published = &current
			if current.Id == 0 || configPayload != current.Payload {
				published = &RiskConfig{Revision: current.Revision + 1, Payload: configPayload, CreatedBy: operatorID, CreatedAt: common.GetTimestamp()}
				if err := tx.Create(published).Error; err != nil {
					return err
				}
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return bumpRiskState(tx, state, false)
	})
	if err != nil {
		return nil, nil, err
	}
	return &created, published, nil
}

// EnsureRiskBaselinePolicy seeds the baseline policy once.
func EnsureRiskBaselinePolicy(key, name, payload, fingerprint string) error {
	var count int64
	if err := DB.Model(&RiskPolicy{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := CreateRiskPolicyRevision(key, name, 0, payload, fingerprint, 0)
	if errors.Is(err, ErrRiskRevisionConflict) {
		return nil
	}
	return err
}

func GetRiskPolicy(key string, revision int) (*RiskPolicy, error) {
	var policy RiskPolicy
	if err := DB.Where("policy_key = ? AND revision = ?", key, revision).First(&policy).Error; err != nil {
		return nil, err
	}
	return &policy, nil
}

// RiskPolicySummary is the latest revision of one policy key.
type RiskPolicySummary struct {
	RiskPolicy
	RevisionCount int64 `json:"revision_count"`
}

func ListRiskPolicies() ([]RiskPolicySummary, error) {
	var keys []string
	if err := DB.Model(&RiskPolicy{}).Distinct("policy_key").Order("policy_key asc").Pluck("policy_key", &keys).Error; err != nil {
		return nil, err
	}
	out := make([]RiskPolicySummary, 0, len(keys))
	for _, key := range keys {
		var latest RiskPolicy
		if err := DB.Where("policy_key = ?", key).Order("revision desc").First(&latest).Error; err != nil {
			return nil, err
		}
		out = append(out, RiskPolicySummary{RiskPolicy: latest, RevisionCount: int64(latest.Revision)})
	}
	return out, nil
}

func ListRiskPolicyRevisions(key string) ([]RiskPolicy, error) {
	var rows []RiskPolicy
	return rows, DB.Where("policy_key = ?", key).Order("revision desc").Find(&rows).Error
}

func ListRiskProviders() ([]RiskProvider, error) {
	var rows []RiskProvider
	return rows, DB.Order("id asc").Find(&rows).Error
}

func GetRiskProvider(id int) (*RiskProvider, error) {
	var row RiskProvider
	if err := DB.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// SaveRiskProvider creates or updates a provider. keys is nil to keep the
// stored keys; any non-nil value (including empty) replaces them.
func SaveRiskProvider(provider *RiskProvider, keys []string) error {
	input := *provider
	var saved RiskProvider
	err := riskTransaction(func(tx *gorm.DB) error {
		row := input
		state, err := lockRiskState(tx)
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		row.UpdatedAt = now
		if keys != nil {
			row.ApiKeys = strings.Join(keys, "\n")
		}
		if row.Id == 0 {
			row.CreatedAt = now
			row.KeysRevision = 1
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			saved = row
			return bumpRiskState(tx, state, false)
		}
		var existing RiskProvider
		if err := tx.First(&existing, provider.Id).Error; err != nil {
			return err
		}
		row.CreatedAt = existing.CreatedAt
		row.KeysRevision = existing.KeysRevision
		if keys == nil {
			row.ApiKeys = existing.ApiKeys
		} else {
			row.KeysRevision++
		}
		if err := tx.Model(&RiskProvider{}).Where("id = ?", provider.Id).Updates(map[string]any{
			"name": row.Name, "provider_type": row.ProviderType, "base_url": row.BaseURL,
			"model": row.Model, "proxy": row.Proxy, "api_keys": row.ApiKeys,
			"keys_revision": row.KeysRevision, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		saved = row
		return bumpRiskState(tx, state, false)
	})
	if err == nil {
		*provider = saved
	}
	return err
}

// GetRiskRefusal returns the unexpired exact refusal record, or nil.
func GetRiskRefusal(namespace string, userID int, fingerprint string, now int64) (*RiskRefusal, error) {
	var row RiskRefusal
	err := DB.Where("namespace = ? AND user_id = ? AND fingerprint = ?", namespace, userID, fingerprint).
		Where("expires_at = 0 OR expires_at > ?", now).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ListRiskRefusalCandidates returns the most recent signed refusals of one
// user and model in a namespace for near-duplicate comparison.
func ListRiskRefusalCandidates(namespace string, userID int, model string, signatureVersion, limit int, now int64) ([]RiskRefusal, error) {
	var rows []RiskRefusal
	err := DB.Select("id", "fingerprint", "signature", "signature_version", "source_request_id").
		Where("namespace = ? AND user_id = ? AND model = ?", namespace, userID, model).
		Where("signature_version = ? AND signature <> ''", signatureVersion).
		Where("expires_at = 0 OR expires_at > ?", now).
		Order("last_seen_at desc").Limit(limit).Find(&rows).Error
	return rows, err
}

// RememberRiskRefusal upserts an upstream refusal. A permanent record stays
// permanent; non-empty new details replace old ones.
func RememberRiskRefusal(rec *RiskRefusal) error {
	return riskTransaction(func(tx *gorm.DB) error {
		if _, err := lockRiskState(tx); err != nil {
			return err
		}
		// A delayed upstream callback must not recreate a refusal removed by
		// manual review. The request alias survives fingerprint deduplication.
		var rejected RiskReview
		query := tx.Where("namespace = ? AND user_id = ?", rec.Namespace, rec.UserId).
			Where("status IN ?", []string{RiskReviewDismissed, RiskReviewRevoked}).
			Where("request_id = ? OR (fingerprint = ? AND created_at <= ? AND reviewed_at >= ?)", rec.SourceRequestId, rec.Fingerprint, rec.LastSeenAt, rec.LastSeenAt)
		err := query.First(&rejected).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var alias RiskReview
		err = tx.Where("namespace = ? AND user_id = ? AND request_id = ? AND duplicate_of > 0", rec.Namespace, rec.UserId, rec.SourceRequestId).First(&alias).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			var original RiskReview
			if err := tx.First(&original, alias.DuplicateOf).Error; err != nil {
				return err
			}
			if original.Status == RiskReviewDismissed || original.Status == RiskReviewRevoked {
				return nil
			}
		}
		var existing RiskRefusal
		err = lockForUpdate(tx).Where("namespace = ? AND user_id = ? AND fingerprint = ?", rec.Namespace, rec.UserId, rec.Fingerprint).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row := *rec
			row.FirstSeenAt, row.LastSeenAt = rec.LastSeenAt, rec.LastSeenAt
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}
		updates := map[string]any{"last_seen_at": rec.LastSeenAt}
		if rec.ErrorMessage != "" {
			updates["error_message"] = rec.ErrorMessage
		}
		if rec.Preview != "" {
			updates["preview"] = rec.Preview
		}
		if rec.Signature != "" {
			updates["signature"] = rec.Signature
			updates["signature_version"] = rec.SignatureVersion
		}
		switch {
		case existing.ExpiresAt == 0 || rec.ExpiresAt == 0:
			updates["expires_at"] = 0
		default:
			updates["expires_at"] = rec.ExpiresAt
		}
		return tx.Model(&RiskRefusal{}).Where("id = ?", existing.Id).Updates(updates).Error
	})
}

func HitRiskRefusal(id int, now int64) error {
	return DB.Model(&RiskRefusal{}).Where("id = ?", id).
		Updates(map[string]any{"hit_count": gorm.Expr("hit_count + 1"), "last_seen_at": now}).Error
}

// RiskListFilter narrows administrator list queries.
type RiskListFilter struct {
	Namespace   string
	UserId      int
	Model       string
	SubjectType string
	ActiveOnly  bool
	Layer       string
	Outcome     string
	RequestId   string
	GroupName   string
	Start       int64
	End         int64
}

func ListRiskRefusals(filter RiskListFilter, offset, limit int) ([]RiskRefusal, int64, error) {
	query := DB.Model(&RiskRefusal{}).Where("namespace = ?", filter.Namespace)
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Model != "" {
		query = query.Where("model = ?", filter.Model)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []RiskRefusal
	err := query.Order("last_seen_at desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func DeleteRiskRefusal(id int) (bool, error) {
	result := DB.Delete(&RiskRefusal{}, id)
	return result.RowsAffected > 0, result.Error
}

func ClearRiskRefusals(namespace string, userID int) (int64, error) {
	query := DB.Where("namespace = ?", namespace)
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	result := query.Delete(&RiskRefusal{})
	return result.RowsAffected, result.Error
}

// GetActiveRiskBlock returns the unreleased, unexpired block, or nil.
func GetActiveRiskBlock(namespace, subjectType, subjectValue string, now int64) (*RiskSubjectBlock, error) {
	var row RiskSubjectBlock
	err := DB.Where("namespace = ? AND subject_type = ? AND subject_value = ?", namespace, subjectType, subjectValue).
		Where("released_at = 0").Where("expires_at = 0 OR expires_at > ?", now).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// BlockRiskSubject creates or re-arms the block on one subject.
func BlockRiskSubject(rec *RiskSubjectBlock) error {
	return riskTransaction(func(tx *gorm.DB) error {
		var existing RiskSubjectBlock
		err := lockForUpdate(tx).Where("namespace = ? AND subject_type = ? AND subject_value = ?", rec.Namespace, rec.SubjectType, rec.SubjectValue).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row := *rec
			row.FirstSeenAt, row.LastSeenAt = rec.LastSeenAt, rec.LastSeenAt
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&RiskSubjectBlock{}).Where("id = ?", existing.Id).Updates(map[string]any{
			"reason": rec.Reason, "fingerprint": rec.Fingerprint, "source_request_id": rec.SourceRequestId,
			"last_seen_at": rec.LastSeenAt, "expires_at": rec.ExpiresAt, "released_at": 0, "released_by": 0,
		}).Error
	})
}

func HitRiskBlock(id int, now int64) error {
	return DB.Model(&RiskSubjectBlock{}).Where("id = ?", id).
		Updates(map[string]any{"hit_count": gorm.Expr("hit_count + 1"), "last_seen_at": now}).Error
}

func ReleaseRiskBlock(id, operatorID int, now int64) (bool, error) {
	result := DB.Model(&RiskSubjectBlock{}).Where("id = ? AND released_at = 0", id).
		Updates(map[string]any{"released_at": now, "released_by": operatorID})
	return result.RowsAffected > 0, result.Error
}

func ClearRiskBlocks(namespace string, operatorID int, now int64) (int64, error) {
	result := DB.Model(&RiskSubjectBlock{}).Where("namespace = ? AND released_at = 0", namespace).
		Updates(map[string]any{"released_at": now, "released_by": operatorID})
	return result.RowsAffected, result.Error
}

func ListRiskBlocks(filter RiskListFilter, now int64, offset, limit int) ([]RiskSubjectBlock, int64, error) {
	query := DB.Model(&RiskSubjectBlock{})
	if filter.Namespace != "" {
		query = query.Where("namespace = ?", filter.Namespace)
	}
	if filter.SubjectType != "" {
		query = query.Where("subject_type = ?", filter.SubjectType)
	}
	if filter.ActiveOnly {
		query = query.Where("released_at = 0").Where("expires_at = 0 OR expires_at > ?", now)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []RiskSubjectBlock
	err := query.Order("last_seen_at desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func InsertRiskEvents(events []RiskEvent) error {
	if len(events) == 0 {
		return nil
	}
	return DB.CreateInBatches(events, 200).Error
}

func ListRiskEvents(filter RiskListFilter, offset, limit int) ([]RiskEvent, int64, error) {
	query := DB.Model(&RiskEvent{})
	if filter.Namespace != "" {
		query = query.Where("namespace = ?", filter.Namespace)
	}
	if filter.GroupName != "" {
		query = query.Where("effective_group = ?", filter.GroupName)
	}
	if filter.Layer != "" {
		query = query.Where("layer = ?", filter.Layer)
	}
	if filter.Outcome != "" {
		query = query.Where("outcome = ?", filter.Outcome)
	}
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.RequestId != "" {
		query = query.Where("request_id = ?", filter.RequestId)
	}
	if filter.Start > 0 {
		query = query.Where("created_at >= ?", filter.Start)
	}
	if filter.End > 0 {
		query = query.Where("created_at <= ?", filter.End)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []RiskEvent
	err := query.Order("id desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func GetRiskEvent(id int) (*RiskEvent, error) {
	var row RiskEvent
	if err := DB.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// RiskCounterKey identifies one hourly counter row.
type RiskCounterKey struct {
	Bucket    int64
	Namespace string
	GroupName string
	Route     string
	Outcome   string
	Layer     string
	Reason    string
	IsFirst   int
}

// AddRiskCounters adds hits with a portable UPDATE-then-INSERT, retrying the
// whole transaction when two nodes insert the same key concurrently.
func AddRiskCounters(deltas map[RiskCounterKey]int64) error {
	return riskTransaction(func(tx *gorm.DB) error {
		for key, hits := range deltas {
			result := tx.Model(&RiskCounter{}).
				Where("bucket = ? AND namespace = ? AND group_name = ? AND route = ? AND outcome = ? AND layer = ? AND reason = ? AND is_first = ?",
					key.Bucket, key.Namespace, key.GroupName, key.Route, key.Outcome, key.Layer, key.Reason, key.IsFirst).
				Update("hits", gorm.Expr("hits + ?", hits))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				continue
			}
			if err := tx.Create(&RiskCounter{
				Bucket: key.Bucket, Namespace: key.Namespace, GroupName: key.GroupName, Route: key.Route,
				Outcome: key.Outcome, Layer: key.Layer, Reason: key.Reason, IsFirst: key.IsFirst, Hits: hits,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// RiskCounterTotal is one aggregated counter dimension.
type RiskCounterTotal struct {
	Outcome   string `json:"outcome"`
	Layer     string `json:"layer"`
	Reason    string `json:"reason"`
	GroupName string `json:"group_name"`
	IsFirst   int    `json:"is_first"`
	Hits      int64  `json:"hits"`
}

func SumRiskCounters(start, end int64, groupName string) ([]RiskCounterTotal, error) {
	query := DB.Model(&RiskCounter{}).Select("outcome, layer, reason, group_name, is_first, SUM(hits) AS hits").
		Where("bucket >= ? AND bucket <= ?", start, end)
	if groupName != "" {
		query = query.Where("group_name = ?", groupName)
	}
	var rows []RiskCounterTotal
	err := query.Group("outcome, layer, reason, group_name, is_first").Scan(&rows).Error
	return rows, err
}

// RiskKeywordCount is one matched keyword with its block count.
type RiskKeywordCount struct {
	Layer   string `json:"layer"`
	Keyword string `json:"keyword"`
	Count   int64  `json:"count"`
}

func TopRiskKeywords(start, end int64, groupName string, limit int) ([]RiskKeywordCount, error) {
	query := DB.Model(&RiskEvent{}).Select("layer, keyword, COUNT(*) AS count").
		Where("created_at >= ? AND created_at <= ? AND keyword <> ''", start, end).
		Where("outcome IN ?", []string{"block", "would_block"})
	if groupName != "" {
		query = query.Where("effective_group = ?", groupName)
	}
	var rows []RiskKeywordCount
	err := query.Group("layer, keyword").Order("count desc").Limit(limit).Scan(&rows).Error
	return rows, err
}

// CountRiskStrikes counts a user's confirmed risk hits in a namespace since a
// time: local blocks other than replays of an active subject block, and
// learned upstream refusals.
func CountRiskStrikes(namespace string, userID int, since int64) (int64, error) {
	var count int64
	err := DB.Model(&RiskEvent{}).
		Where("namespace = ? AND user_id = ? AND created_at >= ?", namespace, userID, since).
		Where("outcome IN ?", []string{"block", "upstream_refusal"}).
		Where("reason <> ?", "subject").
		Count(&count).Error
	return count, err
}

func deleteRiskBatch(model any, where string, args ...any) (int64, error) {
	var total int64
	for {
		var ids []int
		if err := DB.Model(model).Where(where, args...).Order("id asc").Limit(1000).Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		result := DB.Where("id IN ?", ids).Where(where, args...).Delete(model)
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if len(ids) < 1000 {
			return total, nil
		}
	}
}

// CleanupRiskRecords removes expired refusals and blocks and events or
// counters older than their retention cutoffs.
func CleanupRiskRecords(now, eventsBefore, countersBefore int64) (int64, error) {
	var total int64
	steps := []struct {
		model any
		where string
		args  []any
	}{
		{&RiskEvent{}, "created_at < ?", []any{eventsBefore}},
		{&RiskCounter{}, "bucket < ?", []any{countersBefore}},
		{&RiskRefusal{}, "expires_at > 0 AND expires_at <= ?", []any{now}},
		{&RiskSubjectBlock{}, "(expires_at > 0 AND expires_at <= ?) OR (released_at > 0 AND released_at < ?)", []any{now, eventsBefore}},
	}
	for _, step := range steps {
		n, err := deleteRiskBatch(step.model, step.where, step.args...)
		total += n
		if err != nil {
			return total, err
		}
	}
	removed, err := cleanupRiskReviews(now, eventsBefore)
	return total + removed, err
}
