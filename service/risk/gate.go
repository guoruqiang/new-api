package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// AttemptInput describes one attempt from the relay call site.
type AttemptInput struct {
	Phase       string
	SelectGroup string
	LockedGroup string
	ChannelID   int
	ChannelType int
	// Body is the raw request JSON; when nil the HTTP body storage is read.
	Body []byte
}

const requestStateKey = "risk_request_state"

// requestState is per gin context: one HTTP request or one WebSocket
// response.create.
type requestState struct {
	mu           sync.Mutex
	attempts     int
	countedFirst bool
	inputDone    bool
	input        *Input
	inputErr     error
	reuse        map[string]Decision
	firstDone    bool
	firstGroup   string
	firstErr     *types.NewAPIError
}

func requestStateFor(c *gin.Context) *requestState {
	if value, ok := c.Get(requestStateKey); ok {
		return value.(*requestState)
	}
	st := &requestState{reuse: map[string]Decision{}}
	c.Set(requestStateKey, st)
	return st
}

var classifierFor = func(cfg ProviderConfig) Classifier { return runtimeFor(cfg) }

type evaluation struct {
	snapshot   *Snapshot
	binding    ResolvedBinding
	input      *Input
	model      string
	classifier func(ProviderConfig) Classifier
	useCache   bool
	runModel   bool
}

// verdictKey identifies one model judgment: the same document under the same
// policy, endpoint and model always gets the same verdict.
func verdictKey(policy *CompiledPolicy, provider ProviderConfig, modelName, document string) string {
	encoded, _ := common.Marshal([]string{
		policy.Fingerprint,
		providerFingerprint(ProviderConfig{Type: provider.Type, BaseURL: provider.BaseURL, KeyLines: endpointLines(provider)}),
		modelName, document,
	})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// endpointLines keeps only the URL part of each key line so the cache key
// tracks endpoints without holding secrets.
func endpointLines(cfg ProviderConfig) []string {
	lines := make([]string, 0, len(cfg.KeyLines))
	for _, line := range cfg.KeyLines {
		if m := keyLinePattern.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			lines = append(lines, m[1])
		}
	}
	return lines
}

// evaluate scans the system and latest user text for keywords. Without a hit
// the request passes untouched; with one, the decision model judges it.
func evaluate(ctx context.Context, ev evaluation) Decision {
	p := ev.binding.Policy
	payload := p.Payload
	d := Decision{
		Outcome: OutcomePass, Layer: LayerGate,
		PolicyKey: p.Key, PolicyRevision: p.Revision, ConfigRevision: ev.snapshot.ConfigRevision,
	}
	gpt := IsGPTSeriesModel(ev.model)
	if payload.SkipGPTModels && gpt {
		d.Reason = ReasonGPTSkipped
		return d
	}
	texts := ev.input.auditTexts(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64)
	scan := texts.scan()
	allow := func(category string) bool {
		if category == "distill" {
			return payload.Distill.Enabled && !(payload.Distill.SkipGPTModels && gpt)
		}
		return payload.HardRegex.Enabled
	}
	hit, ok := matchRules(scan, p.rules, allow)
	if !ok {
		hit, ok = detectDistill(ev.input, p, scan)
	}
	if !ok {
		d.Reason = ReasonNoHit
		return d
	}
	d.Layer, d.Category, d.Keyword, d.Rule = LayerKeyword, hit.Category, hit.Keyword, hit.Rule

	m := payload.Model
	provider, configured := ev.snapshot.Providers[m.ProviderID]
	if !m.Enabled || !ev.runModel || !configured {
		d.Reason = ReasonModelDisabled
		return d
	}
	moderation := provider.Type == "openai"
	modelName := provider.ResolvedModel(m.Model)
	d.Layer, d.ProviderModel = LayerModel, modelName
	result, err := classify(ctx, ev, provider, modelName, texts.modelDocument(m.MaxAuditChars, hit, moderation))
	if err != nil {
		d.Outcome, d.Reason = OutcomeErrorPass, ReasonUnavailable
		if m.FailAction == FailActionBlock {
			d.Outcome = OutcomeErrorBlock
		}
		return d
	}
	if result.Model != "" {
		d.ProviderModel = result.Model
	}
	d.Scores = result.Scores
	worst, violated := worstHit(result, p.asked, m, moderation)
	if !violated {
		d.Reason = ReasonModelSafe
		return d
	}
	d.Outcome, d.Reason, d.Category = OutcomeBlock, ReasonModelViolation, worst.category
	return d
}

