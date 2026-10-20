package risk

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var policyKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ConfigView is the stored configuration as administrators see it.
type ConfigView struct {
	Revision int64         `json:"revision"`
	Payload  ConfigPayload `json:"payload"`
}

func LatestConfig() (ConfigView, error) {
	row, err := model.GetLatestRiskConfig()
	if err != nil {
		return ConfigView{}, err
	}
	if row == nil {
		return ConfigView{Payload: DefaultConfigPayload()}, nil
	}
	cfg, err := ParseConfigPayload([]byte(row.Payload))
	if err != nil {
		return ConfigView{}, err
	}
	return ConfigView{Revision: row.Revision, Payload: cfg}, nil
}

// PublishConfig validates and publishes a configuration revision, then
// refreshes this node. It returns the validation problems, if any.
func PublishConfig(expectedRevision int64, cfg ConfigPayload, operatorID int) (int64, []*common.Message, error) {
	if cfg.ExemptNamespaces == nil {
		cfg.ExemptNamespaces = []string{}
	}
	if cfg.BypassNamespaces == nil {
		cfg.BypassNamespaces = []string{}
	}
	if cfg.GroupBindings == nil {
		cfg.GroupBindings = []GroupBinding{}
	}
	if problems := ValidateConfigPayload(cfg); len(problems) > 0 {
		return 0, problems, nil
	}
	encoded, err := common.Marshal(cfg)
	if err != nil {
		return 0, nil, err
	}
	revision, err := model.PublishRiskConfig(expectedRevision, string(encoded), operatorID)
	if err != nil {
		return 0, nil, err
	}
	return revision, nil, RefreshNow()
}

// GroupEntry is one concrete group in the audit directory.
type GroupEntry struct {
	GroupName      string `json:"group_name"`
	Namespace      string `json:"namespace"`
	Generation     int    `json:"generation"`
	Exempt         bool   `json:"exempt"`
	Bypassed       bool   `json:"bypassed"`
	BindingSource  string `json:"binding_source"`
	PolicyKey      string `json:"policy_key"`
	PolicyRevision int    `json:"policy_revision"`
	Mode           string `json:"mode"`
}

// InvalidRef is a configuration reference to a retired group namespace.
type InvalidRef struct {
	Namespace string `json:"namespace"`
	GroupName string `json:"group_name"`
	Kind      string `json:"kind"`
}

// GroupDirectory lists every active group with its stored audit setting.
type GroupDirectory struct {
	Groups      []GroupEntry `json:"groups"`
	InvalidRefs []InvalidRef `json:"invalid_refs"`
	Unmapped    []string     `json:"unmapped"`
}

func ListGroupDirectory() (GroupDirectory, error) {
	view, err := LatestConfig()
	if err != nil {
		return GroupDirectory{}, err
	}
	rows, err := model.ListRiskGroupNamespaces(false)
	if err != nil {
		return GroupDirectory{}, err
	}
	exempt := map[string]bool{}
	for _, ns := range view.Payload.ExemptNamespaces {
		exempt[ns] = true
	}
	bypassed := map[string]bool{}
	for _, ns := range view.Payload.BypassNamespaces {
		bypassed[ns] = true
	}
	bindings := map[string]GroupBinding{}
	for _, gb := range view.Payload.GroupBindings {
		bindings[gb.Namespace] = gb
	}
	dir := GroupDirectory{Groups: []GroupEntry{}, InvalidRefs: []InvalidRef{}, Unmapped: []string{}}
	mapped := map[string]bool{}
	for _, row := range rows {
		if !row.Active {
			if exempt[row.Namespace] {
				dir.InvalidRefs = append(dir.InvalidRefs, InvalidRef{Namespace: row.Namespace, GroupName: row.GroupName, Kind: "exempt"})
			}
			if bypassed[row.Namespace] {
				dir.InvalidRefs = append(dir.InvalidRefs, InvalidRef{Namespace: row.Namespace, GroupName: row.GroupName, Kind: "bypass"})
			}
			if _, ok := bindings[row.Namespace]; ok {
				dir.InvalidRefs = append(dir.InvalidRefs, InvalidRef{Namespace: row.Namespace, GroupName: row.GroupName, Kind: "binding"})
			}
			continue
		}
		mapped[row.GroupName] = true
		entry := GroupEntry{GroupName: row.GroupName, Namespace: row.Namespace, Generation: row.Generation}
		switch gb, bound := bindings[row.Namespace]; {
		case bypassed[row.Namespace]:
			entry.Bypassed, entry.BindingSource = true, "bypass"
		case exempt[row.Namespace]:
			entry.Exempt, entry.BindingSource = true, "exempt"
		case bound:
			entry.BindingSource, entry.PolicyKey, entry.PolicyRevision, entry.Mode = "group", gb.PolicyKey, gb.PolicyRevision, gb.Mode
		default:
			d := view.Payload.DefaultBinding
			entry.BindingSource, entry.PolicyKey, entry.PolicyRevision, entry.Mode = "default", d.PolicyKey, d.PolicyRevision, d.Mode
		}
		dir.Groups = append(dir.Groups, entry)
	}
	for name := range ratio_setting.GetGroupRatioCopy() {
		if name != "auto" && !mapped[name] {
			dir.Unmapped = append(dir.Unmapped, name)
		}
	}
	sort.Strings(dir.Unmapped)
	return dir, nil
}

