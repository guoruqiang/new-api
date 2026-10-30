package risk

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	snapshotPollInterval = 5 * time.Second
	snapshotFreshness    = 120 * time.Second
	BaselinePolicyKey    = "baseline"
)

// Binding selects a policy revision and mode.
type Binding struct {
	PolicyKey      string `json:"policy_key"`
	PolicyRevision int    `json:"policy_revision"`
	Mode           string `json:"mode"`
}

// GroupBinding overrides the default binding for one group namespace.
type GroupBinding struct {
	Namespace      string `json:"namespace"`
	PolicyKey      string `json:"policy_key"`
	PolicyRevision int    `json:"policy_revision"`
	Mode           string `json:"mode"`
}

// Retention bounds how long events, counters and reviewed violations are kept.
type Retention struct {
	EventsDays     int `json:"events_days"`
	CountersDays   int `json:"counters_days"`
	ViolationsDays int `json:"violations_days"`
}

// ScoreConfig is the user reputation policy. Every user starts at 100; each
// confirmed violation deducts its category's points, and a violation while
// the score is already zero bans the account when AutoBan is on.
type ScoreConfig struct {
	AutoBan     bool           `json:"auto_ban"`
	EmailNotify bool           `json:"email_notify"`
	Default     int            `json:"default"`
	Deductions  map[string]int `json:"deductions"`
}

// Deduction is the points a violation of category costs.
func (s ScoreConfig) Deduction(category string) int {
	if points, ok := s.Deductions[category]; ok {
		return points
	}
	return s.Default
}

// ScoreCategories are the violation categories the decision model reports.
var ScoreCategories = []string{"nsfw", "jailbreak", "reverse", "pentest", "cyber", "distill", "safety", "custom"}

// ConfigPayload is one published configuration revision. Exempt groups are
// counted but not audited.
type ConfigPayload struct {
	Enabled          bool           `json:"enabled"`
	ExemptNamespaces []string       `json:"exempt_namespaces"`
	DefaultBinding   Binding        `json:"default_binding"`
	GroupBindings    []GroupBinding `json:"group_bindings"`
	Retention        Retention      `json:"retention"`
	Score            ScoreConfig    `json:"score"`
}

func DefaultConfigPayload() ConfigPayload {
	return ConfigPayload{
		ExemptNamespaces: []string{},
		DefaultBinding:   Binding{PolicyKey: BaselinePolicyKey, PolicyRevision: 1, Mode: ModeObserve},
		GroupBindings:    []GroupBinding{},
		Retention:        Retention{EventsDays: 30, CountersDays: 90, ViolationsDays: 180},
		Score:            ScoreConfig{EmailNotify: true, Default: 20, Deductions: map[string]int{"nsfw": 100, "jailbreak": 100}},
	}
}

func ParseConfigPayload(raw []byte) (ConfigPayload, error) {
	cfg := DefaultConfigPayload()
	if len(raw) == 0 {
		return cfg, nil
	}
	var legacy struct {
		BypassNamespaces []string `json:"bypass_namespaces"`
	}
	// Stored deductions replace the defaults rather than merging into them,
	// so a category reset to the default stays reset.
	defaults := cfg.Score.Deductions
	cfg.Score.Deductions = nil
	if err := common.Unmarshal(raw, &cfg); err != nil {
		return ConfigPayload{}, err
	}
	if cfg.Score.Deductions == nil {
		cfg.Score.Deductions = defaults
	}
	if err := common.Unmarshal(raw, &legacy); err != nil {
		return ConfigPayload{}, err
	}
	// Bypassed groups from earlier revisions are exempt groups now.
	for _, ns := range legacy.BypassNamespaces {
		if !slices.Contains(cfg.ExemptNamespaces, ns) {
			cfg.ExemptNamespaces = append(cfg.ExemptNamespaces, ns)
		}
	}
	if cfg.ExemptNamespaces == nil {
		cfg.ExemptNamespaces = []string{}
	}
	if cfg.GroupBindings == nil {
		cfg.GroupBindings = []GroupBinding{}
	}
	return cfg, nil
}

// ResolvedBinding is a compiled policy with its mode.
type ResolvedBinding struct {
	Policy *CompiledPolicy
	Mode   string
	Source string
}

