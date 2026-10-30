package risk

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	ErrorCodePolicyBlocked      types.ErrorCode = "risk_policy_blocked"
	ErrorCodeServiceUnavailable types.ErrorCode = "risk_service_unavailable"
	ErrorCodeConfigUnavailable  types.ErrorCode = "risk_config_unavailable"
)

// IsRiskError reports whether err is a local risk rejection. These errors stop
// retries, are not channel faults and never close a WebSocket connection.
func IsRiskError(err *types.NewAPIError) bool {
	if err == nil || err.GetErrorType() != types.ErrorTypeNewAPIError {
		return false
	}
	switch err.GetErrorCode() {
	case ErrorCodePolicyBlocked, ErrorCodeServiceUnavailable, ErrorCodeConfigUnavailable:
		return true
	}
	return false
}

func riskError(message string, code types.ErrorCode, status int) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(message), code, status, types.ErrOptionWithSkipRetry())
}

// decisionError builds the client-facing error for a blocking decision. The
// message never names matched keywords or rules.
func decisionError(c *gin.Context, d Decision) *types.NewAPIError {
	if d.Outcome == OutcomeErrorBlock {
		return riskError(i18n.T(c, i18n.MsgRiskServiceUnavailable), ErrorCodeServiceUnavailable, http.StatusServiceUnavailable)
	}
	return riskError(i18n.T(c, i18n.MsgRiskPolicyBlocked, map[string]any{"Category": d.Category}), ErrorCodePolicyBlocked, http.StatusForbidden)
}

func configUnavailableError(c *gin.Context) *types.NewAPIError {
	return riskError(i18n.T(c, i18n.MsgRiskConfigUnavailable), ErrorCodeConfigUnavailable, http.StatusServiceUnavailable)
}
