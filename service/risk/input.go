package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

// Attachments are never audited: images, audio and files are skipped when
// reading text and replaced by a placeholder in stored evidence.
var mediaPartTypes = map[string]bool{
	"image_url": true, "image": true, "input_image": true, "input_file": true,
	"file": true, "document": true, "input_audio": true, "audio": true,
}

func isToolPayload(obj map[string]any) bool {
	kind, _ := obj["type"].(string)
	return kind == "tool_result" || kind == "function_call_output" || strings.HasSuffix(kind, "_tool_result")
}

// contentText returns the authored text of a content value. Tool payloads
// and attachments contribute nothing.
func contentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, part := range v {
			if text := contentText(part); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if isToolPayload(v) {
			return ""
		}
		if kind, _ := v["type"].(string); mediaPartTypes[kind] {
			return ""
		}
		if text, ok := v["text"].(string); ok {
			return text
		}
		switch inner := v["content"].(type) {
		case string:
			return inner
		case []any:
			return contentText(inner)
		}
	}
	return ""
}

// responsesInputTexts reads the Responses input items of one role; items
// without a role count as user input.
func responsesInputTexts(input any, role string) []string {
	switch v := input.(type) {
	case string:
		if v == "" || role != "user" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s != "" && role == "user" {
					out = append(out, s)
				}
				continue
			}
			obj, ok := item.(map[string]any)
			if !ok || isToolPayload(obj) {
				continue
			}
			itemRole, _ := obj["role"].(string)
			if itemRole == "" {
				itemRole = "user"
			}
			if !strings.EqualFold(itemRole, role) {
				continue
			}
			var source any = obj
			if c, exists := obj["content"]; exists {
				source = c
			}
			if text := contentText(source); text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func roleTexts(messages []any, roles ...string) []string {
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		obj, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := obj["role"].(string)
		if !slices.Contains(roles, role) {
			continue
		}
		if text := contentText(obj["content"]); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// legacyPrompt reads the completions prompt; token arrays cannot be audited.
func legacyPrompt(prompt any) []string {
	var texts []string
	switch v := prompt.(type) {
	case string:
		if v != "" {
			texts = append(texts, v)
		}
	case []any:
		for _, item := range v {
			if p, ok := item.(string); ok && p != "" {
				texts = append(texts, p)
			}
		}
	}
	return texts
}

func systemFieldTexts(value any) []string {
	switch v := value.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []any:
		if text := contentText(v); text != "" {
			return []string{text}
		}
	}
	return nil
}

func intField(body map[string]any, keys ...string) *int {
	for _, key := range keys {
		if n, ok := body[key].(float64); ok {
			value := int(math.Min(math.Max(n, math.MinInt32), math.MaxInt32))
			return &value
		}
	}
	return nil
}

// auditTextBudget bounds, in runes, the system and user text the audit reads,
// so a padded request costs a bounded amount of CPU before billing.
const auditTextBudget = 256 * 1024

// tailRunes keeps the last limit Unicode code points without splitting UTF-8.
func tailRunes(text string, limit int) (string, bool) {
	start := len(text)
	for start > 0 && limit > 0 {
		_, size := utf8.DecodeLastRuneInString(text[:start])
		start -= size
		limit--
	}
	return text[start:], start > 0
}

// boundText keeps the first and the last runes of a text longer than limit.
func boundText(text string, limit int) (string, bool) {
	if len(text) <= limit || utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	if limit <= 1 {
		return "", true
	}
	head := (limit - 1) / 2
	start := 0
	for range head {
		_, size := utf8.DecodeRuneInString(text[start:])
		start += size
	}
	end := len(text)
	for range limit - 1 - head {
		_, size := utf8.DecodeLastRuneInString(text[:end])
		end -= size
	}
	return text[:start] + "\n" + text[end:], true
}

// ExtractInput builds the audit view of a raw request body: chat completions,
// completions, Claude messages, Responses, or a Responses WebSocket
// response.create payload.
func ExtractInput(body []byte, headers http.Header) (*Input, error) {
	var doc map[string]any
	if err := common.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("request body is not a JSON object")
	}
	messages, _ := doc["messages"].([]any)
	systems := make([]string, 0, 4)
	for _, text := range slices.Concat(systemFieldTexts(doc["system"]), systemFieldTexts(doc["instructions"]), roleTexts(messages, "system", "developer")) {
		if !slices.Contains(systems, text) {
			systems = append(systems, text)
		}
	}
	users := slices.Concat(roleTexts(messages, "user"), responsesInputTexts(doc["input"], "user"), legacyPrompt(doc["prompt"]))
	in := &Input{
		Raw:       body,
		Official:  isOfficialClaudeCodeTraffic(doc, headers),
		MaxTokens: intField(doc, "max_tokens", "max_output_tokens", "max_completion_tokens", "max_new_tokens"),
	}
	in.Model, _ = doc["model"].(string)
	system, _ := boundText(strings.Join(systems, "\n"), auditTextBudget)
	if system != "" {
		in.Systems = []string{system}
	}
	if len(users) > 0 {
		in.LatestUser, _ = boundText(users[len(users)-1], auditTextBudget)
	}
	switch {
	case messages != nil:
		in.MessageCount = len(messages)
	case doc["input"] != nil || doc["prompt"] != nil:
		in.MessageCount = 1
	}
	tools, _ := doc["tools"].([]any)
	in.ToolCount = len(tools)
	if thinking, ok := doc["thinking"].(map[string]any); ok {
		in.Thinking.Type, _ = thinking["type"].(string)
		in.Thinking.Display, _ = thinking["display"].(string)
		if budget := intField(thinking, "budget_tokens"); budget != nil {
			in.Thinking.BudgetTokens = *budget
		}
	}
	in.Thinking.ReasoningEffort, _ = doc["reasoning_effort"].(string)
	if reasoning, ok := doc["reasoning"].(map[string]any); ok && in.Thinking.ReasoningEffort == "" {
		in.Thinking.ReasoningEffort, _ = reasoning["effort"].(string)
	}
	if n := intField(doc, "n", "num_generations"); n != nil {
		in.Thinking.N = *n
	}
	sum := sha256.Sum256([]byte(NormalizeText(in.distillJoined())))
	in.ContentHash = hex.EncodeToString(sum[:])
	return in, nil
}

