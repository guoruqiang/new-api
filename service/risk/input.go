package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	deviceIDMin = 8
	deviceIDMax = 200
)

type textMode int

const (
	// textDistill drops tool_result parts inside content arrays, like the
	// vm2api distill extractor.
	textDistill textMode = iota
	// textAll keeps tool payloads; refusal similarity reads them.
	textAll
	// textAuthored drops every tool payload; hard rules and the model read it.
	textAuthored
)

var mediaPartTypes = map[string]bool{
	"image_url": true, "image": true, "input_image": true, "input_file": true,
	"file": true, "document": true, "input_audio": true, "audio": true,
}

func isToolPayload(obj map[string]any) bool {
	kind, _ := obj["type"].(string)
	return kind == "tool_result" || kind == "function_call_output" || strings.HasSuffix(kind, "_tool_result")
}

func contentText(content any, mode textMode) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, part := range v {
			if obj, ok := part.(map[string]any); ok && mode == textDistill && obj["type"] == "tool_result" {
				continue
			}
			if text := contentText(part, mode); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if mode != textAll && isToolPayload(v) {
			return ""
		}
		if text, ok := v["text"].(string); ok {
			return text
		}
		if mode == textAll && isToolPayload(v) {
			if output, exists := v["output"]; exists {
				return contentText(output, mode)
			}
		}
		switch inner := v["content"].(type) {
		case string:
			return inner
		case []any:
			return contentText(inner, mode)
		}
	}
	return ""
}

func responsesInputTexts(input any, mode textMode) []string {
	switch v := input.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if s != "" {
					out = append(out, s)
				}
				continue
			}
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if mode != textAll && isToolPayload(obj) {
				continue
			}
			role, _ := obj["role"].(string)
			if role != "" && !strings.EqualFold(role, "user") {
				continue
			}
			var source any = obj
			if c, exists := obj["content"]; exists {
				source = c
			}
			if text := contentText(source, mode); text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func roleTexts(messages []any, roles []string, mode textMode) []string {
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
		if text := contentText(obj["content"], mode); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// legacyPrompt reads the completions prompt; token arrays cannot be audited.
func legacyPrompt(prompt any) (texts []string, tokenOnly bool) {
	switch v := prompt.(type) {
	case string:
		if v != "" {
			texts = append(texts, v)
		}
	case []any:
		for _, item := range v {
			switch p := item.(type) {
			case string:
				if p != "" {
					texts = append(texts, p)
				}
			default:
				tokenOnly = true
			}
		}
	}
	return texts, tokenOnly
}

func userTexts(body map[string]any, messages []any, mode textMode, prompt []string) []string {
	out := roleTexts(messages, []string{"user"}, mode)
	out = append(out, responsesInputTexts(body["input"], mode)...)
	return append(out, prompt...)
}

func systemFieldTexts(value any) []string {
	switch v := value.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []any:
		if text := contentText(v, textDistill); text != "" {
			return []string{text}
		}
	}
	return nil
}

func hasMedia(value any) bool {
	switch v := value.(type) {
	case []any:
		return slices.ContainsFunc(v, hasMedia)
	case map[string]any:
		if kind, _ := v["type"].(string); mediaPartTypes[kind] {
			return true
		}
		for _, inner := range v {
			if hasMedia(inner) {
				return true
			}
		}
	}
	return false
}

func intField(body map[string]any, keys ...string) *int {
	for _, key := range keys {
		if n, ok := body[key].(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
			value := int(math.Min(math.Max(n, math.MinInt32), math.MaxInt32))
			return &value
		}
	}
	return nil
}

