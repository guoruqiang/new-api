package risk

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// CoveredRoute reports which audited route a relay request belongs to, or ""
// when the path is not covered (compact, Gemini, realtime, media, embeddings,
// task-plugin traffic and so on). Uncovered traffic keeps its old behavior.
func CoveredRoute(c *gin.Context, format types.RelayFormat, relayMode int, channelType int) string {
	if c != nil {
		if _, claimed := c.Get(jsplugin.ContextKeyPinnedEndpoint); claimed {
			return ""
		}
	}
	if channelType == constant.ChannelTypeTaskPlugin {
		return ""
	}
	path := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	switch format {
	case types.RelayFormatOpenAI:
		switch relayMode {
		case relayconstant.RelayModeChatCompletions:
			if strings.HasPrefix(path, "/pg/") {
				return RoutePlayground
			}
			return RouteChatCompletions
		case relayconstant.RelayModeCompletions:
			return RouteCompletions
		}
	case types.RelayFormatClaude:
		return RouteClaudeMessages
	case types.RelayFormatOpenAIResponses:
		if relayMode == relayconstant.RelayModeResponses {
			return RouteResponses
		}
	}
	return ""
}

// CoveredRoutes and UncoveredRoutes are shown to administrators so that
// "not audited" is never mistaken for "audited and passed".
var CoveredRoutes = []string{
	"POST /v1/chat/completions", "POST /v1/completions", "POST /v1/messages",
	"POST /v1/responses", "GET /v1/responses (WebSocket response.create)", "POST /pg/chat/completions",
}

var UncoveredRoutes = []string{
	"POST /v1/responses/compact", "Gemini native (/v1beta)", "POST /v1/embeddings", "POST /v1/moderations",
	"POST /v1/rerank", "GET /v1/realtime", "images and audio", "task plugin traffic", "POST /v1/messages/count_tokens",
}
