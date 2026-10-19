package risk

import (
	"bytes"
	"encoding/base64"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PreprocessVersion changes whenever NormalizeText, StripReminders or
// ExpandBase64 change their output; it is part of every cache key and stored
// record so older entries never match newer semantics.
const PreprocessVersion = 1

const (
	reminderOpen  = "<system-reminder>"
	reminderClose = "</system-reminder>"

	base64MinRun     = 32
	base64MaxRun     = 64 * 1024
	base64MaxDecoded = 64 * 1024
	base64MaxPerPass = 8
	base64MaxPasses  = 3
)

func isAuditSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\uFEFF'
}

// NormalizeText lowercases, folds whitespace runs into one space, drops the
// whitespace after a backslash and trims.
func NormalizeText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	pendingSpace := false
	afterBackslash := false
	for _, r := range strings.ToLower(text) {
		if isAuditSpace(r) {
			if !afterBackslash {
				pendingSpace = true
			}
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
		afterBackslash = r == '\\'
	}
	return b.String()
}

// StripReminders removes <system-reminder> blocks, matching the tags without
// regard to ASCII case. An unclosed block drops the rest of the text.
func StripReminders(text string) string {
	lower := asciiLower(text)
	if !strings.Contains(lower, reminderOpen) {
		return text
	}
	var b strings.Builder
	rest, restLower := text, lower
	for {
		open := strings.Index(restLower, reminderOpen)
		if open < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:open])
		rest, restLower = rest[open+len(reminderOpen):], restLower[open+len(reminderOpen):]
		closeAt := strings.Index(restLower, reminderClose)
		if closeAt < 0 {
			break
		}
		rest, restLower = rest[closeAt+len(reminderClose):], restLower[closeAt+len(reminderClose):]
	}
	return strings.TrimSpace(b.String())
}

func asciiLower(text string) string {
	buf := []byte(text)
	for i, ch := range buf {
		if ch >= 'A' && ch <= 'Z' {
			buf[i] = ch + 32
		}
	}
	return string(buf)
}

func isBase64Byte(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '+' || ch == '/' || ch == '='
}

func looksLikeText(text string) bool {
	total, printable, letters := 0, 0, 0
	for _, r := range text {
		total++
		if r == '\n' || r == '\r' || r == '\t' {
			printable++
			continue
		}
		if r == ' ' || (r > ' ' && r != 0x7f && unicode.IsPrint(r)) {
			printable++
			if unicode.IsLetter(r) {
				letters++
			}
		}
	}
	return letters > 0 && printable*10 >= total*9
}

func decodeBase64Text(run string) (string, bool) {
	if len(run)%4 != 0 {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(run)
	if err != nil || len(raw) == 0 || !utf8.Valid(raw) {
		return "", false
	}
	if base64.StdEncoding.EncodeToString(raw) != run {
		return "", false
	}
	text := string(raw)
	if !looksLikeText(text) {
		return "", false
	}
	return text, true
}

// ExpandBase64 replaces long standard-base64 runs that decode to readable
// text with the decoded text. A run larger than the per-run or total budget is
// kept verbatim and reported as incomplete coverage instead of being cut.
func ExpandBase64(text string) (string, bool) {
	decodedTotal := 0
	return expandBase64(text, &decodedTotal)
}

func expandBase64(text string, decodedTotal *int) (string, bool) {
	current := text
	incomplete := false
	for range base64MaxPasses {
		var out bytes.Buffer
		changed := false
		segments := 0
		last := 0
		for i := 0; i < len(current); {
			if !isBase64Byte(current[i]) {
				i++
				continue
			}
			j := i
			for j < len(current) && isBase64Byte(current[j]) {
				j++
			}
			run := current[i:j]
			if len(run) >= base64MinRun {
				if len(run) > base64MaxRun || segments >= base64MaxPerPass {
					incomplete = true
				} else if plain, ok := decodeBase64Text(run); ok {
					if *decodedTotal+len(plain) > base64MaxDecoded {
						incomplete = true
					} else {
						out.WriteString(current[last:i])
						out.WriteString(plain)
						last = j
						*decodedTotal += len(plain)
						segments++
						changed = true
					}
				}
			}
			i = j
		}
		if !changed {
			break
		}
		out.WriteString(current[last:])
		current = out.String()
	}
	return current, incomplete
}

// PrepareText applies the configured reminder and base64 passes.
func PrepareText(text string, stripReminders, expandBase64 bool) (string, bool) {
	out := text
	if stripReminders {
		out = StripReminders(out)
	}
	incomplete := false
	if expandBase64 {
		out, incomplete = ExpandBase64(out)
	}
	return strings.TrimSpace(out), incomplete
}
