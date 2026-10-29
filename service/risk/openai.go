package risk

import (
	"math"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

// OpenAIModerationCategories are the fixed categories of omni-moderation.
// They do not cover arbitrary questions, jailbreaks or prompt injection.
var OpenAIModerationCategories = []string{
	"sexual", "sexual/minors", "harassment", "harassment/threatening",
	"hate", "hate/threatening", "illicit", "illicit/violent",
	"self-harm", "self-harm/intent", "self-harm/instructions",
	"violence", "violence/graphic",
}

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

// parseOpenAIModeration keeps the official flags separate from confidence.
// Converted safety scores preserve the gateway's high-is-safe convention;
// they must never replace the provider's per-category violation flags.
func parseOpenAIModeration(body []byte) (ClassifyResult, error) {
	var doc struct {
		Model   string `json:"model"`
		Results []struct {
			Flagged    *bool               `json:"flagged"`
			Categories map[string]*bool    `json:"categories"`
			Scores     map[string]*float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := common.Unmarshal(body, &doc); err != nil {
		return ClassifyResult{}, unavailable("malformed moderation response")
	}
	if !strings.HasPrefix(doc.Model, "omni-moderation-") || utf8.RuneCountInString(doc.Model) > 120 || len(doc.Results) != 1 {
		return ClassifyResult{}, unavailable("missing moderation result")
	}
	row := doc.Results[0]
	if row.Flagged == nil {
		return ClassifyResult{}, unavailable("missing moderation flags")
	}
	for _, category := range OpenAIModerationCategories {
		if row.Categories[category] == nil || row.Scores[category] == nil {
			return ClassifyResult{}, unavailable("incomplete moderation categories")
		}
	}
	result := ClassifyResult{
		Model: doc.Model, Scores: make(map[string]float64, len(row.Categories)),
		Flags: make(map[string]bool, len(row.Categories)),
	}
	flagged := false
	for category, flag := range row.Categories {
		score := row.Scores[category]
		if flag == nil || score == nil || math.IsNaN(*score) || math.IsInf(*score, 0) || *score < 0 || *score > 1 {
			return ClassifyResult{}, unavailable("invalid moderation category")
		}
		if !slices.Contains(OpenAIModerationCategories, category) {
			if *flag {
				return ClassifyResult{}, unavailable("unsupported moderation category")
			}
			continue
		}
		flagged = flagged || *flag
		result.Flags[category] = *flag
		result.Scores[category] = 1 - *score
	}
	if flagged != *row.Flagged {
		return ClassifyResult{}, unavailable("inconsistent moderation flags")
	}
	return result, nil
}