// PolicyView is one policy revision with its decoded payload.
type PolicyView struct {
	model.RiskPolicy
	Decoded PolicyPayload `json:"decoded"`
}

func GetPolicy(key string, revision int) (PolicyView, error) {
	row, err := model.GetRiskPolicy(key, revision)
	if err != nil {
		return PolicyView{}, err
	}
	payload, err := ParsePolicyPayload([]byte(row.Payload))
	if err != nil {
		return PolicyView{}, err
	}
	return PolicyView{RiskPolicy: *row, Decoded: payload}, nil
}

// CreatePolicyRevision validates, compiles and stores a new policy revision.
func CreatePolicyRevision(key, name string, baseRevision int, payload PolicyPayload, operatorID int) (*model.RiskPolicy, []*common.Message, error) {
	view, problems, err := SavePolicyRevision(key, name, baseRevision, payload, operatorID, nil)
	if view == nil {
		return nil, problems, err
	}
	return &view.RiskPolicy, problems, err
}

// PolicyActivation publishes one policy across all bindings. Enable only turns
// auditing on; an ordinary save preserves its existing enabled state and modes.
type PolicyActivation struct {
	ExpectedConfigRevision int64 `json:"expected_config_revision"`
	Enable                 bool  `json:"enable,omitempty"`
}

type PolicySaveView struct {
	model.RiskPolicy
	ConfigRevision *int64 `json:"config_revision,omitempty"`
}

