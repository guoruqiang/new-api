package risk

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

//go:embed defaults/distill_rules.json
var defaultDistillJSON []byte

type DistillError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type DistillStructure struct {
	MinMaxTokens      int    `json:"min_max_tokens"`
	RequireNoTools    bool   `json:"require_no_tools"`
	RequireSingleTurn bool   `json:"require_single_turn"`
	ContestPattern    string `json:"contest_pattern"`
}

type DistillRules struct {
	Enabled       bool             `json:"enabled"`
	SkipOfficial  bool             `json:"skip_official"`
	SkipGPTModels bool             `json:"skip_gpt_models"`
	Error         DistillError     `json:"error"`
	Structure     DistillStructure `json:"structure"`
	HardNeedles   []string         `json:"hard_needles"`
	Needles       []string         `json:"needles"`
	Patterns      []string         `json:"patterns"`
	Fingerprints  []string         `json:"fingerprints"`
}

type HardRule struct {
	Category string `json:"category"`
	Source   string `json:"source"`
	Enabled  bool   `json:"enabled"`
	Action   string `json:"action"`
}

// UnmarshalJSON makes an omitted enabled flag true without losing false.
func (r *HardRule) UnmarshalJSON(data []byte) error {
	type hardRuleJSON HardRule
	value := hardRuleJSON{Enabled: true}
	if err := common.Unmarshal(data, &value); err != nil {
		return err
	}
	*r = HardRule(value)
	return nil
}

type HardRegexConfig struct {
	Enabled        bool       `json:"enabled"`
	StripReminders bool       `json:"strip_reminders"`
	ExpandBase64   bool       `json:"expand_base64"`
	Rules          []HardRule `json:"rules"`
	Patterns       []string   `json:"patterns"`
}

const (
	BlockSubjectUser       = "user"
	BlockSubjectUserDevice = "user_device"
	FailActionPass         = "pass"
	FailActionBlock        = "block"
)

type RefusalConfig struct {
	Enabled             bool   `json:"enabled"`
	SimilarityEnabled   bool   `json:"similarity_enabled"`
	Similarity          int    `json:"similarity"`
	SubjectBlockEnabled bool   `json:"subject_block_enabled"`
	BlockSubject        string `json:"block_subject"`
	BlockTTLMinutes     int    `json:"block_ttl_minutes"`
	// A subject is blocked once a user reaches BlockAfterHits confirmed hits
	// within BlockWindowMinutes; a single hit only stops its own request.
	BlockAfterHits       int `json:"block_after_hits"`
	BlockWindowMinutes   int `json:"block_window_minutes"`
	MemoryTTLDays        int `json:"memory_ttl_days"`
	MaxSimilarCandidates int `json:"max_similar_candidates"`
}

type ModelConfig struct {
	Enabled           bool       `json:"enabled"`
	ProviderID        int        `json:"provider_id"`
	Model             string     `json:"model"`
	Questions         []Question `json:"questions"`
	CustomInstruction string     `json:"custom_instruction"`
	Threshold         float64    `json:"threshold"`
	NearMissBand      float64    `json:"near_miss_band"`
	FailAction        string     `json:"fail_action"`
	BudgetMS          int        `json:"budget_ms"`
	MaxAttempts       int        `json:"max_attempts"`
	DedupSec          int        `json:"dedup_sec"`
	MaxAuditChars     int        `json:"max_audit_chars"`
}

type EvidenceConfig struct {
	StorePreview bool `json:"store_preview"`
}

// PolicyPayload is one immutable policy revision as stored and edited.
type PolicyPayload struct {
	SkipGPTModels bool            `json:"skip_gpt_models"`
	Distill       DistillRules    `json:"distill"`
	HardRegex     HardRegexConfig `json:"hard_regex"`
	Refusal       RefusalConfig   `json:"refusal"`
	Model         ModelConfig     `json:"model"`
	Evidence      EvidenceConfig  `json:"evidence"`
}

// DefaultDistillRules returns the shipped vm2api distill rule set.
func DefaultDistillRules() DistillRules {
	var rules DistillRules
	if err := common.Unmarshal(defaultDistillJSON, &rules); err != nil {
		panic(fmt.Sprintf("risk: invalid embedded distill rules: %v", err))
	}
	return rules
}