// Snapshot is the immutable configuration one request is judged against.
type Snapshot struct {
	Enabled        bool
	Available      bool
	ConfigRevision int64
	ChangeRevision int64
	Scope          ExemptionScope
	Default        ResolvedBinding
	Groups         map[string]ResolvedBinding
	Providers      map[int]ProviderConfig
	Retention      Retention
	Score          ScoreConfig
	InvalidRefs    []string
	NamespaceGroup map[string]string

	confirmedAt atomic.Int64
}

func (s *Snapshot) confirm(now time.Time) { s.confirmedAt.Store(now.UnixNano()) }

// ConfirmedAt is when the database last confirmed this snapshot.
func (s *Snapshot) ConfirmedAt() time.Time { return time.Unix(0, s.confirmedAt.Load()) }

// Fresh reports whether an enabled snapshot may still be trusted. A disabled
// snapshot never expires: losing the database must not turn an unused risk
// module into an outage.
func (s *Snapshot) Fresh(now time.Time) bool {
	if !s.Enabled {
		return true
	}
	return now.Sub(s.ConfirmedAt()) <= snapshotFreshness
}

// BindingFor returns the group binding of a namespace or the default one.
func (s *Snapshot) BindingFor(namespace string) ResolvedBinding {
	if b, ok := s.Groups[namespace]; ok {
		return b
	}
	return s.Default
}

var current atomic.Pointer[Snapshot]

// CurrentSnapshot is nil until the first load; nil means disabled.
func CurrentSnapshot() *Snapshot { return current.Load() }

// InstallSnapshot replaces the active snapshot; the sync loop and tests use it.
func InstallSnapshot(s *Snapshot) { current.Store(s) }

func compileStoredPolicy(key string, revision int) (*CompiledPolicy, error) {
	row, err := model.GetRiskPolicy(key, revision)
	if err != nil {
		return nil, fmt.Errorf("policy %s@%d: %w", key, revision, err)
	}
	payload, err := ParsePolicyPayload([]byte(row.Payload))
	if err != nil {
		return nil, fmt.Errorf("policy %s@%d: %w", key, revision, err)
	}
	return CompilePolicy(key, revision, payload)
}

func providerConfigOf(row model.RiskProvider) ProviderConfig {
	return ProviderConfig{ID: row.Id, Type: row.ProviderType, BaseURL: row.BaseURL, Model: row.Model, Proxy: row.Proxy, KeyLines: row.KeyLines(), KeysRevision: row.KeysRevision}
}

// BuildSnapshot reads one consistent configuration: it retries while the
// change revision moves underneath it.
func BuildSnapshot() (*Snapshot, error) {
	for range 3 {
		before, err := model.GetRiskChangeRevision()
		if err != nil {
			return nil, err
		}
		snap, err := buildSnapshotOnce()
		if err != nil {
			return nil, err
		}
		after, err := model.GetRiskChangeRevision()
		if err != nil {
			return nil, err
		}
		if before == after {
			snap.ChangeRevision = after
			snap.confirm(time.Now())
			return snap, nil
		}
	}
	return nil, errors.New("risk configuration kept changing while loading")
}

