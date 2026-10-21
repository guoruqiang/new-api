// Package risk implements the per-group request risk audit ported from vm2api:
// distill detection, hard regex rules, upstream refusal memory with subject
// blocks, and a systemone decision model, scoped by the routing group of each
// upstream attempt.
package risk

import "github.com/QuantumNous/new-api/relaykit/types"

const (
	ModeObserve = "observe"
	ModeEnforce = "enforce"
)

const (
	LayerGate      = "gate"
	LayerDistill   = "distill"
	LayerHardRegex = "hard_regex"
	LayerRefusal   = "refusal"
	LayerModel     = "model"
	LayerUpstream  = "upstream"
)

const (
	OutcomePass            = "pass"
	OutcomeBlock           = "block"
	OutcomeWouldBlock      = "would_block"
	OutcomeErrorBlock      = "error_block"
	OutcomeErrorPass       = "error_pass"
	OutcomeExempt          = "exempt"
	OutcomeUncovered       = "uncovered"
	OutcomeUpstreamRefusal = "upstream_refusal"
	OutcomeWouldStop       = "would_stop"
)

const (
	ReasonModelSafe              = "model_safe"
	ReasonModelReplay            = "model_replay"
	ReasonRegexMiss              = "regex_miss"
	ReasonFailOpen               = "fail_open"
	ReasonCoverageIncompletePass = "coverage_incomplete_pass"
	ReasonUnchecked              = "unchecked"
	ReasonExemptGroup            = "exempt_group"
	ReasonUncovered              = "uncovered"
	ReasonExact                  = "exact"
	ReasonSimilar                = "similar"
	ReasonSubject                = "subject"
	ReasonUnavailable            = "unavailable"
	ReasonCoverageIncomplete     = "coverage_incomplete"
	// A review-tier rule hit that the decision model judged safe, or that no
	// decision model could confirm; both pass and are logged for tuning.
	ReasonRuleCleared     = "rule_cleared"
	ReasonRuleUnconfirmed = "rule_unconfirmed"
)

const (
	GroupSourceUsingGroup      = "using_group"
	GroupSourceAutoGroup       = "auto_group"
	GroupSourceRetrySelect     = "retry_select"
	GroupSourcePinned          = "pinned"
	GroupSourcePinnedCandidate = "pinned_candidate"
	GroupSourceAutoUnresolved  = "auto_unresolved"
	GroupSourceWSSelect        = "ws_select"
	GroupSourceWSLocked        = "ws_locked"
)

const (
	RouteChatCompletions = "chat_completions"
	RouteCompletions     = "completions"
	RouteClaudeMessages  = "claude_messages"
	RouteResponses       = "responses"
	RouteResponsesWS     = "responses_ws"
	RoutePlayground      = "playground"
)

const (
	CoverageComplete   = "complete"
	CoverageTailWindow = "tail_window"
	CoverageIncomplete = "incomplete"
)

// ReservedAutoNamespace isolates auto requests whose concrete group cannot be
// resolved; it never names a real group.
const ReservedAutoNamespace = "auto"

const (
	SubjectUser   = "user"
	SubjectDevice = "device"
)

// AttemptContext describes one upstream attempt that the audit judges.
type AttemptContext struct {
	RequestID      string
	Index          int
	UserID         int
	TokenID        int
	UserGroup      string
	TokenGroup     string
	EffectiveGroup string
	Namespace      string
	GroupSource    string
	ViaAuto        bool
	ChannelID      int
	ChannelType    int
	Model          string
	Route          string
}

// Input is the role-preserving audit view of one request body.
type Input struct {
	Digest       string
	Model        string
	Systems      []string
	DistillUsers []string
	UserTurns    []string
	UserAuthored []string
	// Model-only tails preserve the latest text after extraction bounds apply,
	// without changing the local-rule and refusal-fingerprint input above.
	modelSystems    []string
	modelUsers      []string
	modelAssistants []string
	modelTools      []string
	// Assistants and RecentToolOutputs are client-supplied context that only
	// the review tier of local rules scans; the latest tool results are the
	// only ones not already audited with an earlier request of the session.
	Assistants        []string
	RecentToolOutputs []string
	LatestUser        string
	ToolNames         []string
	MessageCount      int
	ToolCount         int
	MaxTokens         *int
	Thinking          ThinkingRequest
	DeviceID          string
	Official          bool
	HasMedia          bool
	RefsHistory       bool
	PromptTokenOnly   bool
	// Truncated reports that older text was left out to keep the audit bounded.
	// This is a tail-window policy choice, not a decision service failure.
	Truncated bool
}

// ThinkingRequest holds the reasoning knobs the distill structure check reads.
type ThinkingRequest struct {
	Type            string
	Display         string
	BudgetTokens    int
	ReasoningEffort string
	N               int
}

// Decision is the result of evaluating one attempt.
type Decision struct {
	Outcome        string             `json:"outcome"`
	Layer          string             `json:"layer"`
	Reason         string             `json:"reason"`
	Category       string             `json:"category"`
	Keyword        string             `json:"keyword"`
	Rule           string             `json:"rule"`
	Scores         map[string]float64 `json:"scores,omitempty"`
	Fingerprint    string             `json:"fingerprint"`
	Signature      string             `json:"-"`
	BanSubject     bool               `json:"ban_subject"`
	Coverage       string             `json:"coverage"`
	Status         int                `json:"status,omitempty"`
	Code           types.ErrorCode    `json:"code,omitempty"`
	Message        string             `json:"message,omitempty"`
	PolicyKey      string             `json:"policy_key"`
	PolicyRevision int                `json:"policy_revision"`
	ConfigRevision int64              `json:"config_revision"`
	ProviderModel  string             `json:"provider_model,omitempty"`
	LatencyMS      int64              `json:"latency_ms"`
	Preview        string             `json:"preview,omitempty"`
	BlockID        int                `json:"block_id,omitempty"`
	RefusalID      int                `json:"refusal_id,omitempty"`
	Similarity     float64            `json:"similarity,omitempty"`
	// Reviewed marks a decision that a review-tier rule hit sent to the
	// decision model; such decisions are logged even when they pass.
	Reviewed bool `json:"reviewed,omitempty"`
	// ReviewEvidence contains the bounded, role-labelled text used by the
	// detector. It is stored only when the policy explicitly allows it.
	ReviewEvidence string `json:"-"`
}

// Blocks reports whether the decision stops the request in enforce mode.
func (d Decision) Blocks() bool {
	return d.Outcome == OutcomeBlock || d.Outcome == OutcomeErrorBlock
}