// distillJoined is the system text plus the latest user text.
func (in *Input) distillJoined() string {
	parts := slices.Clone(in.Systems)
	if in.LatestUser != "" {
		parts = append(parts, in.LatestUser)
	}
	return strings.Join(parts, "\n")
}

// auditTexts are the prepared system and latest-user texts that keyword rules
// scan and the decision model reads.
type auditTexts struct {
	system string
	user   string
}

func (in *Input) auditTexts(stripReminders, expandBase64 bool) auditTexts {
	var t auditTexts
	t.system, _ = PrepareText(strings.Join(in.Systems, "\n"), false, expandBase64)
	t.user, _ = PrepareText(in.LatestUser, stripReminders, expandBase64)
	t.system, t.user = NormalizeText(t.system), NormalizeText(t.user)
	return t
}

// scan is the text keyword rules match: system first, then the user.
func (t auditTexts) scan() string {
	if t.system == "" || t.user == "" {
		return t.system + t.user
	}
	return t.system + "\n" + t.user
}

const reviewExcerptRunes = 1000

// modelDocument is what the decision model reads: the newest current-user
// text and as much system context as fits in size runes, labelled by role.
// A keyword match outside that window is added as a bounded excerpt.
// OpenAI Moderation classifies content only, so it gets the user text and,
// for a match in the system text, that excerpt.
func (t auditTexts) modelDocument(size int, hit keywordHit, moderation bool) string {
	userStart := len(t.scan()) - len(t.user)
	inUser := hit.Start >= userStart
	part := keywordHit{Text: t.system, Start: hit.Start, End: hit.End}
	if inUser {
		part = keywordHit{Text: t.user, Start: hit.Start - userStart, End: hit.End - userStart}
	}
	userTail, _ := tailRunes(t.user, size)
	userCovered := inUser && part.Start >= len(t.user)-len(userTail)
	if moderation {
		if userCovered {
			return userTail
		}
		return strings.TrimSpace(part.excerpt(reviewExcerptRunes) + "\n\n" + userTail)
	}
	room := size - utf8.RuneCountInString(userTail)
	systemTail, _ := tailRunes(t.system, max(room, 0))
	excerpt := ""
	if !userCovered && (inUser || part.Start < len(t.system)-len(systemTail)) {
		excerpt = part.excerpt(reviewExcerptRunes)
		systemTail, _ = tailRunes(t.system, max(room-utf8.RuneCountInString(excerpt), 0))
	}
	var b strings.Builder
	if excerpt != "" {
		b.WriteString("[review_context]\n" + excerpt + "\n[/review_context]\n")
	}
	if systemTail != "" {
		b.WriteString("[system_context]\n" + systemTail + "\n[/system_context]\n")
	}
	b.WriteString("[current_user]\n" + userTail + "\n[/current_user]")
	return b.String()
}

// evidenceLimit stays below MySQL 5.7's default 4 MB max_allowed_packet.
const evidenceLimit = 3 << 20

