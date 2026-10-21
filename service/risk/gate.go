package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
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

type subjectRef struct {
	kind  string
	value string
}

func subjectsFor(cfg RefusalConfig, attempt AttemptContext, in *Input) []subjectRef {
	subjects := []subjectRef{{kind: SubjectUser, value: strconv.Itoa(attempt.UserID)}}
	if cfg.BlockSubject == BlockSubjectUserDevice && in.DeviceID != "" {
		subjects = append(subjects, subjectRef{kind: SubjectDevice, value: hashDevice(in.DeviceID)})
	}
	return subjects
}

func blockDecision(base Decision, layer, reason, category, keyword, rule string, ban bool) Decision {
	d := base
	d.Outcome = OutcomeBlock
	d.Layer = layer
	d.Reason = reason
	d.Category = category
	d.Keyword = truncateRunes(keyword, 40)
	d.Rule = truncateRunes(rule, 120)
	d.BanSubject = ban
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
		Namespace  string   `json:"ns"`
		User       int      `json:"u"`
		Provider   string   `json:"p"`
		Model      string   `json:"m"`
		Questions  []string `json:"q"`
		Threshold  float64  `json:"t"`
		Band       float64  `json:"b"`
		Preprocess string   `json:"pp"`
		Document   string   `json:"d"`
	}{
		ev.attempt.Namespace, ev.attempt.UserID,
		providerFingerprint(ProviderConfig{Type: provider.Type, BaseURL: provider.BaseURL, KeyLines: endpointLines(provider)}),
		modelName, asked, p.Threshold, p.NearMissBand,
		fmt.Sprintf("%d:%t:%t", PreprocessVersion, ev.binding.Policy.Payload.HardRegex.StripReminders, ev.binding.Policy.Payload.HardRegex.ExpandBase64),
		document,
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
func evaluate(ctx context.Context, ev evaluation) Decision {
	p := ev.binding.Policy
	payload := p.Payload
	in := ev.input
	now := ev.now.Unix()
	d := Decision{
		Outcome: OutcomePass, Layer: LayerGate, Coverage: CoverageComplete,
		PolicyKey: p.Key, PolicyRevision: p.Revision, ConfigRevision: ev.snapshot.ConfigRevision,
	}
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

	if hits := detectDistill(in, p); len(hits) > 0 {
		evidence := make([]string, 0, len(hits))
		rules := make([]string, 0, len(hits))
		for _, h := range hits {
			evidence = append(evidence, h.Evidence)
			rules = append(rules, h.Rule)
		}
		return blockDecision(d, LayerDistill, hits[0].Rule, "distill", strings.Join(evidence, ";"), strings.Join(rules, ","), true)
	}

	ruleDoc, userIncomplete := in.RuleDocument(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64)
	contextDoc, contextIncomplete := in.ContextDocument(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64)
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
	// A block-tier hit in the user's own text stops the request. Review-tier
	// hits, and every hit in system, assistant or tool text, go to the model.
	var review *hardHit
	if hit, ok := matchHardRules(ruleDoc, p.hardRules, allow); ok {
		switch {
		case hit.Category == "distill":
			return blockDecision(d, LayerDistill, "distill_regex", "distill", hit.Keyword, hit.Rule, true)
		case hit.Action == RuleActionBlock:
			return blockDecision(d, LayerHardRegex, hit.Category, hit.Category, hit.Keyword, hit.Rule, true)
		}
		review = &hit
	}
	if review == nil {
		notDistill := func(category string) bool { return category != "distill" && allow(category) }
		if hit, ok := matchHardRules(contextDoc, p.hardRules, notDistill); ok {
			review = &hit
		}
	}

	if cfg := payload.Refusal; cfg.Enabled {
		ns := ev.attempt.Namespace
		if cfg.SubjectBlockEnabled {
			for _, s := range subjectsFor(cfg, ev.attempt, in) {
				blk, err := ev.store.ActiveBlock(ns, s.kind, s.value, now)
				if err != nil {
					return storeFailure(d, err)
				}
				if blk != nil {
					hit := blockDecision(d, LayerRefusal, ReasonSubject, "subject", blk.Reason, "", false)
					hit.BlockID = blk.Id
					return hit
				}
			}
		}
		rec, err := ev.store.ExactRefusal(ns, ev.attempt.UserID, d.Fingerprint, now)
		if err != nil {
			return storeFailure(d, err)
		}
		if rec != nil {
			hit := blockDecision(d, LayerRefusal, ReasonExact, "refusal", ReasonExact, "", true)
			hit.RefusalID = rec.Id
			return hit
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
					hit := blockDecision(d, LayerRefusal, ReasonSimilar, "refusal", fmt.Sprintf("similar %.0f%%", best*100), "", true)
					hit.RefusalID = bestID
					hit.Similarity = best
					return hit
				}
			}
		}
	}

	m := payload.Model
	if review != nil {
		d.Layer, d.Category, d.Keyword, d.Rule, d.Reviewed = LayerHardRegex, review.Category, review.Keyword, review.Rule, true
	}
	if !m.Enabled || !ev.runModel {
		switch {
		case review != nil:
			d.Reason = ReasonRuleUnconfirmed
		case scanned:
			d.Reason = ReasonRegexMiss
		default:
			d.Reason = ReasonUnchecked
		}
		return d
	}
	provider, ok := ev.snapshot.Providers[m.ProviderID]
	if !ok || provider.BaseURL == "" && len(endpointLines(provider)) == 0 {
		return failureDecision(d, payload, LayerModel, ReasonUnavailable, "no provider")
	}
	documents, tailWindow, docIncomplete, reviewCovered := in.ModelDocuments(payload.HardRegex.StripReminders, payload.HardRegex.ExpandBase64, m.MaxAuditChars, review)
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
	var worst *modelHit
	merged := map[string]float64{}
	allCached := true
	for _, v := range classifyDocuments(ctx, ev, provider, modelName, documents) {
		if v.err != nil {
			if failure == nil {
				failure = v.err
			}
			allCached = false
			continue
		}
		allCached = allCached && v.cached
		for id, score := range v.scores {
			if current, seen := merged[id]; !seen || score < current {
				merged[id] = score
			}
		}
		if hits := judgeScores(v.scores, p.asked, m.Threshold, m.NearMissBand); len(hits) > 0 && (worst == nil || hits[0].score < worst.score) {
			worst = &hits[0]
			d.Scores = v.scores
		}
	}
	if worst != nil {
		return blockDecision(d, LayerModel, "model", worst.category, worst.category, d.Rule, true)
	}
	d.Scores = merged
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
	case review != nil:
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
			result, err := classifier.Classify(callCtx, ClassifyRequest{State: document, Questions: ev.binding.Policy.asked, Model: modelName, MaxAttempts: m.MaxAttempts})
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

