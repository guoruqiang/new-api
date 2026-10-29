package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
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

const ReasonGPTSkipped = "gpt_skipped"
const ReasonStoreUnavailable = "store_unavailable"

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

type auditedAttempt struct {
	attempt        AttemptContext
	binding        ResolvedBinding
	input          *Input
	configRevision int64
}

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
	last         *auditedAttempt
	learned      map[int]bool
}

func requestStateFor(c *gin.Context) *requestState {
	if value, ok := c.Get(requestStateKey); ok {
		return value.(*requestState)
	}
	st := &requestState{reuse: map[string]Decision{}, learned: map[int]bool{}}
	c.Set(requestStateKey, st)
	return st
}

func existingRequestState(c *gin.Context) *requestState {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(requestStateKey); ok {
		return value.(*requestState)
	}
	return nil
}

var classifierFor = func(cfg ProviderConfig) Classifier { return runtimeFor(cfg) }

var attemptTime = time.Now

type evaluation struct {
	snapshot   *Snapshot
	binding    ResolvedBinding
	attempt    AttemptContext
	input      *Input
	store      Store
	classifier func(ProviderConfig) Classifier
	useCache   bool
	runModel   bool
	now        time.Time
}

func hashDevice(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

func blockDecision(base Decision, layer, reason, category, keyword, rule string) Decision {
	d := base
	d.Outcome = OutcomeBlock
	d.Layer = layer
	d.Reason = reason
	d.Category = category
	d.Keyword = truncateRunes(keyword, 40)
	d.Rule = truncateRunes(rule, 120)
	d.BanSubject = false
	return d
}

func failureDecision(base Decision, payload PolicyPayload, layer, reason, detail string) Decision {
	d := base
	d.Layer = layer
	d.Keyword = truncateRunes(detail, 40)
	if reason == ReasonCoverageIncomplete {
		d.Coverage = CoverageIncomplete
	}
	if payload.Model.FailAction == FailActionBlock {
		d.Outcome = OutcomeErrorBlock
		d.Reason = reason
		return d
	}
	d.Outcome = OutcomeErrorPass
	d.Reason = ReasonFailOpen
	if reason == ReasonCoverageIncomplete {
		d.Reason = ReasonCoverageIncompletePass
	}
	return d
}

func storeFailure(base Decision, err error) Decision {
	d := base
	d.Outcome = OutcomeErrorBlock
	d.Layer = LayerRefusal
	d.Reason = ReasonStoreUnavailable
	d.Keyword = truncateRunes(err.Error(), 40)
	return d
}

func verdictKey(ev evaluation, provider ProviderConfig, modelName, document string) string {
	p := ev.binding.Policy.Payload.Model
	asked := make([]string, 0, len(ev.binding.Policy.asked))
	for _, q := range ev.binding.Policy.asked {
		asked = append(asked, q.id+"="+q.instructions)
	}
	encoded, _ := common.Marshal(struct {
		Revision   int64    `json:"revision"`
		Namespace  string   `json:"ns"`
		User       int      `json:"u"`
		Token      int      `json:"token"`
		Provider   string   `json:"p"`
		Model      string   `json:"m"`
		Questions  []string `json:"q"`
		Threshold  float64  `json:"t"`
		Band       float64  `json:"b"`
		Preprocess string   `json:"pp"`
		Document   string   `json:"d"`
		Context    string   `json:"context"`
		Policy     string   `json:"policy"`
	}{
		ev.snapshot.ChangeRevision,
		ev.attempt.Namespace, ev.attempt.UserID, ev.attempt.TokenID,
		providerFingerprint(ProviderConfig{Type: provider.Type, BaseURL: provider.BaseURL, KeyLines: endpointLines(provider)}),
		modelName, asked, p.Threshold, p.NearMissBand,
		fmt.Sprintf("%d:%d:%t:%t", model.RiskJudgmentVersion, PreprocessVersion, ev.binding.Policy.Payload.HardRegex.StripReminders, ev.binding.Policy.Payload.HardRegex.ExpandBase64),
		document,
		ev.input.ContextDigest,
		ev.binding.Policy.Fingerprint,
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

// evaluate runs the four layers in vm2api's order and stops at the first
// hit. It may populate the model cache; commit applies persistent side effects.
func evaluate(ctx context.Context, ev evaluation) (d Decision) {
	p := ev.binding.Policy
	payload := p.Payload
	in := ev.input
	now := ev.now.Unix()
	d = Decision{
		Outcome: OutcomePass, Layer: LayerGate, Coverage: CoverageComplete,
		PolicyKey: p.Key, PolicyRevision: p.Revision, ConfigRevision: ev.snapshot.ConfigRevision,
	}
	defer func() {
		// Passing, unconfirmed and failed audits retain metadata only. Even
		// a matched keyword can contain user text, so do not persist it.
		if d.Outcome != OutcomeBlock || d.Layer != LayerModel && d.Layer != LayerHardRegex && d.Layer != LayerDistill {
			d.Preview, d.ReviewEvidence, d.Keyword = "", "", ""
		}
	}()
	if payload.Evidence.StorePreview {
		d.Preview = in.Preview()
	}
	if in.HasMedia || in.RefsHistory || in.PromptTokenOnly || in.Truncated {
		d.Coverage = CoverageIncomplete
	}
	gpt := IsGPTSeriesModel(ev.attempt.Model)
	if payload.SkipGPTModels && gpt {
		d.Reason = ReasonGPTSkipped
		return d
	}
	d.Fingerprint = Fingerprint(in)

	var review *hardHit
	points, screeningReason := 0, ""
	currentDoc, userIncomplete := in.CurrentUserDocument(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64)
	contextDoc, contextIncomplete := in.ContextDocument(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64)
	contextDoc, _ = tailRunes(contextDoc, 16000)
	distillInput := *in
	distillInput.LatestUser = currentDoc
	distillInput.Systems = []string{contextDoc}
	for _, hit := range detectDistill(&distillInput, p) {
		if hit.Review != nil {
			if review == nil {
				review = hit.Review
				points, screeningReason = 1, "distill_signal"
			}
			continue
		}
		points, screeningReason = 1, "distill_signal"
	}

	ruleIncomplete := userIncomplete || contextIncomplete
	if ruleIncomplete {
		d.Coverage = CoverageIncomplete
	}
	allow := func(category string) bool {
		if category == "distill" {
			return payload.Distill.Enabled && !(payload.Distill.SkipGPTModels && gpt)
		}
		return payload.HardRegex.Enabled
	}
	scanned := slices.ContainsFunc(p.hardRules, func(r compiledRule) bool { return allow(r.category) })
	// Legacy block-tier rules are signals too. Only the decision model may
	// establish a content violation.
	if hit, ok := matchHardRules(currentDoc, p.hardRules, allow); ok {
		review = &hit
		points, screeningReason = 3, "user_rule"
	}
	if review == nil {
		if hit, ok := matchHardRules(contextDoc, p.hardRules, allow); ok {
			review = &hit
			points, screeningReason = 1, "context_rule"
		}
	}
	var signal *hardHit
	if hit, ok := matchHardRules(currentDoc, p.signalRules, allow); ok {
		signal = &hit
		if points == 0 {
			points, screeningReason = 1, "keyword"
		}
	}

	if cfg := payload.Refusal; cfg.Enabled {
		ns := ev.attempt.Namespace

		rec, err := ev.store.ExactRefusal(ns, ev.attempt.UserID, d.Fingerprint, now)
		if err != nil {
			return storeFailure(d, err)
		}
		if rec != nil {
			d.RefusalID, d.Reviewed = rec.Id, true
			if points == 0 {
				points, screeningReason = 1, "exact_refusal"
			}
		}
		if cfg.SimilarityEnabled {
			if sig := Signature(in.SimilarityDocument()); sig != nil {
				d.Signature = EncodeSignature(sig)
				candidates, err := ev.store.RefusalCandidates(ns, ev.attempt.UserID, strings.ToLower(strings.TrimSpace(in.Model)), cfg.MaxSimilarCandidates, now)
				if err != nil {
					return storeFailure(d, err)
				}
				best, bestID := 0.0, 0
				for _, cand := range candidates {
					if score := Similarity(sig, DecodeSignature(cand.Signature)); score > best {
						best, bestID = score, cand.Id
					}
				}
				if bestID > 0 && best >= float64(cfg.Similarity)/100 {
					// Similarity is a review signal, not evidence that the new
					// request shares the earlier refusal's intent.
					d.Similarity = best
					d.Reviewed = true
					if points == 0 {
						points, screeningReason = 1, "similar_refusal"
					}
				}
			}
		}
	}

	m := payload.Model
	d.ScreeningScore, d.ScreeningReason = points, screeningReason
	if review != nil {
		d.Layer, d.Category, d.Keyword, d.Rule, d.Reviewed = LayerHardRegex, review.Category, review.Keyword, review.Rule, true
		if review.Category == "distill" {
			d.Layer = LayerDistill
		}
	}
	if !m.Enabled || !ev.runModel {
		switch {
		case d.Reviewed:
			d.Reason = ReasonRuleUnconfirmed
		case scanned:
			d.Reason = ReasonRegexMiss
		default:
			d.Reason = ReasonUnchecked
		}
		return d
	}
	if points == 0 {
		d.Reason = ReasonModelNotTriggered
		return d
	}
	if signal != nil && review == nil {
		d.Category = signal.Category
	}
	d.ModelTriggered = true
	provider, ok := ev.snapshot.Providers[m.ProviderID]
	if !ok || provider.BaseURL == "" && len(endpointLines(provider)) == 0 {
		return failureDecision(d, payload, LayerModel, ReasonUnavailable, "no provider")
	}
	documents, tailWindow, docIncomplete, reviewCovered := in.ModelDocuments(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64, m.MaxAuditChars, review)
	if payload.Evidence.StoreReviewEvidence {
		d.ReviewEvidence = strings.Join(documents, "\n\n--- audit document ---\n\n")
	}
	if ruleIncomplete || docIncomplete || in.HasMedia || in.RefsHistory || in.PromptTokenOnly {
		d.Coverage = CoverageIncomplete
	} else if tailWindow && len(documents) > 0 {
		d.Coverage = CoverageTailWindow
	}
	if len(documents) == 0 {
		d.Reason = ReasonUnchecked
		if review != nil {
			d.Reason = ReasonRuleUnconfirmed
		}
		return d
	}
	modelName := provider.ResolvedModel(m.Model)
	d.ProviderModel = modelName
	var failure error
	merged := map[string]float64{}
	allCached := true
	freshJudgments := true
	for _, v := range classifyDocuments(ctx, ev, provider, modelName, documents) {
		if v.err != nil {
			if failure == nil {
				failure = v.err
			}
			allCached = false
			continue
		}
		allCached = allCached && v.cached
		freshJudgments = freshJudgments && !v.cached
		for id, score := range v.scores {
			if current, seen := merged[id]; !seen || score < current {
				merged[id] = score
			}
		}
	}
	d.Scores = merged
	if hits := judgeScores(merged, p.asked, m.Threshold, m.NearMissBand); len(hits) > 0 {
		d.JudgmentSource = "model"
		d.JudgmentVersion = model.RiskJudgmentVersion
		d.Deduction = model.RiskViolationDeduction
		// Preserve each violated category's lowest safety score. Correlated
		// questions and multiple audit documents still form one incident.
		seen := make(map[string]bool, len(hits))
		for _, hit := range hits {
			category := "custom"
			for _, question := range p.asked {
				if question.id == hit.category {
					category = question.category
					break
				}
			}
			if !seen[category] {
				seen[category] = true
				d.Assessments = append(d.Assessments, model.RiskAssessment{Category: category, SafetyScore: hit.score, AppliedDeduction: model.RiskViolationDeduction})
			}
		}
		d.AutoConfirm = ev.attempt.TokenID > 0 && payload.Behavior.Enabled && payload.Behavior.AutoConfirmEnabled && failure == nil && freshJudgments &&
			reviewCovered && d.Coverage != CoverageIncomplete && hits[0].score < m.AutoConfirmThreshold &&
			payload.Evidence.StoreReviewEvidence && d.ReviewEvidence != "" && in.OriginalRequest != "" && !in.OriginalRequestTruncated
		category := d.Assessments[0].Category
		return blockDecision(d, LayerModel, "model", category, category, d.Rule)
	}
	if failure != nil {
		detail := failure.Error()
		if unavail, ok := failure.(*ErrModelUnavailable); ok {
			detail = unavail.Detail
		}
		return failureDecision(d, payload, LayerModel, ReasonUnavailable, detail)
	}
	d.Layer = LayerModel
	switch {
	case review != nil && !reviewCovered:
		d.Reason = ReasonRuleUnconfirmed
	case d.Reviewed:
		d.Reason = ReasonRuleCleared
	case allCached:
		d.Reason = ReasonModelReplay
	default:
		d.Reason = ReasonModelSafe
	}
	return d
}

type documentVerdict struct {
	scores map[string]float64
	cached bool
	err    error
}

// classifyDocuments judges the tail window and optional review excerpt within
// one deadline. Each document has its own verdict cache entry.
func classifyDocuments(ctx context.Context, ev evaluation, provider ProviderConfig, modelName string, documents []string) []documentVerdict {
	m := ev.binding.Policy.Payload.Model
	classifier := ev.classifier(provider)
	verdicts := make([]documentVerdict, len(documents))
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(m.BudgetMS)*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for i, document := range documents {
		wg.Go(func() {
			key := ""
			if ev.useCache && m.DedupSec > 0 {
				key = verdictKey(ev, provider, modelName, document)
				if cached, found, _ := verdictCache.Get(key); found {
					verdicts[i] = documentVerdict{scores: cached.scores, cached: true}
					return
				}
			}
			req := ClassifyRequest{State: document, Questions: ev.binding.Policy.asked, Model: modelName, MaxAttempts: m.MaxAttempts}
			result, err := classifier.Classify(callCtx, req)
			if err != nil {
				verdicts[i] = documentVerdict{err: err}
				return
			}
			if key != "" {
				verdictCache.SetWithTTL(key, cachedVerdict{scores: result.Scores, model: modelName}, time.Duration(m.DedupSec)*time.Second)
			}
			verdicts[i] = documentVerdict{scores: result.Scores}
		})
	}
	wg.Wait()
	return verdicts
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

// commit preserves violating evidence before an eligible fresh model verdict
// can affect reputation. Local rules only trigger the model's review.
func commit(ev evaluation, d Decision) {
	if d.Outcome == OutcomeBlock || d.Outcome == OutcomeWouldBlock {
		if d.Layer == LayerDistill || d.Layer == LayerHardRegex || d.Layer == LayerModel {
			recordReviewCandidate(ev, d)
		}
	}
	if ev.binding.Mode != ModeEnforce || d.Outcome != OutcomeBlock {
		return
	}
	now := ev.now.Unix()
	if d.BlockID > 0 {
		if err := ev.store.HitBlock(d.BlockID, now); err != nil {
			common.SysError(common.LogText("risk block hit update failed: %s", err.Error()))
		}
	}
	if d.RefusalID > 0 {
		if err := ev.store.HitRefusal(d.RefusalID, now); err != nil {
			common.SysError(common.LogText("risk refusal hit update failed: %s", err.Error()))
		}
	}
}

func recordReviewCandidate(ev evaluation, d Decision) {
	payload := ev.binding.Policy.Payload
	store, ok := ev.store.(ReviewStore)
	if !ok || ev.attempt.UserID <= 0 || ev.attempt.RequestID == "" ||
		(d.Outcome != OutcomeBlock && d.Outcome != OutcomeWouldBlock) ||
		(d.Layer != LayerModel && d.Layer != LayerHardRegex && d.Layer != LayerDistill) {
		return
	}
	evidence := ""
	if payload.Evidence.StoreReviewEvidence {
		evidence = d.ReviewEvidence
		if evidence == "" {
			documents, _, _, _ := ev.input.ModelDocuments(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64, payload.Model.MaxAuditChars, nil)
			evidence = strings.Join(documents, "\n\n--- audit document ---\n\n")
		}
	}
	scores := ""
	if len(d.Scores) > 0 {
		encoded, _ := common.Marshal(d.Scores)
		scores = string(encoded)
	}
	assessments, _ := common.Marshal(d.Assessments)
	// A model switch is still the same incident for behavior scoring. The
	// original fingerprint is retained to clear the corresponding refusal.
	incidentInput := *ev.input
	incidentInput.Model = ""
	rec := &model.RiskReview{
		RequestId: ev.attempt.RequestID, Namespace: ev.attempt.Namespace,
		GroupName: ev.attempt.EffectiveGroup, UserId: ev.attempt.UserID, Mode: ev.binding.Mode,
		Layer: d.Layer, Category: d.Category, Reason: d.Reason, Model: truncateRunes(ev.attempt.Model, 128),
		ProviderModel: d.ProviderModel, Scores: scores, PolicyKey: d.PolicyKey, PolicyRevision: d.PolicyRevision,
		Fingerprint: d.Fingerprint, IncidentKey: Fingerprint(&incidentInput), Evidence: evidence, Coverage: d.Coverage,
		TokenId: ev.attempt.TokenID, JudgmentSource: d.JudgmentSource,
		ScreeningScore: d.ScreeningScore, ScreeningReason: d.ScreeningReason, ConfigRevision: d.ConfigRevision,
		Assessments: string(assessments), Deduction: d.Deduction, JudgmentVersion: d.JudgmentVersion,
	}
	if payload.Evidence.StoreReviewEvidence {
		rec.OriginalRequest = ev.input.OriginalRequest
		rec.OriginalRequestHash = ev.input.OriginalRequestHash
		rec.OriginalRequestTruncated = ev.input.OriginalRequestTruncated
	}
	// Context is part of violation identity. A different tool/assistant
	// context must not inherit an earlier human dismissal or model verdict.
	incidentHash := sha256.Sum256([]byte(rec.IncidentKey + "\x00" + ev.input.IncidentContextDigest))
	rec.IncidentKey = hex.EncodeToString(incidentHash[:])
	policy := payload.Behavior.Policy()
	if ev.snapshot != nil {
		policy.ExpectedChangeRevision = ev.snapshot.ChangeRevision
		policy.ExpectedConfigRevision = ev.snapshot.ConfigRevision
	}
	autoConfirm := d.AutoConfirm && ev.binding.Mode == ModeEnforce && ev.attempt.TokenID > 0
	if autoConfirm {
		provider := ev.snapshot.Providers[payload.Model.ProviderID]
		// A provider can change without publishing a policy revision. Check
		// the judged endpoint in the confirmation transaction as well.
		policy.ExpectedProvider = &model.RiskProvider{
			Id: provider.ID, ProviderType: provider.Type, BaseURL: provider.BaseURL,
			Model: provider.Model, Proxy: provider.Proxy, KeysRevision: provider.KeysRevision,
		}
	}
	allowAutoBlock := autoConfirm && payload.Behavior.AutoBlockEnabled
	if _, err := store.CreateModelReview(rec, policy, autoConfirm, allowAutoBlock, ev.now.Unix()); err != nil {
		common.SysError(common.LogText("risk review write failed: %s", err.Error()))
	}
}

func counterKey(attempt AttemptContext, d Decision, isFirst bool, now time.Time) model.RiskCounterKey {
	first := 0
	if isFirst {
		first = 1
	}
	group := attempt.EffectiveGroup
	if group == "" && attempt.ViaAuto {
		group = "auto"
	}
	return model.RiskCounterKey{
		Bucket: now.Truncate(time.Hour).Unix(), Namespace: attempt.Namespace, GroupName: group, Route: attempt.Route,
		Outcome: d.Outcome, Layer: d.Layer, Reason: d.Reason, IsFirst: first,
	}
}

func buildEvent(attempt AttemptContext, d Decision, mode string, deviceID string, now time.Time) *model.RiskEvent {
	if (d.Outcome != OutcomeBlock && d.Outcome != OutcomeWouldBlock) ||
		(d.Layer != LayerModel && d.Layer != LayerHardRegex && d.Layer != LayerDistill) {
		d.Preview, d.Keyword = "", ""
	}
	scores := ""
	if len(d.Scores) > 0 {
		encoded, _ := common.Marshal(d.Scores)
		scores = string(encoded)
	}
	device := ""
	if deviceID != "" {
		device = hashDevice(deviceID)
	}
	return &model.RiskEvent{
		RequestId: attempt.RequestID, Attempt: attempt.Index, CreatedAt: now.Unix(), UserId: attempt.UserID, TokenId: attempt.TokenID,
		UserGroup: attempt.UserGroup, TokenGroup: attempt.TokenGroup, EffectiveGroup: attempt.EffectiveGroup, Namespace: attempt.Namespace,
		GroupSource: attempt.GroupSource, ChannelId: attempt.ChannelID, Model: truncateRunes(attempt.Model, 128), Route: attempt.Route,
		Mode: mode, ConfigRevision: d.ConfigRevision, PolicyKey: d.PolicyKey, PolicyRevision: d.PolicyRevision,
		Layer: d.Layer, Outcome: d.Outcome, Reason: d.Reason, Category: d.Category, Keyword: truncateRunes(d.Keyword, 64),
		Rule: truncateRunes(d.Rule, 160), Scores: scores, ProviderModel: d.ProviderModel, LatencyMs: int(d.LatencyMS),
		Coverage: d.Coverage, ErrorCode: string(d.Code), Preview: truncateRunes(d.Preview, 240), Fingerprint: d.Fingerprint,
		DeviceHash: device, Similarity: d.Similarity,
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
// Disabled, bypassed, exempt and uncovered traffic return before the body is parsed.
func CheckAttempt(c *gin.Context, info *relaycommon.RelayInfo, in AttemptInput) *types.NewAPIError {
	if info.TokenId > 0 && (!c.GetBool("risk_token_block_checked") || in.Phase == PhaseWSSelect || in.Phase == PhaseWSLocked) {
		block, err := defaultStore.ActiveTokenBlock(info.UserId, info.TokenId, time.Now().Unix())
		if err != nil {
			return decisionError(c, storeFailure(Decision{}, err), nil)
		}
		if block != nil {
			return decisionError(c, Decision{Outcome: OutcomeBlock, Layer: LayerRefusal, Reason: ReasonSubject}, nil)
		}
	}
	snap := current.Load()
	if snap == nil || !snap.Enabled {
		return nil
	}
	group, source, viaAuto := ResolveAttemptGroup(c, groupInput(in, info))
	// A bypassed group never enters the audit: no request state, counters,
	// events or freshness check, so a stale snapshot cannot fail it either.
	if IsBypassed(snap.Scope, group, viaAuto) {
		// Configuration can change between attempts of an in-flight request.
		// Do not learn an upstream refusal against its earlier audited attempt.
		if st := existingRequestState(c); st != nil {
			st.mu.Lock()
			st.last = nil
			st.mu.Unlock()
		}
		return nil
	}
	st := requestStateFor(c)
	st.mu.Lock()
	defer st.mu.Unlock()

	if in.Phase == PhaseHTTPLoop && st.firstDone {
		if group == st.firstGroup {
			return st.firstErr
		}
		logger.LogWarn(c, common.LogText("risk group_mismatch: pre-billing group %q, first attempt group %q", st.firstGroup, group))
	}
	// A later exempt or uncovered attempt must not learn a refusal against
	// the previous attempt's group or policy.
	st.last = nil

	now := attemptTime()
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

	ev := evaluation{snapshot: snap, binding: binding, attempt: attempt, input: st.input, store: defaultStore,
		classifier: classifierFor, useCache: true, runModel: true, now: now}
	started := time.Now()
	var raw Decision
	if st.inputErr != nil {
		base := Decision{Outcome: OutcomePass, PolicyKey: binding.Policy.Key, PolicyRevision: binding.Policy.Revision, ConfigRevision: snap.ConfigRevision}
		raw = failureDecision(base, binding.Policy.Payload, LayerGate, ReasonCoverageIncomplete, "unreadable request body")
	} else {
		provider := snap.Providers[binding.Policy.Payload.Model.ProviderID]
		key := fmt.Sprintf("%s|%d|%d|%d|%s|%s|%s", attempt.Namespace, attempt.UserID, snap.ConfigRevision, snap.ChangeRevision, binding.Policy.Fingerprint, providerFingerprint(provider), st.input.Digest)
		if prev, ok := st.reuse[key]; ok && prev.Blocks() {
			raw = prev
			raw.AutoConfirm = false
		} else {
			ctx := context.Background()
			if c.Request != nil {
				ctx = c.Request.Context()
			}
			raw = evaluate(ctx, ev)
			raw.LatencyMS = time.Since(started).Milliseconds()
			st.reuse[key] = raw
		}
	}
	d := applyMode(raw, binding.Mode)
	var riskErr *types.NewAPIError
	if binding.Mode == ModeEnforce && d.Blocks() {
		riskErr = decisionError(c, d, binding.Policy)
		d.Code = riskErr.GetErrorCode()
	}
	if st.input != nil {
		// Concurrent model calls can finish out of request-start order. Score
		// against confirmation time so already committed violations are visible.
		ev.now = attemptTime()
		commit(ev, d)
	}
	rec := Record{Counter: counterKey(attempt, d, isFirst, now)}
	if d.Outcome != OutcomePass || d.Reviewed {
		deviceID := ""
		if st.input != nil {
			deviceID = st.input.DeviceID
		}
		rec.Event = buildEvent(attempt, d, binding.Mode, deviceID, now)
	}
	recorder().Record(rec)
	appendPolicyEvent(c, attempt, d)
	if st.input != nil {
		st.last = &auditedAttempt{attempt: attempt, binding: binding, input: st.input, configRevision: snap.ConfigRevision}
	}
	return finish(riskErr)
}