// SavePolicyRevision optionally saves the policy and its live bindings in one
// transaction, retaining immutable revisions only for concurrency and auditing.
func SavePolicyRevision(key, name string, baseRevision int, payload PolicyPayload, operatorID int, activation *PolicyActivation) (*PolicySaveView, []*common.Message, error) {
	key = strings.TrimSpace(key)
	if !policyKeyPattern.MatchString(key) {
		return nil, []*common.Message{common.NewMessage("Policy key must be 1-64 lowercase letters, digits, - or _")}, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = key
	}
	if len([]rune(name)) > 128 {
		return nil, []*common.Message{common.NewMessage("{{field}} allows at most {{max}} characters", map[string]any{"field": "name", "max": 128})}, nil
	}
	NormalizePolicyPayload(&payload)
	if problems := ValidatePolicyPayload(payload); len(problems) > 0 {
		return nil, problems, nil
	}
	if payload.Model.Enabled {
		provider, err := model.GetRiskProvider(payload.Model.ProviderID)
		if err != nil || strings.TrimSpace(provider.BaseURL) == "" {
			return nil, []*common.Message{common.NewMessage("Model provider must have a base URL")}, nil
		}
	}
	if _, err := CompilePolicy(key, baseRevision+1, payload); err != nil {
		return nil, nil, err
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	var publish *model.RiskPolicyActivation
	if activation != nil {
		publish = &model.RiskPolicyActivation{
			ExpectedConfigRevision: activation.ExpectedConfigRevision,
			BuildConfig: func(current *model.RiskConfig, policy *model.RiskPolicy) (string, error) {
				cfg, err := ParseConfigPayload([]byte(current.Payload))
				if err != nil {
					return "", err
				}
				changed := current.Id == 0
				if activation.Enable && !cfg.Enabled {
					cfg.Enabled = true
					changed = true
				}
				if cfg.DefaultBinding.PolicyKey != policy.PolicyKey || cfg.DefaultBinding.PolicyRevision != policy.Revision {
					cfg.DefaultBinding.PolicyKey = policy.PolicyKey
					cfg.DefaultBinding.PolicyRevision = policy.Revision
					changed = true
				}
				for i := range cfg.GroupBindings {
					if cfg.GroupBindings[i].PolicyKey != policy.PolicyKey || cfg.GroupBindings[i].PolicyRevision != policy.Revision {
						cfg.GroupBindings[i].PolicyKey = policy.PolicyKey
						cfg.GroupBindings[i].PolicyRevision = policy.Revision
						changed = true
					}
				}
				if !changed {
					return current.Payload, nil
				}
				encoded, err := common.Marshal(cfg)
				return string(encoded), err
			},
		}
	}
	row, config, err := model.SaveRiskPolicyRevision(key, name, baseRevision, string(encoded), PolicyFingerprint(payload), operatorID, publish)
	if err != nil {
		return nil, nil, err
	}
	view := &PolicySaveView{RiskPolicy: *row}
	if config != nil {
		view.ConfigRevision = &config.Revision
	}
	return view, nil, RefreshNow()
}

// ProviderView is a provider without its keys.
type ProviderView struct {
	Id           int        `json:"id"`
	Name         string     `json:"name"`
	ProviderType string     `json:"provider_type"`
	BaseURL      string     `json:"base_url"`
	Model        string     `json:"model"`
	Proxy        string     `json:"proxy"`
	KeysRevision int        `json:"keys_revision"`
	ApiKeyCount  int        `json:"api_key_count"`
	KeyStates    []KeyState `json:"key_states"`
	UpdatedAt    int64      `json:"updated_at"`
}

func providerView(row model.RiskProvider) ProviderView {
	cfg := providerConfigOf(row)
	return ProviderView{
		Id: row.Id, Name: row.Name, ProviderType: row.ProviderType, BaseURL: redactURLCredentials(row.BaseURL), Model: row.Model, Proxy: redactURLCredentials(row.Proxy),
		KeysRevision: row.KeysRevision, ApiKeyCount: len(cfg.KeyLines), KeyStates: ProviderKeyStates(cfg), UpdatedAt: row.UpdatedAt,
	}
}

// redactURLCredentials keeps an editable endpoint while hiding both parts of
// URL user information from readers of provider settings.
func redactURLCredentials(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.User != nil {
		u.User = url.UserPassword("****", "****")
		return u.String()
	}
	return raw
}

func ListProviderViews() ([]ProviderView, error) {
	rows, err := model.ListRiskProviders()
	if err != nil {
		return nil, err
	}
	views := make([]ProviderView, 0, len(rows))
	for _, row := range rows {
		views = append(views, providerView(row))
	}
	return views, nil
}

// ProviderInput is a create or update request; nil APIKeys keeps the stored
// keys, an empty slice clears them.
type ProviderInput struct {
	Name         string    `json:"name"`
	ProviderType string    `json:"provider_type"`
	BaseURL      string    `json:"base_url"`
	Model        string    `json:"model"`
	Proxy        string    `json:"proxy"`
	APIKeys      *[]string `json:"api_keys"`
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func validateProvider(in ProviderInput) []*common.Message {
	problems := make([]*common.Message, 0)
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		problems = append(problems, common.NewMessage("Name is required and allows at most 64 characters"))
	}
	if _, ok := presetFor(in.ProviderType); !ok {
		problems = append(problems, common.NewMessage("Provider type must be jev, laya or modernbert"))
	}
	if in.BaseURL == "" || len(in.BaseURL) > 300 || !validHTTPURL(in.BaseURL) {
		problems = append(problems, common.NewMessage("Base URL must be an http(s) URL of at most 300 characters"))
	}
	if len([]rune(in.Model)) > 120 {
		problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} characters", map[string]any{"field": "model", "max": 120}))
	}
	if in.Proxy != "" {
		_, err := common.ParseProxyURLStrict(in.Proxy)
		if len(in.Proxy) > 255 || err != nil {
			problems = append(problems, common.NewMessage("Proxy must be a proxy URL of at most 255 characters"))
		}
	}
	if in.APIKeys != nil {
		if len(*in.APIKeys) > 999 {
			problems = append(problems, common.NewMessage("API keys allow at most 999 lines"))
		}
		for _, line := range *in.APIKeys {
			if len(line) > 500 {
				problems = append(problems, common.NewMessage("Each API key line allows at most 500 characters"))
				break
			}
			if m := keyLinePattern.FindStringSubmatch(line); m != nil && !validHTTPURL(m[1]) {
				problems = append(problems, common.NewMessage("API key endpoints must be valid HTTP URLs without user information"))
				break
			}
		}
	}
	return problems
}

