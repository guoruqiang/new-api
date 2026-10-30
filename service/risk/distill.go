package risk

import (
	"regexp"
	"strings"
)

var negatedExtraction = regexp.MustCompile(`(?i)\b(?:do not|don't|never|must not|should not)\s+$`)

func matchNeedles(text string, needles []string) string {
	hay := strings.ToLower(text)
	if hay == "" {
		return ""
	}
	for _, raw := range needles {
		needle := strings.ToLower(raw)
		if needle != "" && strings.Contains(hay, needle) {
			return raw
		}
	}
	return ""
}

func matchFingerprints(text string, fingerprints []string) string {
	hay := NormalizeText(text)
	if hay == "" {
		return ""
	}
	for _, raw := range fingerprints {
		needle := NormalizeText(raw)
		if needle != "" && strings.Contains(hay, needle) {
			return raw
		}
	}
	return ""
}

// matchDistillPatterns ignores a match directly preceded by a negation, so a
// ban on disclosing reasoning is not itself an extraction request.
func matchDistillPatterns(text string, patterns []*regexp.Regexp) string {
	hay := NormalizeText(text)
	if hay == "" {
		return ""
	}
	for _, re := range patterns {
		for _, loc := range re.FindAllStringIndex(hay, -1) {
			if !negatedExtraction.MatchString(hay[:loc[0]]) {
				return hay[loc[0]:loc[1]]
			}
		}
	}
	return ""
}

func wantsThinkingText(t ThinkingRequest, floor int) bool {
	if strings.EqualFold(t.Display, "summarized") {
		return true
	}
	if strings.EqualFold(t.Type, "enabled") && floor > 0 && t.BudgetTokens >= floor {
		return true
	}
	switch strings.ToLower(t.ReasoningEffort) {
	case "high", "xhigh", "max":
		return true
	}
	return t.N > 1
}

func isHarvestStructure(in *Input, st DistillStructure) bool {
	if st.RequireNoTools && in.ToolCount > 0 {
		return false
	}
	if st.RequireSingleTurn && in.MessageCount > 1 {
		return false
	}
	if st.MinMaxTokens <= 0 {
		return false
	}
	if in.MaxTokens != nil && *in.MaxTokens < st.MinMaxTokens {
		return false
	}
	return wantsThinkingText(in.Thinking, st.MinMaxTokens)
}

// withFolded appends the confusable-folded form of text when it differs, so
// needles and patterns also match obfuscated spellings.
func withFolded(text string) string {
	if folded := foldConfusables(text); folded != text {
		return text + "\n" + folded
	}
	return text
}

// distillHit locates a distill match in the scanned text. A match that cannot
// be located, such as one found only in folded text, keeps the whole text.
func distillHit(scan, match, rule string) keywordHit {
	match = NormalizeText(match)
	start := strings.Index(scan, match)
	if start < 0 || match == "" {
		start, match = len(scan), ""
	}
	return keywordHit{Category: "distill", Keyword: truncateRunes(match, 40), Rule: rule, Text: scan, Start: start, End: start + len(match)}
}

// detectDistill reports the first distill signal. Templates, benchmark
// questions and reasoning knobs only select the request for the decision
// model; they never prove an extraction attempt on their own.
func detectDistill(in *Input, p *CompiledPolicy, scan string) (keywordHit, bool) {
	rules := p.Payload.Distill
	if !rules.Enabled || (rules.SkipGPTModels && IsGPTSeriesModel(in.Model)) {
		return keywordHit{}, false
	}
	joined := withFolded(in.distillJoined())
	if needle := matchNeedles(joined, rules.HardNeedles); needle != "" {
		return distillHit(scan, needle, "harvest_needle"), true
	}
	if hit := matchDistillPatterns(joined, p.distillPatterns); hit != "" {
		return distillHit(scan, hit, "distill_regex"), true
	}
	if rules.SkipOfficial && in.Official {
		return keywordHit{}, false
	}
	fingerprintText := joined
	if in.LatestUser != "" {
		fingerprintText = withFolded(in.LatestUser)
	}
	if fp := matchFingerprints(fingerprintText, rules.Fingerprints); fp != "" {
		return distillHit(scan, fp, "fingerprint"), true
	}
	if needle := matchNeedles(joined, rules.Needles); needle != "" {
		return distillHit(scan, needle, "needle"), true
	}
	if p.contest != nil && p.contest.MatchString(joined) && isHarvestStructure(in, rules.Structure) {
		return distillHit(scan, p.contest.FindString(joined), "harvest"), true
	}
	return keywordHit{}, false
}