var dataURLPattern = regexp.MustCompile(`^data:[^,]{0,128},`)

// EvidenceBody returns the request JSON kept with a violation. A request
// without attachments is kept byte for byte; otherwise attachments become a
// size placeholder (object keys are re-serialized in sorted order). The
// result is cut at evidenceLimit bytes, and the original size returned.
func EvidenceBody(raw []byte) (string, int, bool) {
	var doc any
	text := string(raw)
	if common.Unmarshal(raw, &doc) == nil && omitAttachments(&doc) {
		encoded, _ := common.Marshal(doc)
		text = string(encoded)
	}
	if len(text) <= evidenceLimit {
		return text, len(raw), false
	}
	cut := evidenceLimit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], len(raw), true
}

func attachmentPlaceholder(size int) string {
	return "[attachment omitted: " + strconv.Itoa(size) + " bytes]"
}

// omitAttachments replaces attachment parts and data URLs in place and
// reports whether anything changed.
func omitAttachments(value *any) bool {
	changed := false
	switch v := (*value).(type) {
	case string:
		if len(v) > 256 && dataURLPattern.MatchString(v) {
			*value, changed = attachmentPlaceholder(len(v)), true
		}
	case []any:
		for i := range v {
			changed = omitAttachments(&v[i]) || changed
		}
	case map[string]any:
		if kind, _ := v["type"].(string); mediaPartTypes[kind] {
			encoded, _ := common.Marshal(v)
			*value = map[string]any{"type": kind, "omitted": attachmentPlaceholder(len(encoded))}
			return true
		}
		for key, inner := range v {
			if key == "inline_data" || key == "inlineData" {
				encoded, _ := common.Marshal(inner)
				v[key], changed = attachmentPlaceholder(len(encoded)), true
				continue
			}
			changed = omitAttachments(&inner) || changed
			v[key] = inner
		}
	}
	return changed
}

var legacyUserIDPattern = regexp.MustCompile(`^user_(.*?)_account_(.*?)_session_(.*)$`)

// parseUserID reads metadata.user_id in its object, JSON string or legacy
// "user_<device>_account_<uuid>_session_<sid>" form.
func parseUserID(raw any) (deviceID, sessionID string, ok bool) {
	read := func(obj map[string]any) (string, string) {
		device, _ := obj["device_id"].(string)
		if device == "" {
			device, _ = obj["deviceId"].(string)
		}
		session, _ := obj["session_id"].(string)
		if session == "" {
			session, _ = obj["sessionId"].(string)
		}
		return strings.TrimSpace(device), strings.TrimSpace(session)
	}
	switch v := raw.(type) {
	case map[string]any:
		d, s := read(v)
		return d, s, true
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return "", "", false
		}
		var obj map[string]any
		if strings.HasPrefix(text, "{") && common.UnmarshalJsonStr(text, &obj) == nil {
			d, s := read(obj)
			return d, s, true
		}
		if m := legacyUserIDPattern.FindStringSubmatch(text); m != nil {
			return strings.TrimSpace(m[1]), strings.TrimSpace(m[3]), true
		}
	}
	return "", "", false
}

var (
	claudeCLIUserAgent      = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+\.\d+`)
	legacyOfficialUserID    = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account_([a-fA-F0-9-]*)_session_([a-fA-F0-9-]{36})$`)
	officialEntrypoint      = regexp.MustCompile(`(?i)cc_entrypoint\s*=\s*(cli|sdk-cli|vscode|cowork|desktop|claude-vscode|claude-desktop(?:-3p)?)\b`)
	localAgentEntrypoint    = regexp.MustCompile(`(?i)cc_entrypoint\s*=\s*local-agent\b`)
	officialIdentityVariant = regexp.MustCompile(`(?i)You are Claude Code\b|Claude Agent SDK`)
	billingHeaderLine       = regexp.MustCompile(`(?i)x-anthropic-billing-header:`)
	ompRoleBlock            = regexp.MustCompile(`(?m)^ROLE\s*\n\s*=+`)
	ompProjectBlock         = regexp.MustCompile(`(?m)^PROJECT\s*\n\s*=+`)
	ompHarnessName          = regexp.MustCompile(`(?i)\b(?:oh my pi|pi coding harness|omp coding harness)\b`)
	desktopLocalAgentUA     = regexp.MustCompile(`[ (,]local-agent[,)]`)
	desktopAgentSDKUA       = regexp.MustCompile(`[ (,]agent-sdk/\d+\.\d+\.\d+[,)]`)
	semverOnly              = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