// SaveProvider creates (id 0) or updates a provider.
func SaveProvider(id int, in ProviderInput) (ProviderView, []*common.Message, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.Model = strings.TrimSpace(in.Model)
	in.Proxy = strings.TrimSpace(in.Proxy)
	if id > 0 {
		stored, err := model.GetRiskProvider(id)
		if err != nil {
			return ProviderView{}, nil, err
		}
		if in.Proxy == redactURLCredentials(stored.Proxy) {
			in.Proxy = stored.Proxy
		}
	}
	var keys []string
	if in.APIKeys != nil {
		keys = cleanList(*in.APIKeys)
		in.APIKeys = &keys
	}
	if problems := validateProvider(in); len(problems) > 0 {
		return ProviderView{}, problems, nil
	}
	row := &model.RiskProvider{Id: id, Name: in.Name, ProviderType: in.ProviderType, BaseURL: in.BaseURL, Model: in.Model, Proxy: in.Proxy}
	if in.APIKeys == nil {
		keys = nil
	} else if keys == nil {
		keys = []string{}
	}
	if err := model.SaveRiskProvider(row, keys); err != nil {
		return ProviderView{}, nil, err
	}
	saved, err := model.GetRiskProvider(row.Id)
	if err != nil {
		return ProviderView{}, nil, err
	}
	return providerView(*saved), nil, RefreshNow()
}

