package risk

import (
	"regexp"
	"strings"
)

// HardRuleCategories label keyword rules; "distill" rules follow the distill
// switch. A match only selects the request for the decision model.
var HardRuleCategories = []string{"nsfw", "distill", "crack", "jailbreak", "custom"}

const (
	hardRuleMax             = 60
	hardPatternMax          = 50
	patternMaxLen           = 400
	keygenPattern           = `(?:^|[^\w-])(keygen|warez)(?:[^\w-]|$)`
	jailbreakControlPattern = `(?:解除|关闭|绕过|无视)(?:(?:你的|所有|全部)?(?:安全|内容)|你的|所有|全部)(?:限制|审查|过滤|策略)`
	crackToolPattern        = `(?:破解补丁|注册机(?:$|[^制构器])|激活码生成器)`
)

// BuiltinHardRules are vm2api's HARD_POLICY_RULES rewritten for RE2 plus a
// few broad keywords. Only the keygen rule used look-around; it now captures
// the keyword in group 1. 渗透 skips 渗透率/压/性/力, and 钓鱼 needs a phishing
// noun, because their everyday senses dominate ordinary text.
var BuiltinHardRules = []HardRule{
	{Category: "nsfw", Source: `(?:色情|性爱|裸照|淫秽)|\b(?:porn|nsfw|sexual|nude)\b`, Enabled: true},
	{Category: "jailbreak", Source: `(?:越狱|破限)|\bjailbreak\b`, Enabled: true},
	{Category: "crack", Source: `(?:破解|注册机|激活码)|\b(?:keygen|warez|crack)\b`, Enabled: true},
	{Category: "custom", Source: `(?:渗透(?:$|[^率压性力])|提权|木马|勒索|钓鱼(?:网站|网页|邮件|链接|页面|攻击|短信))|\b(?:exploit|ransomware|phishing|ddos|privilege\s+escalation)\b`, Enabled: true},
	{Category: "distill", Source: `(?:蒸馏|思维链)|\b(?:distill|chain[- ]of[- ]thought)\b`, Enabled: true},
	{Category: "jailbreak", Source: `\b(?:jailbreak|dan)\s+mode\b`, Enabled: true},
	{Category: "jailbreak", Source: `\bignore\s+(?:all\s+)?(?:previous|prior|above)\s+(?:instructions|rules|prompts)\b`, Enabled: true},
	{Category: "jailbreak", Source: `\b(?:bypass|disable|remove)\s+(?:your\s+|the\s+|all\s+)?(?:safety|content)\s+(?:filters?|policies|restrictions|limits)\b`, Enabled: true},
	{Category: "jailbreak", Source: jailbreakControlPattern, Enabled: true},
	{Category: "jailbreak", Source: `(?:越狱|破限)(?:模式|提示词|提示|指令)`, Enabled: true},
	{Category: "crack", Source: keygenPattern, Enabled: true},
	{Category: "crack", Source: crackToolPattern, Enabled: true},
	{Category: "crack", Source: `\bcrack(?:ing)?\s+(?:this\s+|the\s+)?(?:software|license|drm|activation)\b`, Enabled: true},
	{Category: "nsfw", Source: `\b(?:write|generate|describe)\s+(?:an?\s+)?(?:explicit|pornographic)\s+(?:sex|sexual)\b`, Enabled: true},
	{Category: "nsfw", Source: `(?:生成|写一段|描述)(?:露骨|色情)(?:内容|小说|场景)`, Enabled: true},
	{Category: "distill", Source: `\bdistill(?:ation|ing)?\s+(?:of\s+)?(?:(?:the|your|a|an|reusable|durable|rollout|hidden|internal|full|complete)\s+)?(?:reasoning(?:\s+traces?)?|chain[- ]of[- ]thoughts?|teacher(?:\s+model)?)\b`, Enabled: true},
	{Category: "distill", Source: `\b(?:extract|export|dump|reveal|harvest|exfiltrate)(?:ing|ed|ion|s)?\s+(?:(?:the|your|a|an|full|hidden|internal|complete|raw|durable|entire)\s+){0,4}(?:chain[- ]of[- ]thoughts?|reasoning\s+traces?|hidden\s+reasoning|internal\s+reasoning|internal\s+monologues?)\b`, Enabled: true},
	{Category: "distill", Source: `\b(?:chain[- ]of[- ]thought|reasoning\s+trace)\s+extraction\b`, Enabled: true},
	{Category: "distill", Source: `(?:思维链蒸馏|推理蒸馏|思考链蒸馏)`, Enabled: true},
	{Category: "distill", Source: `(?:提取|导出|抽取|蒸馏|收割|扒取)\s*(?:出|取)?\s*(?:你的|本人的|完整|全部|隐藏|内部)?\s*(?:的)?\s*(?:思维链|思考链|推理链)`, Enabled: true},
	{Category: "distill", Source: `(?:思维链|思考链|推理链)\s*(?:的)?\s*(?:提取|导出|抽取|蒸馏|收割)`, Enabled: true},
	{Category: "distill", Source: `(?:提取|导出|抽取)\s*(?:出)?\s*(?:隐藏|内部)\s*(?:的)?\s*(?:推理|思维|思考)`, Enabled: true},
	{Category: "custom", Source: `\bphishing\s+kit\b`, Enabled: true},
}

type compiledRule struct {
	category string
	source   string
	re       *regexp.Regexp
}

func compileCaseless(source string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + source)
}

// keywordHit is one keyword match. Start and End locate it in the scanned
// text, so the decision model can be shown the surrounding text.
type keywordHit struct {
	Category string
	Keyword  string
	Rule     string
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

// matchRules returns the first rule that matches text or its
// confusable-folded form.
func matchRules(text string, rules []compiledRule, allow func(category string) bool) (keywordHit, bool) {
	if text == "" {
		return keywordHit{}, false
	}
	folded := foldConfusables(text)
	for _, rule := range rules {
		if !allow(rule.category) {
			continue
		}
		loc := rule.re.FindStringSubmatchIndex(text)
		if loc == nil && folded != text {
			loc = rule.re.FindStringSubmatchIndex(folded)
			if loc != nil {
				text = folded
			}
		}
		if loc == nil {
			continue
		}
		start, end := loc[0], loc[1]
		if rule.source == keygenPattern && len(loc) >= 4 && loc[2] >= 0 {
			start, end = loc[2], loc[3]
		}
		keyword := strings.Join(strings.Fields(text[start:end]), " ")
		if keyword == "" {
			keyword = rule.category
		}
		return keywordHit{Category: rule.category, Keyword: truncateRunes(keyword, 40), Rule: truncateRunes(rule.source, 120),
			Text: text, Start: loc[0], End: loc[1]}, true
	}
	return keywordHit{}, false
}

// excerpt is the match with surrounding context, size runes at most.
func (h keywordHit) excerpt(size int) string {
	if h.Start < 0 || h.End > len(h.Text) || h.Start >= h.End {
		return ""
	}
	before, match, after := []rune(h.Text[:h.Start]), []rune(h.Text[h.Start:h.End]), []rune(h.Text[h.End:])
	if len(match) >= size {
		return string(match[:size])
	}
	room := size - len(match)
	trail := min(len(after), room-min(len(before), room/2))
	lead := min(len(before), room-trail)
	return string(before[len(before)-lead:]) + string(match) + string(after[:trail])
}