func buildSnapshotOnce() (*Snapshot, error) {
	row, err := model.GetLatestRiskConfig()
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{Available: true, Groups: map[string]ResolvedBinding{}, Providers: map[int]ProviderConfig{}, NamespaceGroup: map[string]string{}}
	snap.Scope = ExemptionScope{GroupNamespaces: map[string]string{}, Exempt: map[string]bool{}}
	if row == nil {
		return snap, nil
	}
	cfg, err := ParseConfigPayload([]byte(row.Payload))
	if err != nil {
		return nil, err
	}
	snap.Enabled = cfg.Enabled
	snap.ConfigRevision = row.Revision
	snap.Retention = cfg.Retention
	snap.Score = cfg.Score

	namespaces, err := model.ListRiskGroupNamespaces(false)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, ns := range namespaces {
		snap.NamespaceGroup[ns.Namespace] = ns.GroupName
		if ns.Active {
			snap.Scope.GroupNamespaces[ns.GroupName] = ns.Namespace
			active[ns.Namespace] = true
		}
	}
	for _, ns := range cfg.ExemptNamespaces {
		if !active[ns] {
			snap.InvalidRefs = append(snap.InvalidRefs, ns)
			continue
		}
		snap.Scope.Exempt[ns] = true
	}

	providers, err := model.ListRiskProviders()
	if err != nil {
		return nil, err
	}
	for _, p := range providers {
		snap.Providers[p.Id] = providerConfigOf(p)
	}

	resolve := func(key string, revision int, mode, source string) (ResolvedBinding, error) {
		policy, err := compileStoredPolicy(key, revision)
		if err != nil {
			return ResolvedBinding{}, err
		}
		if policy.Payload.Model.Enabled {
			if _, ok := snap.Providers[policy.Payload.Model.ProviderID]; !ok {
				return ResolvedBinding{}, fmt.Errorf("policy %s@%d references missing provider %d", key, revision, policy.Payload.Model.ProviderID)
			}
		}
		if mode != ModeObserve && mode != ModeEnforce {
			return ResolvedBinding{}, fmt.Errorf("binding %s@%d has invalid mode %q", key, revision, mode)
		}
		return ResolvedBinding{Policy: policy, Mode: mode, Source: source}, nil
	}
	snap.Default, err = resolve(cfg.DefaultBinding.PolicyKey, cfg.DefaultBinding.PolicyRevision, cfg.DefaultBinding.Mode, "default")
	if err != nil {
		return nil, err
	}
	for _, gb := range cfg.GroupBindings {
		if !active[gb.Namespace] {
			snap.InvalidRefs = append(snap.InvalidRefs, gb.Namespace)
			continue
		}
		if snap.Scope.Exempt[gb.Namespace] {
			continue
		}
		b, err := resolve(gb.PolicyKey, gb.PolicyRevision, gb.Mode, "group")
		if err != nil {
			return nil, err
		}
		snap.Groups[gb.Namespace] = b
	}
	return snap, nil
}

type syncLoop struct {
	once     sync.Once
	started  atomic.Bool
	requests chan chan error
}

var loop = &syncLoop{requests: make(chan chan error)}

// refresh is only ever called from the sync goroutine (or before it starts),
// so snapshot installation has a single writer.
func refresh(force bool) error {
	revision, err := model.GetRiskChangeRevision()
	if err != nil {
		return err
	}
	cur := current.Load()
	if !force && cur != nil && cur.ChangeRevision == revision {
		cur.confirm(time.Now())
		return nil
	}
	snap, err := BuildSnapshot()
	if err != nil {
		return err
	}
	current.Store(snap)
	return nil
}

// LoadInitialSnapshot loads the configuration before the router serves. A
// stored configuration that is enabled but cannot be built installs an
// unavailable snapshot; a database read error is returned.
func LoadInitialSnapshot() error {
	err := refresh(true)
	if err == nil {
		return nil
	}
	row, readErr := model.GetLatestRiskConfig()
	if readErr != nil {
		return readErr
	}
	enabled := false
	if row != nil {
		cfg, parseErr := ParseConfigPayload([]byte(row.Payload))
		if parseErr != nil {
			return fmt.Errorf("cannot determine risk configuration state: %w", parseErr)
		}
		enabled = cfg.Enabled
	}
	snap := &Snapshot{Enabled: enabled, Available: !enabled}
	snap.Scope = ExemptionScope{GroupNamespaces: map[string]string{}, Exempt: map[string]bool{}}
	snap.confirm(time.Now())
	current.Store(snap)
	common.SysError(common.LogText("risk configuration could not be built: %s", err.Error()))
	return nil
}

// SeedBaselinePolicy creates the built-in default policy once.
func SeedBaselinePolicy() error {
	payload := DefaultPolicyPayload()
	encoded, err := common.Marshal(payload)
	if err != nil {
		return err
	}
	return model.EnsureRiskBaselinePolicy(BaselinePolicyKey, "Baseline", string(encoded), PolicyFingerprint(payload))
}

// StartSnapshotSync polls the change revision and serves refresh requests.
func StartSnapshotSync() {
	loop.once.Do(func() {
		loop.started.Store(true)
		go func() {
			ticker := time.NewTicker(snapshotPollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := refresh(false); err != nil {
						common.SysError(common.LogText("risk configuration refresh failed: %s", err.Error()))
					}
				case done := <-loop.requests:
					done <- refresh(true)
				}
			}
		}()
	})
}

