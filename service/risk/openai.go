package risk

import (
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// OpenAIModerationURL accepts a host, /v1 base or complete moderation endpoint.
func OpenAIModerationURL(base string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(trimmed)
	if err != nil || !validHTTPURL(trimmed) || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	switch {
	case moderationSuffix.MatchString(trimmed):
		return trimmed
	case versionSuffix.MatchString(trimmed):
		return trimmed + "/moderations"
	default:
		return trimmed + "/v1/moderations"
	}
}

// parseOpenAIModeration keeps OpenAI's per-category flags, which decide the
// violation, and converts risk scores to the gateway's high-is-safe scores.
func parseOpenAIModeration(body []byte) (ClassifyResult, error) {
	var doc struct {
		Model   string `json:"model"`
		Results []struct {
			Categories map[string]bool    `json:"categories"`
			Scores     map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := common.Unmarshal(body, &doc); err != nil || len(doc.Results) == 0 {
		return ClassifyResult{}, unavailable("malformed moderation response")
	}
	row := doc.Results[0]
	result := ClassifyResult{Model: doc.Model, Flags: row.Categories, Scores: make(map[string]float64, len(row.Scores))}
	for category, score := range row.Scores {
		result.Scores[category] = 1 - score
	}
	return result, nil
}

// moderationCategory maps an OpenAI category to a violation category.
func moderationCategory(category string) string {
	if category == "sexual" || category == "sexual/minors" {
		return "nsfw"
	}
	return "safety"
}