// DefaultPolicyPayload is every layer at its vm2api default, with the model
// layer off until a provider is configured.
func DefaultPolicyPayload() PolicyPayload {
	return PolicyPayload{
		Distill: DefaultDistillRules(),
		HardRegex: HardRegexConfig{
			Enabled:        true,
			StripReminders: true,
			ExpandBase64:   true,
			Patterns:       []string{},
		},
		Refusal: RefusalConfig{
			Enabled:              true,
			SimilarityEnabled:    true,
			Similarity:           90,
			SubjectBlockEnabled:  true,
			BlockSubject:         BlockSubjectUser,
			BlockTTLMinutes:      30,
			BlockAfterHits:       3,
			BlockWindowMinutes:   10,
			MemoryTTLDays:        7,
			MaxSimilarCandidates: 2000,
		},
		Model: ModelConfig{
			Threshold:     0.5,
			NearMissBand:  0.08,
			FailAction:    FailActionPass,
			BudgetMS:      2000,
			MaxAttempts:   2,
			DedupSec:      60,
			MaxAuditChars: 16000,
		},
		Evidence: EvidenceConfig{StorePreview: true},
	}
}

// ParsePolicyPayload decodes a stored or submitted payload over the defaults,
// so omitted fields keep their default value.
func ParsePolicyPayload(raw []byte) (PolicyPayload, error) {
	payload := DefaultPolicyPayload()
	if len(raw) == 0 {
		return payload, nil
	}
	if err := common.Unmarshal(raw, &payload); err != nil {
		return PolicyPayload{}, err
	}
	NormalizePolicyPayload(&payload)
	return payload, nil
}

// effectiveAction is a rule's action. Rules saved before actions existed keep
// blocking unless they are a built-in rule, which follows its built-in action.
func effectiveAction(rule HardRule) string {
	if rule.Action != "" {
		return rule.Action
	}
	for _, b := range BuiltinHardRules {
		if b.Source == rule.Source && b.Category == rule.Category {
			return b.Action
		}
	}
	return RuleActionBlock
}

func cleanList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item)
		if text != "" && !slices.Contains(out, text) {
			out = append(out, text)
		}
	}
	return out
}

// NormalizePolicyPayload trims and de-duplicates list entries. It never
// changes a value that validation would reject.
func NormalizePolicyPayload(p *PolicyPayload) {
	p.Distill.HardNeedles = cleanList(p.Distill.HardNeedles)
	p.Distill.Needles = cleanList(p.Distill.Needles)
	hard := make(map[string]bool, len(p.Distill.HardNeedles))
	for _, n := range p.Distill.HardNeedles {
		hard[strings.ToLower(n)] = true
	}
	p.Distill.Needles = slices.DeleteFunc(p.Distill.Needles, func(n string) bool { return hard[strings.ToLower(n)] })
	p.Distill.Patterns = cleanList(p.Distill.Patterns)
	p.Distill.Fingerprints = cleanList(p.Distill.Fingerprints)
	p.Distill.Error.Message = strings.TrimSpace(p.Distill.Error.Message)
	p.Distill.Structure.ContestPattern = strings.TrimSpace(p.Distill.Structure.ContestPattern)
	builtin := make(map[string]bool, len(BuiltinHardRules))
	for _, r := range BuiltinHardRules {
		builtin[r.Source] = true
	}
	p.HardRegex.Patterns = slices.DeleteFunc(cleanList(p.HardRegex.Patterns), func(s string) bool { return builtin[s] })
	if p.HardRegex.Rules != nil {
		seen := make(map[string]bool, len(p.HardRegex.Rules))
		rules := make([]HardRule, 0, len(p.HardRegex.Rules))
		for i := range p.HardRegex.Rules {
			rule := &p.HardRegex.Rules[i]
			rule.Source = strings.TrimSpace(rule.Source)
			// Upgrade only recognizable pre-action builtins. Explicit actions
			// and customized categories keep the administrator's saved pattern.
			if rule.Action == "" {
				switch {
				case rule.Category == "jailbreak" && rule.Source == `(?:解除|关闭|绕过|无视)(?:你的|所有|全部)?(?:安全|内容)?(?:限制|审查|过滤|策略)`:
					rule.Source = jailbreakControlPattern
				case rule.Category == "crack" && rule.Source == `(?:破解补丁|注册机|激活码生成器)`:
					rule.Source = crackToolPattern
				}
			}
			rule.Action = effectiveAction(*rule)
			if !seen[rule.Source] {
				rules = append(rules, *rule)
				seen[rule.Source] = true
			}
		}
		p.HardRegex.Rules = rules
		if len(p.HardRegex.Rules) == 0 {
			p.HardRegex.Rules = nil
		}
	}
	p.Model.Model = strings.TrimSpace(p.Model.Model)
	p.Model.CustomInstruction = strings.TrimSpace(p.Model.CustomInstruction)
	if slices.Contains(legacyQuestionInstructions["safety"], p.Model.CustomInstruction) {
		p.Model.CustomInstruction = ""
	}
	for i := range p.Model.Questions {
		q := &p.Model.Questions[i]
		q.ID = strings.ToLower(strings.TrimSpace(q.ID))
		q.Label = strings.TrimSpace(q.Label)
		q.Summary = strings.TrimSpace(q.Summary)
		q.Instructions = strings.TrimSpace(q.Instructions)
		q.Builtin = builtinQuestionIDs()[q.ID]
		if slices.Contains(legacyQuestionInstructions[q.ID], q.Instructions) {
			for _, builtin := range BuiltinQuestions {
				if builtin.ID == q.ID {
					q.Instructions = builtin.Instructions
					break
				}
			}
		}
		if q.Label == "" {
			q.Label = q.ID
		}
	}
}

