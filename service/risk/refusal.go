package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// FingerprintVersion and SignatureVersion version the refusal keys; changing
// either algorithm requires a new version so older records stop matching.
const (
	FingerprintVersion = 1
	SignatureVersion   = 1
)

const (
	similarityMinRunes = 512
	shingleRunes       = 24
	shingleStep        = 12
	shingleMax         = 4096
	signatureSlots     = 128
)

// Fingerprint is the exact refusal key: model, normalized system+user text and
// tool names. Sampling parameters such as stream or max_tokens are excluded.
func Fingerprint(in *Input) string {
	encoded, _ := common.Marshal(struct {
		Version int      `json:"fingerprint_version"`
		Model   string   `json:"model"`
		Prompt  string   `json:"prompt"`
		Tools   []string `json:"tools"`
	}{FingerprintVersion, strings.ToLower(strings.TrimSpace(in.Model)), NormalizeText(in.fingerprintPrompt()), in.ToolNames})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func fnv1a(text string) uint32 {
	hash := uint32(0x811c9dc5)
	for i := 0; i < len(text); i++ {
		hash ^= uint32(text[i])
		hash *= 0x01000193
	}
	return hash
}

func shingles(runes []rune) []string {
	if len(runes) <= shingleRunes {
		return []string{string(runes)}
	}
	raw := (len(runes)-shingleRunes)/shingleStep + 1
	step := shingleStep
	if raw > shingleMax {
		step = shingleStep * ((raw + shingleMax - 1) / shingleMax)
	}
	out := make([]string, 0, min(raw, shingleMax))
	for i := 0; i+shingleRunes <= len(runes); i += step {
		out = append(out, string(runes[i:i+shingleRunes]))
	}
	return out
}

// Signature is a 128-slot MinHash of the user text, or nil when the text is
// too short for near-duplicate matching.
func Signature(document string) []uint32 {
	runes := []rune(NormalizeText(document))
	if len(runes) < similarityMinRunes {
		return nil
	}
	sig := make([]uint32, signatureSlots)
	for i := range sig {
		sig[i] = ^uint32(0)
	}
	for _, gram := range shingles(runes) {
		h1 := fnv1a(gram)
		h2 := fnv1a("\x01"+gram) | 1
		for i := range signatureSlots {
			mixed := h1 + uint32(i)*h2
			if mixed < sig[i] {
				sig[i] = mixed
			}
		}
	}
	return sig
}

func EncodeSignature(sig []uint32) string {
	if len(sig) != signatureSlots {
		return ""
	}
	var b strings.Builder
	for i, v := range sig {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatUint(uint64(v), 10))
	}
	return b.String()
}

func DecodeSignature(raw string) []uint32 {
	if raw == "" {
		return nil
	}
	sig := make([]uint32, 0, signatureSlots)
	for part := range strings.SplitSeq(raw, ",") {
		if len(sig) >= signatureSlots {
			return nil
		}
		v, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil
		}
		sig = append(sig, uint32(v))
	}
	if len(sig) != signatureSlots {
		return nil
	}
	return sig
}

// Similarity is the estimated Jaccard similarity of two signatures.
func Similarity(a, b []uint32) float64 {
	if len(a) != signatureSlots || len(b) != signatureSlots {
		return 0
	}
	same := 0
	for i := range a {
		if a[i] == b[i] {
			same++
		}
	}
	return float64(same) / signatureSlots
}

var contentPolicyCodes = map[string]bool{
	"content_policy":           true,
	"content_filter":           true,
	"content_filter_refusal":   true,
	"cyber_policy":             true,
	"moderation_blocked":       true,
	"safety_violation":         true,
	"usage_policy":             true,
	"content_policy_violation": true,
}

var usagePolicyMessage = regexp.MustCompile(`(?i)(?:violat(?:e|es|ed|ion of)|blocked (?:by|for)|rejected (?:by|for)|due to) (?:our |the )?(?:usage|content|safety) polic(?:y|ies)|content_filter_refusal`)

// IsContentPolicyCode reports whether an upstream error code names a content
// policy refusal.
func IsContentPolicyCode(code string) bool {
	return contentPolicyCodes[strings.ToLower(strings.TrimSpace(code))]
}

// ClassifyUpstreamError recognizes an explicit content refusal in an upstream
// error. Authentication, rate limit, server and network errors never qualify
// unless they carry a content-policy code.
func ClassifyUpstreamError(code, errType, message string) (string, bool) {
	for _, c := range []string{code, errType} {
		if IsContentPolicyCode(c) {
			return "upstream_error_code=" + strings.ToLower(strings.TrimSpace(c)), true
		}
	}
	if usagePolicyMessage.MatchString(message) {
		return "upstream_usage_policy", true
	}
	return "", false
}

func responseHasRefusal(response map[string]any) (string, bool) {
	if status, _ := response["status"].(string); status == "incomplete" {
		if details, ok := response["incomplete_details"].(map[string]any); ok {
			if reason, _ := details["reason"].(string); reason == "content_filter" {
				return "openai_responses_incomplete=content_filter", true
			}
		}
	}
	output, _ := response["output"].([]any)
	for _, item := range output {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		parts, _ := obj["content"].([]any)
		for _, part := range parts {
			p, ok := part.(map[string]any)
			if !ok || p["type"] != "refusal" {
				continue
			}
			if text, _ := p["refusal"].(string); strings.TrimSpace(text) != "" {
				return "openai_responses_refusal", true
			}
		}
	}
	return "", false
}

// ClassifyResponsesPayload recognizes a content refusal in a Responses API
// response object or stream/WebSocket event.
func ClassifyResponsesPayload(body []byte) (string, bool) {
	var doc map[string]any
	if common.Unmarshal(body, &doc) != nil || doc == nil {
		return "", false
	}
	if response, ok := doc["response"].(map[string]any); ok {
		if reason, hit := responseHasRefusal(response); hit {
			return reason, true
		}
		if errObj, ok := response["error"].(map[string]any); ok {
			code, _ := errObj["code"].(string)
			message, _ := errObj["message"].(string)
			if reason, hit := ClassifyUpstreamError(code, "", message); hit {
				return reason, true
			}
		}
	}
	if reason, hit := responseHasRefusal(doc); hit {
		return reason, true
	}
	if errObj, ok := doc["error"].(map[string]any); ok {
		code, _ := errObj["code"].(string)
		errType, _ := errObj["type"].(string)
		message, _ := errObj["message"].(string)
		return ClassifyUpstreamError(code, errType, message)
	}
	return "", false
}