// RefreshNow rebuilds the snapshot after a local publish and waits for it.
// Before the sync goroutine starts (tests, tools) it refreshes inline.
func RefreshNow() error {
	if !loop.started.Load() {
		return refresh(true)
	}
	done := make(chan error, 1)
	loop.requests <- done
	return <-done
}

// ValidateConfigPayload checks a configuration before it is published.
func ValidateConfigPayload(cfg ConfigPayload) []*common.Message {
	problems := make([]*common.Message, 0)
	namespaces, err := model.ListRiskGroupNamespaces(true)
	if err != nil {
		return []*common.Message{common.NewMessage("Cannot read the group directory")}
	}
	active := map[string]bool{}
	for _, ns := range namespaces {
		active[ns.Namespace] = true
	}
	seen := map[string]bool{}
	for _, ns := range cfg.ExemptNamespaces {
		switch {
		case ns == ReservedAutoNamespace || ns == "auto":
			problems = append(problems, common.NewMessage("Auto cannot be exempt; auto requests are always audited"))
		case seen[ns]:
			problems = append(problems, common.NewMessage("Exempt group {{namespace}} is listed twice", map[string]any{"namespace": ns}))
		case !active[ns]:
			problems = append(problems, common.NewMessage("Group namespace {{namespace}} does not exist or was retired", map[string]any{"namespace": ns}))
		}
		seen[ns] = true
	}
	checkBinding := func(label, key string, revision int, mode string) {
		if mode != ModeObserve && mode != ModeEnforce {
			problems = append(problems, common.NewMessage("{{binding}}: mode must be observe or enforce", map[string]any{"binding": label}))
		}
		policy, err := compileStoredPolicy(key, revision)
		if err != nil {
			problems = append(problems, common.NewMessage("{{binding}}: policy {{policy}} revision {{revision}} is unavailable", map[string]any{"binding": label, "policy": key, "revision": revision}))
			return
		}
		if policy.Payload.Model.Enabled {
			provider, err := model.GetRiskProvider(policy.Payload.Model.ProviderID)
			if err != nil || provider.BaseURL == "" {
				problems = append(problems, common.NewMessage("{{binding}}: the model layer needs a provider with a base URL", map[string]any{"binding": label}))
			}
		}
	}
	checkBinding("default binding", cfg.DefaultBinding.PolicyKey, cfg.DefaultBinding.PolicyRevision, cfg.DefaultBinding.Mode)
	bound := map[string]bool{}
	for _, gb := range cfg.GroupBindings {
		label := "group binding " + gb.Namespace
		switch {
		case !active[gb.Namespace]:
			problems = append(problems, common.NewMessage("Group namespace {{namespace}} does not exist or was retired", map[string]any{"namespace": gb.Namespace}))
		case bound[gb.Namespace]:
			problems = append(problems, common.NewMessage("Group {{namespace}} can have only one binding", map[string]any{"namespace": gb.Namespace}))
		case slices.Contains(cfg.ExemptNamespaces, gb.Namespace):
			problems = append(problems, common.NewMessage("Exempt group {{namespace}} cannot have a binding", map[string]any{"namespace": gb.Namespace}))
		}
		bound[gb.Namespace] = true
		checkBinding(label, gb.PolicyKey, gb.PolicyRevision, gb.Mode)
	}
	if cfg.Retention.EventsDays < 1 || cfg.Retention.EventsDays > 3650 {
		problems = append(problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": "retention.events_days", "min": 1, "max": 3650}))
	}
	if cfg.Retention.CountersDays < 1 || cfg.Retention.CountersDays > 3650 {
		problems = append(problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": "retention.counters_days", "min": 1, "max": 3650}))
	}
	if cfg.Retention.ViolationsDays < 1 || cfg.Retention.ViolationsDays > 3650 {
		problems = append(problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": "retention.violations_days", "min": 1, "max": 3650}))
	}
	if cfg.Score.Default < 1 || cfg.Score.Default > 100 {
		problems = append(problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": "score.default", "min": 1, "max": 100}))
	}
	for category, points := range cfg.Score.Deductions {
		if !slices.Contains(ScoreCategories, category) {
			problems = append(problems, common.NewMessage("Unknown risk violation category {{category}}", map[string]any{"category": category}))
		}
		if points < 1 || points > 100 {
			problems = append(problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": "score.deductions." + category, "min": 1, "max": 100}))
		}
	}
	return problems
}