// commit applies the enforce-mode side effects of a blocking decision:
// hit counters and subject blocks. Observe mode writes nothing.
func commit(ev evaluation, d Decision) {
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
	cfg := ev.binding.Policy.Payload.Refusal
	if d.BanSubject && cfg.Enabled && cfg.SubjectBlockEnabled && strikeLimitReached(ev, cfg) {
		blockSubjects(ev, cfg, d.Layer+":"+d.Category, d.Fingerprint)
	}
}

// strikeLimitReached counts this confirmed hit together with the user's
// earlier ones in the window. Events are written asynchronously, so a burst
// within the same second may need a hit or two more before the block.
func strikeLimitReached(ev evaluation, cfg RefusalConfig) bool {
	if cfg.BlockAfterHits <= 1 {
		return true
	}
	since := ev.now.Add(-time.Duration(cfg.BlockWindowMinutes) * time.Minute).Unix()
	earlier, err := ev.store.RecentStrikes(ev.attempt.Namespace, ev.attempt.UserID, since)
	if err != nil {
		common.SysError(common.LogText("risk strike count failed: %s", err.Error()))
		return false
	}
	return earlier+1 >= int64(cfg.BlockAfterHits)
}

func blockSubjects(ev evaluation, cfg RefusalConfig, reason, fingerprint string) {
	now := ev.now.Unix()
	expires := int64(0)
	if cfg.BlockTTLMinutes > 0 {
		expires = now + int64(cfg.BlockTTLMinutes)*60
	}
	for _, s := range subjectsFor(cfg, ev.attempt, ev.input) {
		err := ev.store.Block(&model.RiskSubjectBlock{
			Namespace: ev.attempt.Namespace, SubjectType: s.kind, SubjectValue: s.value,
			Reason: truncateRunes(reason, 80), Fingerprint: fingerprint, SourceRequestId: ev.attempt.RequestID,
			LastSeenAt: now, ExpiresAt: expires,
		})
		if err != nil {
			common.SysError(common.LogText("risk subject block failed: %s", err.Error()))
		}
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
		if prev, ok := st.reuse[key]; ok {
			raw = prev
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
