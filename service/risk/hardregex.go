package risk

import (
	"regexp"
	"strings"
)

// Hard rule categories; "distill" rules follow the distill switch.
var HardRuleCategories = []string{"nsfw", "distill", "crack", "jailbreak", "custom"}

// Rule actions: a block rule stops the request on its own; a review rule only
// sends the request to the decision model, which makes the final call.
const (
	RuleActionBlock  = "block"
	RuleActionReview = "review"
)

const (
	hardRuleMax             = 60
	hardPatternMax          = 50
	patternMaxLen           = 400
	keygenPattern           = `(?:^|[^\w-])(keygen|warez)(?:[^\w-]|$)`
	jailbreakControlPattern = `(?:解除|关闭|绕过|无视)(?:(?:你的|所有|全部)?(?:安全|内容)|你的|所有|全部)(?:限制|审查|过滤|策略)`
	crackToolPattern        = `(?:破解补丁|注册机(?:$|[^制构器])|激活码生成器)`
)

// BuiltinHardRules are vm2api's HARD_POLICY_RULES rewritten for RE2. Only
// the keygen rule used look-around; it now captures the keyword in group 1.
// Explicit requests for sexual content block directly; the broader jailbreak,
// cracking and phishing phrases also occur in ordinary work and are reviewed.
// Distill rules belong to the distill layer and always block.
var BuiltinHardRules = []HardRule{
	{Category: "jailbreak", Source: `\b(?:jailbreak|dan)\s+mode\b`, Enabled: true, Action: RuleActionReview},
	{Category: "jailbreak", Source: `\bignore\s+(?:all\s+)?(?:previous|prior|above)\s+(?:instructions|rules|prompts)\b`, Enabled: true, Action: RuleActionReview},
	{Category: "jailbreak", Source: `\b(?:bypass|disable|remove)\s+(?:your\s+|the\s+|all\s+)?(?:safety|content)\s+(?:filters?|policies|restrictions|limits)\b`, Enabled: true, Action: RuleActionReview},
	{Category: "jailbreak", Source: jailbreakControlPattern, Enabled: true, Action: RuleActionReview},
	{Category: "jailbreak", Source: `(?:越狱|破限)(?:模式|提示词|提示|指令)`, Enabled: true, Action: RuleActionReview},
	{Category: "crack", Source: keygenPattern, Enabled: true, Action: RuleActionReview},
	{Category: "crack", Source: crackToolPattern, Enabled: true, Action: RuleActionReview},
	{Category: "crack", Source: `\bcrack(?:ing)?\s+(?:this\s+|the\s+)?(?:software|license|drm|activation)\b`, Enabled: true, Action: RuleActionReview},
	{Category: "nsfw", Source: `\b(?:write|generate|describe)\s+(?:an?\s+)?(?:explicit|pornographic)\s+(?:sex|sexual)\b`, Enabled: true, Action: RuleActionBlock},
	{Category: "nsfw", Source: `(?:生成|写一段|描述)(?:露骨|色情)(?:内容|小说|场景)`, Enabled: true, Action: RuleActionBlock},
	{Category: "distill", Source: `\bdistill(?:ation|ing)?\s+(?:of\s+)?(?:(?:the|your|a|an|reusable|durable|rollout|hidden|internal|full|complete)\s+)?(?:reasoning(?:\s+traces?)?|chain[- ]of[- ]thoughts?|teacher(?:\s+model)?)\b`, Enabled: true, Action: RuleActionBlock},
	{Category: "distill", Source: `\b(?:extract|export|dump|reveal|harvest|exfiltrate)(?:ing|ed|ion|s)?\s+(?:(?:the|your|a|an|full|hidden|internal|complete|raw|durable|entire)\s+){0,4}(?:chain[- ]of[- ]thoughts?|reasoning\s+traces?|hidden\s+reasoning|internal\s+reasoning|internal\s+monologues?)\b`, Enabled: true, Action: RuleActionBlock},
	{Category: "distill", Source: `\b(?:chain[- ]of[- ]thought|reasoning\s+trace)\s+extraction\b`, Enabled: true, Action: RuleActionBlock},
	{Category: "distill", Source: `(?:思维链蒸馏|推理蒸馏|思考链蒸馏)`, Enabled: true, Action: RuleActionBlock},
	{Category: "distill", Source: `(?:提取|导出|抽取|蒸馏|收割|扒取)\s*(?:出|取)?\s*(?:你的|本人的|完整|全部|隐藏|内部)?\s*(?:的)?\s*(?:思维链|思考链|推理链)`, Enabled: true, Action: RuleActionBlock},
	{Category: "distill", Source: `(?:思维链|思考链|推理链)\s*(?:的)?\s*(?:提取|导出|抽取|蒸馏|收割)`, Enabled: true, Action: RuleActionBlock},
	{Category: "distill", Source: `(?:提取|导出|抽取)\s*(?:出)?\s*(?:隐藏|内部)\s*(?:的)?\s*(?:推理|思维|思考)`, Enabled: true, Action: RuleActionBlock},
	{Category: "custom", Source: `\bphishing\s+kit\b`, Enabled: true, Action: RuleActionReview},
}

type compiledRule struct {
	category string
	source   string
	action   string
	re       *regexp.Regexp
}

func compileCaseless(source string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + source)
}

// hardHit is one rule match. Text and the byte offsets locate the match in
// the scanned text, so a review can send the surrounding text to the model.
type hardHit struct {
	Category string
	Keyword  string
	Rule     string
	Action   string
	Text     string
	Start    int
	End      int
}

func truncateRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n])
}

// matchHardRules checks both text forms. Distill hits take precedence over
// block hits, which take precedence over review hits; rule order breaks ties.
func matchHardRules(text string, rules []compiledRule, allow func(category string) bool) (hardHit, bool) {
	if text == "" {
		return hardHit{}, false
	}
	texts := []string{text}
	if folded := foldConfusables(text); folded != text {
		texts = append(texts, folded)
	}
	var best hardHit
	found := false
	for _, rule := range rules {
		if !allow(rule.category) {
			continue
		}
		for _, candidate := range texts {
			loc := rule.re.FindStringSubmatchIndex(candidate)
			if loc == nil {
				continue
			}
			start, end := loc[0], loc[1]
			if rule.source == keygenPattern && len(loc) >= 4 && loc[2] >= 0 {
				start, end = loc[2], loc[3]
			}
			keyword := strings.Join(strings.Fields(candidate[start:end]), " ")
			if keyword == "" {
				keyword = rule.category
			}
			hit := hardHit{Category: rule.category, Keyword: truncateRunes(keyword, 40), Rule: truncateRunes(rule.source, 120),
				Action: rule.action, Text: candidate, Start: loc[0], End: loc[1]}
			if rule.category == "distill" {
				return hit, true
			}
			if !found || best.Action != RuleActionBlock && rule.action == RuleActionBlock {
				best, found = hit, true
			}
		}
	}
	return best, found
}

// excerpt includes the complete match and as much surrounding context as
// size runes allow. A match longer than the window cannot be confirmed by a
// partial excerpt, so it is left for the administrator to review.
func (h hardHit) excerpt(size int) (string, bool) {
	before, match, after := []rune(h.Text[:h.Start]), []rune(h.Text[h.Start:h.End]), []rune(h.Text[h.End:])
	if len(match) > size || size <= 0 {
		return "", false
	}
	room := size - len(match)
	trail := min(len(after), room-min(len(before), room/2))
	lead := min(len(before), room-trail)
	return string(before[len(before)-lead:]) + string(match) + string(after[:trail]), true
}
