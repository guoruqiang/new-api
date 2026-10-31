// Package risk audits relay requests per routing group: keyword rules and
// distill signals select requests for a decision model, whose violations are
// rejected, scored against the user and kept as evidence for review.
package risk

import "github.com/QuantumNous/new-api/relaykit/types"

const (
	ModeObserve = "observe"
	ModeEnforce = "enforce"
)

const (
	LayerGate    = "gate"
	LayerKeyword = "keyword"
	LayerModel   = "model"
)

const (
	OutcomePass       = "pass"
	OutcomeBlock      = "block"
	OutcomeWouldBlock = "would_block"
	OutcomeErrorBlock = "error_block"
	OutcomeErrorPass  = "error_pass"
	OutcomeExempt     = "exempt"
	OutcomeUncovered  = "uncovered"
)

const (
	ReasonNoHit          = "no_hit"
	ReasonModelSafe      = "model_safe"
	ReasonModelViolation = "model_violation"
	ReasonModelDisabled  = "model_disabled"
	ReasonUnavailable    = "unavailable"
	ReasonUnreadable     = "unreadable"
	ReasonExemptGroup    = "exempt_group"
	ReasonUncovered      = "uncovered"
	ReasonGPTSkipped     = "gpt_skipped"
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

// ReservedAutoNamespace isolates auto requests whose concrete group cannot be
// resolved; it never names a real group.
const ReservedAutoNamespace = "auto"

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

// groupLabel names the attempt's group in statistics and violation records;
// an auto request whose group could not be resolved is labelled "auto".
func (a AttemptContext) groupLabel() string {
	if a.EffectiveGroup == "" && a.ViaAuto {
		return "auto"
	}
	return a.EffectiveGroup
}

// Input is the text view of one request body. Attachments are never read.
type Input struct {
	Raw          []byte
	ContentHash  string
	Model        string
	Systems      []string
	LatestUser   string
	MessageCount int
	ToolCount    int
	MaxTokens    *int
	Thinking     ThinkingRequest
	Official     bool
}

// ThinkingRequest is the reasoning shape of a request, for distill harvests.
type ThinkingRequest struct {
	Type            string
	Display         string
	BudgetTokens    int
	ReasoningEffort string
	N               int
}

// Decision is the outcome of one audited attempt.
type Decision struct {
	Outcome        string             `json:"outcome"`
	Layer          string             `json:"layer"`
	Reason         string             `json:"reason"`
	Category       string             `json:"category"`
	Keyword        string             `json:"keyword"`
	Rule           string             `json:"rule"`
	Scores         map[string]float64 `json:"scores,omitempty"`
	Code           types.ErrorCode    `json:"code,omitempty"`
	PolicyKey      string             `json:"policy_key"`
	PolicyRevision int                `json:"policy_revision"`
	ConfigRevision int64              `json:"config_revision"`
	ProviderModel  string             `json:"provider_model,omitempty"`
	LatencyMS      int64              `json:"latency_ms"`
}

// Blocks reports whether the decision stops the request in enforce mode.
func (d Decision) Blocks() bool {
	return d.Outcome == OutcomeBlock || d.Outcome == OutcomeErrorBlock
}

// Violates reports whether the decision model judged the request violating.
func (d Decision) Violates() bool {
	return d.Outcome == OutcomeBlock || d.Outcome == OutcomeWouldBlock
}