// ProbeModels lists a provider's models. A stored provider's first key is
// used when no key is typed.
func ProbeModels(ctx context.Context, providerID int, baseURL, apiKey, proxy string) (string, []string, error) {
	if providerID > 0 {
		row, err := model.GetRiskProvider(providerID)
		if err != nil {
			return "", nil, err
		}
		if baseURL == "" {
			baseURL = row.BaseURL
		}
		if proxy == "" {
			proxy = row.Proxy
		} else if proxy == redactURLCredentials(row.Proxy) {
			proxy = row.Proxy
		}
		if apiKey == "" {
			if lines := row.KeyLines(); len(lines) > 0 {
				storedEndpoint := row.BaseURL
				apiKey = lines[0]
				if m := keyLinePattern.FindStringSubmatch(apiKey); m != nil {
					storedEndpoint = m[1]
					apiKey = m[2]
				}
				if ModelsURL(baseURL) != ModelsURL(row.BaseURL) && ModelsURL(baseURL) != ModelsURL(storedEndpoint) || proxy != row.Proxy {
					return "", nil, common.NewMessage("Changing the endpoint or proxy requires an explicit API key")
				}
				baseURL = storedEndpoint
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return ListModels(ctx, baseURL, apiKey, proxy)
}

// StageStates reports whether each layer of a policy is on.
func StageStates(policy *CompiledPolicy, providers map[int]ProviderConfig) map[string]string {
	state := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}
	p := policy.Payload
	modelState := "off"
	if p.Model.Enabled {
		modelState = "on"
		if cfg, ok := providers[p.Model.ProviderID]; !ok || cfg.BaseURL == "" {
			modelState = "unset"
		}
	}
	return map[string]string{
		LayerDistill:   state(p.Distill.Enabled),
		LayerHardRegex: state(p.HardRegex.Enabled),
		LayerRefusal:   state(p.Refusal.Enabled),
		LayerModel:     modelState,
	}
}

// CounterRow is one aggregated statistic row.
type CounterRow struct {
	Outcome string `json:"outcome"`
	Layer   string `json:"layer"`
	Reason  string `json:"reason"`
	Count   int64  `json:"count"`
}

// Overview is the dashboard summary for a time window.
type Overview struct {
	Start          int64                    `json:"start"`
	End            int64                    `json:"end"`
	Enabled        bool                     `json:"enabled"`
	Fresh          bool                     `json:"fresh"`
	Available      bool                     `json:"available"`
	ConfigRevision int64                    `json:"config_revision"`
	ConfirmedAt    int64                    `json:"confirmed_at"`
	Requests       int64                    `json:"requests"`
	Attempts       int64                    `json:"attempts"`
	Outcomes       map[string]int64         `json:"outcomes"`
	Blocks         []CounterRow             `json:"blocks"`
	Passes         []CounterRow             `json:"passes"`
	Keywords       []model.RiskKeywordCount `json:"keywords"`
	Stages         map[string]string        `json:"stages"`
	InvalidRefs    int                      `json:"invalid_refs"`
	CoveredRoutes  []string                 `json:"covered_routes"`
	Uncovered      []string                 `json:"uncovered_routes"`
}

func BuildOverview(start, end int64, groupName string) (Overview, error) {
	groupName = strings.TrimSpace(groupName)
	ov := Overview{Start: start, End: end, Outcomes: map[string]int64{}, Blocks: []CounterRow{}, Passes: []CounterRow{}, CoveredRoutes: CoveredRoutes, Uncovered: UncoveredRoutes,
		Stages: map[string]string{LayerDistill: "off", LayerHardRegex: "off", LayerRefusal: "off", LayerModel: "off"}}
	if snap := current.Load(); snap != nil {
		ov.Enabled, ov.Available, ov.ConfigRevision = snap.Enabled, snap.Available, snap.ConfigRevision
		ov.Fresh = snap.Fresh(time.Now())
		ov.ConfirmedAt = snap.ConfirmedAt().Unix()
		ov.InvalidRefs = len(snap.InvalidRefs)
		if snap.Enabled {
			binding := snap.Default
			bypassed := groupName != "auto" && IsBypassed(snap.Scope, groupName, false)
			exempt := groupName != "auto" && IsExempt(snap.Scope, groupName, false)
			switch {
			case bypassed:
				for layer := range ov.Stages {
					ov.Stages[layer] = "bypass"
				}
			case exempt:
				for layer := range ov.Stages {
					ov.Stages[layer] = "exempt"
				}
			default:
				if groupName != "" && groupName != "auto" {
					binding = snap.BindingFor(snap.Scope.NamespaceFor(groupName))
				}
				if binding.Policy != nil {
					ov.Stages = StageStates(binding.Policy, snap.Providers)
				}
			}
		}
	}
	rows, err := model.SumRiskCounters(start, end, groupName)
	if err != nil {
		return ov, err
	}
	blocks := map[string]*CounterRow{}
	passes := map[string]*CounterRow{}
	for _, r := range rows {
		// Upstream learning is a later observation of an already counted
		// attempt, not another outgoing request.
		if r.Layer != LayerUpstream {
			ov.Attempts += r.Hits
		}
		if r.IsFirst == 1 {
			ov.Requests += r.Hits
		}
		ov.Outcomes[r.Outcome] += r.Hits
		key := r.Outcome + "|" + r.Layer + "|" + r.Reason
		bucket := passes
		switch r.Outcome {
		case OutcomeBlock, OutcomeWouldBlock, OutcomeErrorBlock, OutcomeUpstreamRefusal, OutcomeWouldStop:
			bucket = blocks
		}
		if row, ok := bucket[key]; ok {
			row.Count += r.Hits
			continue
		}
		bucket[key] = &CounterRow{Outcome: r.Outcome, Layer: r.Layer, Reason: r.Reason, Count: r.Hits}
	}
	for _, row := range blocks {
		ov.Blocks = append(ov.Blocks, *row)
	}
	for _, row := range passes {
		ov.Passes = append(ov.Passes, *row)
	}
	byCount := func(rows []CounterRow) func(i, j int) bool {
		return func(i, j int) bool {
			if rows[i].Count != rows[j].Count {
				return rows[i].Count > rows[j].Count
			}
			return rows[i].Reason < rows[j].Reason
		}
	}
	sort.Slice(ov.Blocks, byCount(ov.Blocks))
	sort.Slice(ov.Passes, byCount(ov.Passes))
	ov.Keywords, err = model.TopRiskKeywords(start, end, groupName, 20)
	return ov, err
}

// PreviewRequest asks how a sample request would be judged.
type PreviewRequest struct {
	GroupName      string `json:"group_name"`
	ViaAuto        bool   `json:"via_auto"`
	PolicyKey      string `json:"policy_key"`
	PolicyRevision int    `json:"policy_revision"`
	Mode           string `json:"mode"`
	UserID         int    `json:"user_id"`
	Body           string `json:"body"`
	RunModel       bool   `json:"run_model"`
}

// PreviewResult reports the scope decision and the layer outcome.
type PreviewResult struct {
	Enabled        bool     `json:"enabled"`
	Audited        bool     `json:"audited"`
	Exempt         bool     `json:"exempt"`
	Bypassed       bool     `json:"bypassed"`
	Namespace      string   `json:"namespace"`
	BindingSource  string   `json:"binding_source"`
	PolicyKey      string   `json:"policy_key"`
	PolicyRevision int      `json:"policy_revision"`
	Mode           string   `json:"mode"`
	ModelRun       bool     `json:"model_run"`
	Decision       Decision `json:"decision"`
}

var ErrSnapshotNotLoaded = errors.New("risk configuration is not loaded")

// Preview evaluates a sample without writing records, blocking subjects,
// using the verdict cache or touching shared key scheduling state.
func Preview(ctx context.Context, req PreviewRequest) (PreviewResult, error) {
	snap := current.Load()
	if snap == nil {
		return PreviewResult{}, ErrSnapshotNotLoaded
	}
	group := strings.TrimSpace(req.GroupName)
	namespace := snap.Scope.NamespaceFor(group)
	if req.ViaAuto && group == "" {
		namespace = ReservedAutoNamespace
	}
	res := PreviewResult{Enabled: snap.Enabled, Namespace: namespace}
	if !snap.Enabled {
		return res, nil
	}
	if IsBypassed(snap.Scope, group, req.ViaAuto) {
		res.Bypassed = true
		res.BindingSource = "bypass"
		return res, nil
	}
	if IsExempt(snap.Scope, group, req.ViaAuto) {
		res.Exempt = true
		res.BindingSource = "exempt"
		return res, nil
	}
	binding := snap.BindingFor(namespace)
	if req.PolicyKey != "" {
		policy, err := compileStoredPolicy(req.PolicyKey, req.PolicyRevision)
		if err != nil {
			return res, err
		}
		mode := req.Mode
		if mode != ModeEnforce {
			mode = ModeObserve
		}
		binding = ResolvedBinding{Policy: policy, Mode: mode, Source: "preview"}
	}
	if binding.Policy == nil {
		return res, fmt.Errorf("no policy is bound")
	}
	res.Audited = true
	res.BindingSource, res.PolicyKey, res.PolicyRevision, res.Mode = binding.Source, binding.Policy.Key, binding.Policy.Revision, binding.Mode
	input, err := ExtractInput([]byte(req.Body), nil)
	if err != nil {
		return res, fmt.Errorf("body must be a JSON request: %w", err)
	}
	attempt := AttemptContext{Namespace: namespace, UserID: req.UserID, EffectiveGroup: group, ViaAuto: req.ViaAuto, Model: input.Model}
	ev := evaluation{
		snapshot: snap, binding: binding, attempt: attempt, input: input,
		store: readOnlyStore{Store: defaultStore},
		classifier: func(cfg ProviderConfig) Classifier {
			// This factory is reached only immediately before Classify. Local
			// blocks, skips and incomplete coverage never count as a model run.
			res.ModelRun = true
			return newProviderRuntime(cfg)
		},
		useCache: false, runModel: req.RunModel, now: time.Now(),
	}
	started := time.Now()
	d := evaluate(ctx, ev)
	d.LatencyMS = time.Since(started).Milliseconds()
	res.Decision = applyMode(d, binding.Mode)
	return res, nil
}