func classify(ctx context.Context, ev evaluation, provider ProviderConfig, modelName, document string) (ClassifyResult, error) {
	m := ev.binding.Policy.Payload.Model
	key := ""
	if ev.useCache && m.DedupSec > 0 {
		key = verdictKey(ev.binding.Policy, provider, modelName, document)
		if cached, found, _ := verdictCache.Get(key); found {
			return ClassifyResult{Scores: cached.scores, Model: cached.model}, nil
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(m.BudgetMS)*time.Millisecond)
	defer cancel()
	req := ClassifyRequest{State: document, Model: modelName, MaxAttempts: m.MaxAttempts}
	if provider.Type != "openai" {
		req.Questions = ev.binding.Policy.asked
	}
	result, err := ev.classifier(provider).Classify(callCtx, req)
	if err == nil && key != "" {
		verdictCache.SetWithTTL(key, cachedVerdict{scores: result.Scores, model: result.Model}, time.Duration(m.DedupSec)*time.Second)
	}
	return result, err
}

// applyMode turns blocking outcomes into observations in observe mode.
func applyMode(d Decision, mode string) Decision {
	if mode != ModeObserve {
		return d
	}
	switch d.Outcome {
	case OutcomeBlock:
		d.Outcome = OutcomeWouldBlock
	case OutcomeErrorBlock:
		d.Outcome = OutcomeErrorPass
	}
	return d
}

// recordViolation keeps the evidence, deducts the score and applies a ban.
// It runs only for model-confirmed violations, never on the passing path.
func recordViolation(attempt AttemptContext, mode string, d Decision, in *Input, score ScoreConfig) {
	body, size, truncated := EvidenceBody(in.Raw)
	scores, _ := common.Marshal(d.Scores)
	v := &model.RiskViolation{
		RequestId: attempt.RequestID, ContentHash: in.ContentHash, UserId: attempt.UserID, TokenId: attempt.TokenID,
		GroupName: attempt.groupLabel(), Namespace: attempt.Namespace, Mode: mode, Model: truncateRunes(attempt.Model, 128),
		Category: d.Category, Keyword: truncateRunes(d.Keyword, 160), Rule: truncateRunes(d.Rule, 255),
		ProviderModel: truncateRunes(d.ProviderModel, 120), Scores: string(scores),
		RequestBody: body, RequestBodyBytes: size, RequestBodyTruncated: truncated,
	}
	created, ban, err := model.RecordRiskViolation(v, score.Deduction(d.Category), score.AutoBan, score.EmailNotify, time.Now().Unix())
	if err != nil {
		common.SysError(common.LogText("risk violation write failed: %s", err.Error()))
		return
	}
	if !created {
		return
	}
	if ban {
		banned, err := service.SetUserEnabledForRisk(v.UserId, false)
		if err != nil {
			common.SysError(common.LogText("risk ban failed for user %d: %s", v.UserId, err.Error()))
		}
		if err != nil || !banned {
			v.Action = model.RiskActionBanSuggested
			if err := model.SetRiskViolationAction(v.Id, v.Action); err != nil {
				common.SysError(common.LogText("risk violation write failed: %s", err.Error()))
			}
		}
	}
	if v.NotificationStatus == "pending" {
		queueViolationNotice(*v)
	}
}

func counterKey(attempt AttemptContext, d Decision, isFirst bool, now time.Time) model.RiskCounterKey {
	first := 0
	if isFirst {
		first = 1
	}
	return model.RiskCounterKey{
		Bucket: now.Truncate(time.Hour).Unix(), Namespace: attempt.Namespace, GroupName: attempt.groupLabel(), Route: attempt.Route,
		Outcome: d.Outcome, Layer: d.Layer, Reason: d.Reason, IsFirst: first,
	}
}

// buildEvent records a keyword hit or a failure without any request text.
func buildEvent(attempt AttemptContext, d Decision, mode string, in *Input, now time.Time) *model.RiskEvent {
	scores := ""
	if len(d.Scores) > 0 {
		encoded, _ := common.Marshal(d.Scores)
		scores = string(encoded)
	}
	fingerprint := ""
	if in != nil {
		fingerprint = in.ContentHash
	}
	return &model.RiskEvent{
		RequestId: attempt.RequestID, Attempt: attempt.Index, CreatedAt: now.Unix(), UserId: attempt.UserID, TokenId: attempt.TokenID,
		UserGroup: attempt.UserGroup, TokenGroup: attempt.TokenGroup, EffectiveGroup: attempt.EffectiveGroup, Namespace: attempt.Namespace,
		GroupSource: attempt.GroupSource, ChannelId: attempt.ChannelID, Model: truncateRunes(attempt.Model, 128), Route: attempt.Route,
		Mode: mode, ConfigRevision: d.ConfigRevision, PolicyKey: d.PolicyKey, PolicyRevision: d.PolicyRevision,
		Layer: d.Layer, Outcome: d.Outcome, Reason: d.Reason, Category: d.Category, Keyword: truncateRunes(d.Keyword, 64),
		Rule: truncateRunes(d.Rule, 160), Scores: scores, ProviderModel: d.ProviderModel, LatencyMs: int(d.LatencyMS),
		ErrorCode: string(d.Code), Fingerprint: fingerprint,
	}
}

func appendPolicyEvent(c *gin.Context, attempt AttemptContext, d Decision) {
	service.RequestPolicy(c).AddEvent(service.PolicyEvent{
		ChannelID:   attempt.ChannelID,
		Group:       attempt.EffectiveGroup,
		Rule:        "risk:" + d.Layer,
		ErrorCode:   string(d.Code),
		ErrorSource: "risk",
		Decision:    service.PolicyDecision{Action: "risk_" + d.Outcome, Reason: d.Reason, Source: "risk"},
		Health:      "unchanged",
	})
}

func attemptRoute(c *gin.Context, info *relaycommon.RelayInfo, in AttemptInput) string {
	if in.Phase == PhaseWSSelect || in.Phase == PhaseWSLocked {
		if CoveredRoute(c, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, in.ChannelType) == "" {
			return ""
		}
		return RouteResponsesWS
	}
	return CoveredRoute(c, info.RelayFormat, info.RelayMode, in.ChannelType)
}

func groupInput(in AttemptInput, info *relaycommon.RelayInfo) GroupResolveInput {
	return GroupResolveInput{Phase: in.Phase, SelectGroup: in.SelectGroup, LockedGroup: in.LockedGroup, ChannelID: in.ChannelID, Model: info.OriginModelName}
}

var recorder = func() Recorder { return defaultRecorder }

// CheckAttempt audits one upstream attempt. It returns a risk error only when
// an enforce-mode decision blocks; everything else lets the request continue.
// The passing path reads no database: configuration comes from the in-memory
// snapshot and statistics are written asynchronously.
func CheckAttempt(c *gin.Context, info *relaycommon.RelayInfo, in AttemptInput) *types.NewAPIError {
	snap := current.Load()
	if snap == nil || !snap.Enabled {
		return nil
	}
	group, source, viaAuto := ResolveAttemptGroup(c, groupInput(in, info))
	st := requestStateFor(c)
	st.mu.Lock()
	defer st.mu.Unlock()

	if in.Phase == PhaseHTTPLoop && st.firstDone {
		if group == st.firstGroup {
			return st.firstErr
		}
		logger.LogWarn(c, common.LogText("risk group_mismatch: pre-billing group %q, first attempt group %q", st.firstGroup, group))
	}

	now := time.Now()
	isFirst := !st.countedFirst
	st.countedFirst = true
	route := attemptRoute(c, info, in)
	if route == "" {
		if isFirst {
			recorder().Record(Record{Counter: model.RiskCounterKey{Bucket: now.Truncate(time.Hour).Unix(), Outcome: OutcomeUncovered, Layer: LayerGate, Reason: ReasonUncovered, IsFirst: 1}})
		}
		return nil
	}
	if !snap.Available || !snap.Fresh(now) {
		return configUnavailableError(c)
	}

	attempt := AttemptContext{
		RequestID: info.RequestId, Index: st.attempts, UserID: info.UserId, TokenID: info.TokenId,
		UserGroup: info.UserGroup, TokenGroup: common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		EffectiveGroup: group, Namespace: snap.Scope.NamespaceFor(group), GroupSource: source, ViaAuto: viaAuto,
		ChannelID: in.ChannelID, ChannelType: in.ChannelType, Model: info.OriginModelName, Route: route,
	}
	st.attempts++
	finish := func(err *types.NewAPIError) *types.NewAPIError {
		if in.Phase == PhaseHTTPFirst {
			st.firstDone, st.firstGroup, st.firstErr = true, group, err
		}
		return err
	}

	if IsExempt(snap.Scope, group, viaAuto) {
		d := Decision{Outcome: OutcomeExempt, Layer: LayerGate, Reason: ReasonExemptGroup}
		recorder().Record(Record{Counter: counterKey(attempt, d, isFirst, now)})
		appendPolicyEvent(c, attempt, d)
		return finish(nil)
	}
	binding := snap.BindingFor(attempt.Namespace)

	if !st.inputDone {
		body := in.Body
		if body == nil {
			storage, err := common.GetBodyStorage(c)
			if err == nil {
				body, err = storage.Bytes()
			}
			st.inputErr = err
		}
		if st.inputErr == nil {
			var header map[string][]string
			if c.Request != nil {
				header = c.Request.Header
			}
			st.input, st.inputErr = ExtractInput(body, header)
		}
		st.inputDone = true
	}

	var d Decision
	if st.inputErr != nil {
		d = Decision{Outcome: OutcomeErrorPass, Layer: LayerGate, Reason: ReasonUnreadable,
			PolicyKey: binding.Policy.Key, PolicyRevision: binding.Policy.Revision, ConfigRevision: snap.ConfigRevision}
		if binding.Policy.Payload.Model.FailAction == FailActionBlock {
			d.Outcome = OutcomeErrorBlock
		}
		d = applyMode(d, binding.Mode)
	} else {
		provider := snap.Providers[binding.Policy.Payload.Model.ProviderID]
		key := fmt.Sprintf("%s|%d|%s|%s|%s", attempt.Namespace, snap.ConfigRevision, binding.Policy.Fingerprint, providerFingerprint(provider), binding.Mode)
		prev, reused := st.reuse[key]
		if reused {
			d = prev
		} else {
			ctx := context.Background()
			if c.Request != nil {
				ctx = c.Request.Context()
			}
			started := time.Now()
			d = evaluate(ctx, evaluation{snapshot: snap, binding: binding, input: st.input, model: info.OriginModelName,
				classifier: classifierFor, useCache: true, runModel: true})
			d.LatencyMS = time.Since(started).Milliseconds()
			d = applyMode(d, binding.Mode)
			st.reuse[key] = d
			if d.Violates() {
				recordViolation(attempt, binding.Mode, d, st.input, snap.Score)
			}
		}
	}
	var riskErr *types.NewAPIError
	if binding.Mode == ModeEnforce && d.Blocks() {
		riskErr = decisionError(c, d)
		d.Code = riskErr.GetErrorCode()
	}
	rec := Record{Counter: counterKey(attempt, d, isFirst, now)}
	if d.Reason != ReasonNoHit && d.Reason != ReasonGPTSkipped {
		rec.Event = buildEvent(attempt, d, binding.Mode, st.input, now)
	}
	recorder().Record(rec)
	appendPolicyEvent(c, attempt, d)
	return finish(riskErr)
}
