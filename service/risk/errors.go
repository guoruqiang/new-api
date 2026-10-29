package risk

import (
	"errors"
	"math"
	"net/http"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	ErrorCodeDistillBlocked     types.ErrorCode = "risk_distill_blocked"
	ErrorCodePolicyBlocked      types.ErrorCode = "risk_policy_blocked"
	ErrorCodeRefusalGuard       types.ErrorCode = "risk_refusal_guard"
	ErrorCodeServiceUnavailable types.ErrorCode = "risk_service_unavailable"
	ErrorCodeConfigUnavailable  types.ErrorCode = "risk_config_unavailable"
	ErrorCodeUpstreamRefusal    types.ErrorCode = "risk_upstream_refusal"
)

// IsRiskError reports whether err is a local risk rejection. These errors stop
// retries, are not channel faults and never close a WebSocket connection.
func IsRiskError(err *types.NewAPIError) bool {
	if err == nil || err.GetErrorType() != types.ErrorTypeNewAPIError {
		return false
	}
	switch err.GetErrorCode() {
	case ErrorCodeDistillBlocked, ErrorCodePolicyBlocked, ErrorCodeRefusalGuard,
		ErrorCodeServiceUnavailable, ErrorCodeConfigUnavailable, ErrorCodeUpstreamRefusal:
		return true
	}
	return false
}

func riskError(message string, code types.ErrorCode, status int) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(message), code, status, types.ErrOptionWithSkipRetry())
}

// decisionError builds the client-facing error for a blocking decision. The
// message names only the category; matched keywords and rules stay with
// administrators.
func decisionError(c *gin.Context, d Decision, policy *CompiledPolicy) *types.NewAPIError {
	switch {
	case d.Outcome == OutcomeErrorBlock:
		return riskError(i18n.T(c, i18n.MsgRiskServiceUnavailable), ErrorCodeServiceUnavailable, http.StatusServiceUnavailable)
	case d.Layer == LayerDistill:
		message := policy.Payload.Distill.Error.Message
		if message == "" {
			message = i18n.T(c, i18n.MsgRiskDistillBlocked)
		}
		return riskError(message, ErrorCodeDistillBlocked, policy.Payload.Distill.Error.Status)
	case d.Layer == LayerRefusal && d.Reason == ReasonSubject:
		return riskError(i18n.T(c, i18n.MsgRiskTokenRestricted), ErrorCodeRefusalGuard, http.StatusForbidden)
	case d.Layer == LayerRefusal && d.Reason == ReasonSimilar:
		percent := int(math.Round(d.Similarity * 100))
		return riskError(i18n.T(c, i18n.MsgRiskRefusalSimilar, map[string]any{"Percent": percent}), ErrorCodeRefusalGuard, http.StatusBadRequest)
	case d.Layer == LayerRefusal:
		return riskError(i18n.T(c, i18n.MsgRiskRefusalExact), ErrorCodeRefusalGuard, http.StatusBadRequest)
	}
	return riskError(i18n.T(c, i18n.MsgRiskPolicyBlocked, map[string]any{"Category": d.Category}), ErrorCodePolicyBlocked, http.StatusForbidden)
}

func configUnavailableError(c *gin.Context) *types.NewAPIError {
	return riskError(i18n.T(c, i18n.MsgRiskConfigUnavailable), ErrorCodeConfigUnavailable, http.StatusServiceUnavailable)
}