func checkPattern(field, source string, problems *[]*common.Message) {
	if len([]rune(source)) > patternMaxLen {
		*problems = append(*problems, common.NewMessage("{{field}}: pattern longer than {{max}} characters", map[string]any{"field": field, "max": patternMaxLen}))
		return
	}
	if _, err := compileCaseless(source); err != nil {
		*problems = append(*problems, common.NewMessage("{{field}}: invalid RE2 pattern {{pattern}}", map[string]any{"field": field, "pattern": truncateRunes(source, 80)}))
	}
}

func checkRange(problems *[]*common.Message, field string, value, low, high int) {
	if value < low || value > high {
		*problems = append(*problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": field, "min": low, "max": high}))
	}
}

// ValidatePolicyPayload returns every problem at once, in English.
func ValidatePolicyPayload(p PolicyPayload) []*common.Message {
	problems := make([]*common.Message, 0)
	d := p.Distill
	if len(d.HardNeedles) > 200 || len(d.Needles) > 200 || len(d.Fingerprints) > 200 {
		problems = append(problems, common.NewMessage("Distill needles and fingerprints allow at most 200 entries each"))
	}
	for _, list := range []struct {
		field   string
		entries []string
	}{{"distill.hard_needles", d.HardNeedles}, {"distill.needles", d.Needles}, {"distill.fingerprints", d.Fingerprints}} {
		for _, entry := range list.entries {
			if len([]rune(entry)) > patternMaxLen {
				problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} characters", map[string]any{"field": list.field, "max": patternMaxLen}))
				break
			}
		}
	}
	if len(d.Patterns) > hardPatternMax {
		problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} entries", map[string]any{"field": "distill.patterns", "max": hardPatternMax}))
	}
	for _, source := range d.Patterns {
		checkPattern("distill.patterns", source, &problems)
	}
	checkRange(&problems, "distill.error.status", d.Error.Status, 400, 599)
	if len([]rune(d.Error.Message)) > 200 {
		problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} characters", map[string]any{"field": "distill.error.message", "max": 200}))
	}
	if d.Structure.MinMaxTokens <= 0 {
		problems = append(problems, common.NewMessage("{{field}} must be positive", map[string]any{"field": "distill.structure.min_max_tokens"}))
	}
	if d.Structure.ContestPattern != "" {
		checkPattern("distill.structure.contest_pattern", d.Structure.ContestPattern, &problems)
	}

	h := p.HardRegex
	if len(h.Rules) > hardRuleMax {
		problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} entries", map[string]any{"field": "hard_regex.rules", "max": hardRuleMax}))
	}
	for _, rule := range h.Rules {
		if !slices.Contains(HardRuleCategories, rule.Category) {
			problems = append(problems, common.NewMessage("Unknown risk rule category {{category}}", map[string]any{"category": rule.Category}))
		}
		if rule.Action != "" && rule.Action != RuleActionBlock && rule.Action != RuleActionReview {
			problems = append(problems, common.NewMessage("Risk rule action must be block or review"))
		}
		if rule.Source == "" {
			problems = append(problems, common.NewMessage("Every risk rule needs a pattern"))
			continue
		}
		checkPattern("hard_regex.rules", rule.Source, &problems)
	}
	if len(h.Patterns) > hardPatternMax {
		problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} entries", map[string]any{"field": "hard_regex.patterns", "max": hardPatternMax}))
	}
	for _, source := range h.Patterns {
		checkPattern("hard_regex.patterns", source, &problems)
	}

	r := p.Refusal
	if !slices.Contains([]int{80, 85, 90, 95}, r.Similarity) {
		problems = append(problems, common.NewMessage("Refusal similarity must be 80, 85, 90 or 95"))
	}
	if r.BlockSubject != BlockSubjectUser && r.BlockSubject != BlockSubjectUserDevice {
		problems = append(problems, common.NewMessage("Block subject must be user or user_device"))
	}
	checkRange(&problems, "refusal.block_ttl_minutes", r.BlockTTLMinutes, 0, 525600)
	checkRange(&problems, "refusal.block_after_hits", r.BlockAfterHits, 1, 20)
	checkRange(&problems, "refusal.block_window_minutes", r.BlockWindowMinutes, 1, 1440)
	checkRange(&problems, "refusal.memory_ttl_days", r.MemoryTTLDays, 0, 3650)
	checkRange(&problems, "refusal.max_similar_candidates", r.MaxSimilarCandidates, 1, 10000)

	m := p.Model
	if m.Enabled && m.ProviderID <= 0 {
		problems = append(problems, common.NewMessage("A provider is required when the model layer is enabled"))
	}
	if math.IsNaN(m.Threshold) || m.Threshold < 0 || m.Threshold > 1 {
		problems = append(problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": "model.threshold", "min": 0, "max": 1}))
	}
	if math.IsNaN(m.NearMissBand) || m.NearMissBand < 0 || m.NearMissBand > 1 {
		problems = append(problems, common.NewMessage("{{field}} must be between {{min}} and {{max}}", map[string]any{"field": "model.near_miss_band", "min": 0, "max": 1}))
	}
	if m.FailAction != FailActionPass && m.FailAction != FailActionBlock {
		problems = append(problems, common.NewMessage("Model failure action must be pass or block"))
	}
	checkRange(&problems, "model.budget_ms", m.BudgetMS, 200, 8000)
	checkRange(&problems, "model.max_attempts", m.MaxAttempts, 1, 10)
	checkRange(&problems, "model.dedup_sec", m.DedupSec, 0, 3600)
	checkRange(&problems, "model.max_audit_chars", m.MaxAuditChars, 256, 64000)
	if len([]rune(m.CustomInstruction)) > 500 {
		problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} characters", map[string]any{"field": "model.custom_instruction", "max": 500}))
	}
	if len(m.Questions) > questionMax {
		problems = append(problems, common.NewMessage("{{field}} allows at most {{max}} entries", map[string]any{"field": "model.questions", "max": questionMax}))
	}
	seen := make(map[string]bool, len(m.Questions))
	for _, q := range m.Questions {
		if !questionIDPattern.MatchString(q.ID) || q.ID == "custom" || seen[q.ID] {
			problems = append(problems, common.NewMessage("Question ID {{id}} must be unique, lowercase, start with a letter and not be custom", map[string]any{"id": q.ID}))
		}
		seen[q.ID] = true
		if q.Instructions == "" || len([]rune(q.Instructions)) > 500 {
			problems = append(problems, common.NewMessage("Question {{id}} needs instructions of at most 500 characters", map[string]any{"id": q.ID}))
		}
	}
	return problems
}