var officialChildPromptPrefixes = []string{
	"You are Claude Code, Anthropic's official CLI for Claude",
	"You are a Claude agent, built on Anthropic's Claude Agent SDK",
	"You are a file search specialist for Claude Code",
	"You are a helpful AI assistant tasked with summarizing conversations",
	"You are an interactive CLI tool that helps users",
	"You are an assistant for performing a web search tool use",
}

func systemBlockTexts(system any) []string {
	switch v := system.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{v}
		}
	case []any:
		out := make([]string, 0, len(v))
		for _, block := range v {
			var text string
			switch b := block.(type) {
			case string:
				text = b
			case map[string]any:
				text, _ = b["text"].(string)
			}
			if strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	case map[string]any:
		if text, ok := v["text"].(string); ok && strings.TrimSpace(text) != "" {
			return []string{text}
		}
	}
	return nil
}

func looksLikeOhMyPiSystem(text string, headers http.Header) bool {
	if text == "" {
		return false
	}
	ua := strings.TrimSpace(headers.Get("User-Agent"))
	desktop := claudeCLIUserAgent.MatchString(ua) &&
		desktopLocalAgentUA.MatchString(ua) &&
		desktopAgentSDKUA.MatchString(ua) &&
		headers.Get("Anthropic-Client-Platform") == "desktop_app" &&
		semverOnly.MatchString(strings.TrimSpace(headers.Get("Anthropic-Client-Version")))
	lower := strings.ToLower(text)
	switch {
	case localAgentEntrypoint.MatchString(text) && !desktop:
		return true
	case strings.Contains(lower, "<system-conventions>") && strings.Contains(lower, "oh my pi"):
		return true
	case strings.Contains(lower, "<system-conventions>") && strings.Contains(lower, "<workstation>"):
		return true
	case ompRoleBlock.MatchString(text) && ompProjectBlock.MatchString(text):
		return true
	}
	return ompHarnessName.MatchString(text)
}

func isValidOfficialUserID(raw any) bool {
	switch v := raw.(type) {
	case map[string]any:
		device, session, _ := parseUserID(v)
		return device != "" && session != ""
	case string:
		text := strings.TrimSpace(v)
		if strings.HasPrefix(text, "{") {
			device, session, ok := parseUserID(text)
			return ok && device != "" && session != ""
		}
		return legacyOfficialUserID.MatchString(text)
	}
	return false
}

func hasOpenAIToolShape(doc map[string]any) bool {
	tools, _ := doc["tools"].([]any)
	for _, t := range tools {
		obj, ok := t.(map[string]any)
		if !ok {
			continue
		}
		_, hasName := obj["name"]
		if obj["type"] == "function" || (obj["function"] != nil && !hasName) {
			return true
		}
	}
	choice, ok := doc["tool_choice"].(map[string]any)
	if !ok {
		return false
	}
	kind, _ := choice["type"].(string)
	return strings.EqualFold(kind, "function")
}

// isOfficialClaudeCodeTraffic is a weak client signal used only to relax the
// distill needles; it is not an identity check.
func isOfficialClaudeCodeTraffic(doc map[string]any, headers http.Header) bool {
	if headers == nil || !claudeCLIUserAgent.MatchString(strings.TrimSpace(headers.Get("User-Agent"))) {
		return false
	}
	blocks := systemBlockTexts(doc["system"])
	text, _ := boundText(strings.Join(blocks, "\n"), auditTextBudget)
	if looksLikeOhMyPiSystem(text, headers) {
		return false
	}
	metadata, _ := doc["metadata"].(map[string]any)
	if metadata == nil || !isValidOfficialUserID(metadata["user_id"]) {
		return false
	}
	if hasOpenAIToolShape(doc) || text == "" {
		return false
	}
	if billingHeaderLine.MatchString(text) || officialIdentityVariant.MatchString(text) {
		return true
	}
	if officialEntrypoint.MatchString(text) && !localAgentEntrypoint.MatchString(text) {
		return true
	}
	for _, block := range blocks {
		trimmed := strings.TrimLeft(block, " \t\r\n")
		if slices.ContainsFunc(officialChildPromptPrefixes, func(prefix string) bool { return strings.HasPrefix(trimmed, prefix) }) {
			return true
		}
	}
	return false
}

var gptSkipPattern = regexp.MustCompile(`(?i)^(whisper|tts-|dall-e|chatgpt-image|gpt-image|text-embedding|text-moderation|omni-moderation|davinci|babbage|curie)`)

// IsGPTSeriesModel reports whether the model id after the last "/" is a GPT
// chat model, mirroring vm2api's platform split.
func IsGPTSeriesModel(model string) bool {
	id := strings.TrimSpace(model)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	return strings.HasPrefix(strings.ToLower(id), "gpt") && !gptSkipPattern.MatchString(id)
}