func toolNames(tools []any) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		obj, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, _ := obj["name"].(string)
		if name == "" {
			if fn, ok := obj["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
		}
		name = strings.TrimSpace(name)
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
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

func inboundDeviceID(body map[string]any, headers http.Header) string {
	var id string
	if metadata, ok := body["metadata"].(map[string]any); ok {
		id, _, _ = parseUserID(metadata["user_id"])
	}
	if id == "" {
		if explicit, ok := body["device_id"].(string); ok {
			id = strings.TrimSpace(explicit)
		}
	}
	if id == "" && headers != nil {
		id = strings.TrimSpace(headers.Get("X-Kin-Device-Id"))
	}
	if n := len([]rune(id)); n < deviceIDMin || n > deviceIDMax {
		return ""
	}
	return id
}

// ExtractInput builds the audit view of a raw request body. The body is the
// client JSON for chat completions, completions, Claude messages, Responses,
// or a Responses WebSocket response.create payload.
func ExtractInput(body []byte, headers http.Header) (*Input, error) {
	var doc map[string]any
	if err := common.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("request body is not a JSON object")
	}
	messages, _ := doc["messages"].([]any)
	prompt, tokenOnly := legacyPrompt(doc["prompt"])

	systems := make([]string, 0, 4)
	for _, text := range append(append(systemFieldTexts(doc["system"]), systemFieldTexts(doc["instructions"])...),
		roleTexts(messages, []string{"system", "developer"}, textDistill)...) {
		if !slices.Contains(systems, text) {
			systems = append(systems, text)
		}
	}

	in := &Input{
		Systems:         systems,
		DistillUsers:    userTexts(doc, messages, textDistill, prompt),
		UserTurns:       userTexts(doc, messages, textAll, prompt),
		UserAuthored:    userTexts(doc, messages, textAuthored, prompt),
		PromptTokenOnly: tokenOnly,
		HasMedia:        hasMedia(messages) || hasMedia(doc["input"]) || hasMedia(doc["system"]),
		DeviceID:        inboundDeviceID(doc, headers),
		Official:        isOfficialClaudeCodeTraffic(doc, headers),
		MaxTokens:       intField(doc, "max_tokens", "max_output_tokens", "max_completion_tokens", "max_new_tokens"),
	}
	in.Model, _ = doc["model"].(string)
	if len(in.DistillUsers) > 0 {
		in.LatestUser = in.DistillUsers[len(in.DistillUsers)-1]
	}
	switch {
	case messages != nil:
		in.MessageCount = len(messages)
	case doc["input"] != nil || doc["prompt"] != nil:
		in.MessageCount = 1
	}
	tools, _ := doc["tools"].([]any)
	in.ToolCount = len(tools)
	in.ToolNames = toolNames(tools)
	if previous, _ := doc["previous_response_id"].(string); previous != "" {
		in.RefsHistory = true
	}
	if doc["conversation"] != nil {
		in.RefsHistory = true
	}
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

	digest, err := common.Marshal(struct {
		Systems []string `json:"s"`
		Users   []string `json:"u"`
		Tools   []string `json:"t"`
	}{in.Systems, in.UserTurns, in.ToolNames})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(digest)
	in.Digest = hex.EncodeToString(sum[:])
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

// fingerprintPrompt is the system text plus every user text.
func (in *Input) fingerprintPrompt() string {
	return strings.Join(append(slices.Clone(in.Systems), in.DistillUsers...), "\n")
}

func normalizedDocument(texts []string) string {
	seen := make(map[string]bool, len(texts))
	parts := make([]string, 0, len(texts))
	for _, text := range texts {
		norm := NormalizeText(text)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		parts = append(parts, norm)
	}
	return strings.Join(parts, "\n")
}

// preparedDocument strips reminders and expands base64 on the raw text before
// normalizing; normalizing first would lowercase base64 and break decoding.
func preparedDocument(texts []string, stripReminders, expand bool, decodedTotal *int) (string, bool) {
	prepared := make([]string, 0, len(texts))
	incomplete := false
	for _, text := range texts {
		out := text
		if stripReminders {
			out = StripReminders(out)
		}
		partial := false
		if expand {
			out, partial = expandBase64(out, decodedTotal)
		}
		incomplete = incomplete || partial
		prepared = append(prepared, out)
	}
	return normalizedDocument(prepared), incomplete
}

// RuleDocument is the user-authored text that hard rules scan.
func (in *Input) RuleDocument(stripReminders, expandBase64 bool) (string, bool) {
	decodedTotal := 0
	return preparedDocument(in.UserAuthored, stripReminders, expandBase64, &decodedTotal)
}

// ModelDocument is what the decision model reads: system text, then the
// user-authored text.
func (in *Input) ModelDocument(stripReminders, expandBase64 bool) (string, bool) {
	decodedTotal := 0
	systems, systemsPartial := preparedDocument(in.Systems, stripReminders, expandBase64, &decodedTotal)
	users, usersPartial := preparedDocument(in.UserAuthored, stripReminders, expandBase64, &decodedTotal)
	incomplete := systemsPartial || usersPartial
	switch {
	case systems == "":
		return users, incomplete
	case users == "":
		return systems, incomplete
	}
	return systems + "\n" + users, incomplete
}

// SimilarityDocument is the user text, tool payloads included, that the
// near-duplicate refusal signature hashes.
func (in *Input) SimilarityDocument() string {
	return normalizedDocument(in.UserTurns)
}

// Preview is a short single-line excerpt for administrators.
func (in *Input) Preview() string {
	text := in.LatestUser
	if text == "" {
		text = strings.Join(in.DistillUsers, " ")
	}
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > 240 {
		text = string(runes[:240])
	}
	return text
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
	text := strings.Join(blocks, "\n")
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