// CompiledPolicy is a policy revision with its patterns compiled once.
type CompiledPolicy struct {
	Key         string
	Revision    int
	Fingerprint string
	Payload     PolicyPayload

	distillPatterns []*regexp.Regexp
	contest         *regexp.Regexp
	hardRules       []compiledRule
	asked           []askedQuestion
}

// PolicyFingerprint hashes the normalized payload.
func PolicyFingerprint(p PolicyPayload) string {
	encoded, _ := common.Marshal(p)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// CompilePolicy validates and compiles a payload for the request path.
func CompilePolicy(key string, revision int, p PolicyPayload) (*CompiledPolicy, error) {
	if problems := ValidatePolicyPayload(p); len(problems) > 0 {
		return nil, fmt.Errorf("policy %s@%d is invalid: %w", key, revision, problems[0])
	}
	cp := &CompiledPolicy{Key: key, Revision: revision, Fingerprint: PolicyFingerprint(p), Payload: p, asked: askedQuestions(p.Model)}
	for _, source := range p.Distill.Patterns {
		re, err := compileCaseless(source)
		if err != nil {
			return nil, err
		}
		cp.distillPatterns = append(cp.distillPatterns, re)
	}
	if p.Distill.Structure.ContestPattern != "" {
		re, err := compileCaseless(p.Distill.Structure.ContestPattern)
		if err != nil {
			return nil, err
		}
		cp.contest = re
	}
	rules := p.HardRegex.Rules
	if len(rules) == 0 {
		rules = BuiltinHardRules
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		re, err := compileCaseless(rule.Source)
		if err != nil {
			return nil, err
		}
		cp.hardRules = append(cp.hardRules, compiledRule{category: rule.Category, source: rule.Source, action: effectiveAction(rule), re: re})
	}
	for _, source := range p.HardRegex.Patterns {
		re, err := compileCaseless(source)
		if err != nil {
			return nil, err
		}
		cp.hardRules = append(cp.hardRules, compiledRule{category: "custom", source: source, action: RuleActionBlock, re: re})
	}
	return cp, nil
}
