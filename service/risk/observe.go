package risk

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ObserveUpstreamSuccess learns from a response that the channel handler
// flagged as an upstream content refusal (finish_reason=content_filter,
// stop_reason=refusal, Responses content_filter or refusal output).
func ObserveUpstreamSuccess(c *gin.Context, info *relaycommon.RelayInfo) {
	if info == nil || !info.PerformanceBusinessRejection {
		return
	}
	learnRefusal(c, common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason), "")
}

// ObserveUpstreamError learns from an explicit upstream content refusal. In
// enforce mode the error becomes non-retryable so the request cannot escape
// to another group; ordinary upstream failures are returned unchanged.
func ObserveUpstreamError(c *gin.Context, info *relaycommon.RelayInfo, err *types.NewAPIError) *types.NewAPIError {
	if err == nil || IsRiskError(err) {
		return err
	}
	oai := err.ToOpenAIError()
	reason, ok := ClassifyUpstreamError(string(err.GetErrorCode()), oai.Type, oai.Message)
	if !ok && oai.Code != nil {
		reason, ok = ClassifyUpstreamError(fmt.Sprint(oai.Code), "", "")
	}
	if !ok {
		return err
	}
	if learnRefusal(c, reason, oai.Message) != ModeEnforce {
		return err
	}
	types.ErrOptionWithSkipRetry()(err)
	return err
}

// ObserveResponsesEvent learns from a Responses API response body or a
// terminal stream / WebSocket event that carries a content refusal.
func ObserveResponsesEvent(c *gin.Context, body []byte) bool {
	reason, ok := ClassifyResponsesPayload(body)
	if !ok {
		return false
	}
	learnRefusal(c, reason, "")
	return true
}

// learnRefusal records an upstream refusal against the last audited attempt
// and returns that attempt's mode. Enforce mode remembers the prompt. Both
// modes keep metadata only; an upstream refusal never confirms a violation
// or automatically adds punishment points.
func learnRefusal(c *gin.Context, reason, message string) string {
	if snap := current.Load(); snap == nil || !snap.Enabled {
		return ""
	}
	st := existingRequestState(c)
	if st == nil {
		return ""
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	last := st.last
	if last == nil || last.input == nil {
		return ""
	}
	mode := last.binding.Mode
	if st.learned[last.attempt.Index] {
		return mode
	}
	st.learned[last.attempt.Index] = true

	now := time.Now()
	policy := last.binding.Policy
	cfg := policy.Payload.Refusal
	d := Decision{
		Outcome: OutcomeUpstreamRefusal, Layer: LayerUpstream, Reason: truncateRunes(reason, 64), Category: "refusal",
		Keyword: truncateRunes(message, 40), Coverage: CoverageComplete, PolicyKey: policy.Key, PolicyRevision: policy.Revision,
		Fingerprint: Fingerprint(last.input), ConfigRevision: last.configRevision,
	}
	if mode == ModeObserve {
		d.Outcome = OutcomeWouldStop
	}
	if mode == ModeEnforce && cfg.Enabled {
		expires := int64(0)
		if cfg.MemoryTTLDays > 0 {
			expires = now.Unix() + int64(cfg.MemoryTTLDays)*86400
		}
		rec := &model.RiskRefusal{
			Namespace: last.attempt.Namespace, UserId: last.attempt.UserID,
			Model:       truncateRunes(strings.ToLower(strings.TrimSpace(last.input.Model)), 128),
			Fingerprint: d.Fingerprint, FingerprintVersion: FingerprintVersion,
			ErrorMessage: truncateRunes(reason, 64), SourceRequestId: last.attempt.RequestID,
			PolicyKey: policy.Key, PolicyRevision: policy.Revision, LastSeenAt: now.Unix(), ExpiresAt: expires,
		}
		if sig := Signature(last.input.SimilarityDocument()); sig != nil {
			rec.Signature = EncodeSignature(sig)
			rec.SignatureVersion = SignatureVersion
		}
		if err := defaultStore.Remember(rec); err != nil {
			common.SysError(common.LogText("risk refusal memory write failed: %s", err.Error()))
		}
	}
	rec := Record{Counter: counterKey(last.attempt, d, false, now), Event: buildEvent(last.attempt, d, mode, last.input.DeviceID, now)}
	recorder().Record(rec)
	appendPolicyEvent(c, last.attempt, d)
	return mode
}
