package risk

import (
	"regexp"
	"strings"
)

var negatedExtraction = regexp.MustCompile(`(?i)\b(?:do not|don't|never|must not|should not)\s+$`)

type distillHit struct {
	Rule     string
	Evidence string
}

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
			return truncateRunes(raw, 80)
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
				return truncateRunes(hay[loc[0]:loc[1]], 80)
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

// detectDistill follows vm2api detectDistill: harvest wrappers and extra
// patterns always apply; the official-client exemption only relaxes the
// ordinary needles, fingerprints and the contest structure check.
func detectDistill(in *Input, p *CompiledPolicy) []distillHit {
	rules := p.Payload.Distill
	if !rules.Enabled || (rules.SkipGPTModels && IsGPTSeriesModel(in.Model)) {
		return nil
	}
	joined := withFolded(in.distillJoined())
	if needle := matchNeedles(joined, rules.HardNeedles); needle != "" {
		return []distillHit{{Rule: "harvest_needle", Evidence: needle}}
	}
	if hit := matchDistillPatterns(joined, p.distillPatterns); hit != "" {
		return []distillHit{{Rule: "distill_regex", Evidence: hit}}
	}
	if rules.SkipOfficial && in.Official {
		return nil
	}
	hits := make([]distillHit, 0, 3)
	fingerprintText := joined
	if in.LatestUser != "" {
		fingerprintText = withFolded(in.LatestUser)
	}
	if fp := matchFingerprints(fingerprintText, rules.Fingerprints); fp != "" {
		hits = append(hits, distillHit{Rule: "fingerprint", Evidence: fp})
	}
	if needle := matchNeedles(joined, rules.Needles); needle != "" {
		hits = append(hits, distillHit{Rule: "needle", Evidence: needle})
	}
	if p.contest != nil && len([]rune(p.Payload.Distill.Structure.ContestPattern)) <= patternMaxLen &&
		p.contest.MatchString(joined) && isHarvestStructure(in, rules.Structure) {
		hits = append(hits, distillHit{Rule: "harvest", Evidence: "contest+harvest"})
	}
	return hits
}
