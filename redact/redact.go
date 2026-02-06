package redact

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"strings"
)

// secretPattern matches high-entropy strings that may be secrets.
var secretPattern = regexp.MustCompile(`[A-Za-z0-9/+_=-]{10,}`)

const entropyThreshold = 4.5

// String replaces high-entropy strings matching secretPattern with [REDACTED].
func String(s string) string {
	locs := secretPattern.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, loc := range locs {
		b.WriteString(s[prev:loc[0]])
		match := s[loc[0]:loc[1]]
		if isSecret(match) {
			b.WriteString("[REDACTED]")
		} else {
			b.WriteString(match)
		}
		prev = loc[1]
	}
	b.WriteString(s[prev:])
	return b.String()
}

// Bytes is a convenience wrapper around String for []byte content.
func Bytes(b []byte) []byte {
	s := string(b)
	redacted := String(s)
	if redacted == s {
		return b
	}
	return []byte(redacted)
}

// JSONLBytes is a convenience wrapper around JSONLContent for []byte content.
func JSONLBytes(b []byte) []byte {
	s := string(b)
	redacted := JSONLContent(s)
	if redacted == s {
		return b
	}
	return []byte(redacted)
}

// JSONLContent parses each line as JSON to determine which string values
// need redaction, then performs targeted replacements on the raw JSON bytes.
// Lines with no secrets are returned unchanged, preserving original formatting.
func JSONLContent(content string) string {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			b.WriteString(line)
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			b.WriteString(line)
			continue
		}
		repls := collectJSONLReplacements(obj)
		if len(repls) == 0 {
			b.WriteString(line)
			continue
		}
		result := line
		for _, r := range repls {
			origJSON := jsonEncodeString(r[0])
			replJSON := jsonEncodeString(r[1])
			result = strings.ReplaceAll(result, origJSON, replJSON)
		}
		b.WriteString(result)
	}
	return b.String()
}

// collectJSONLReplacements walks a parsed JSON value and collects unique
// (original, redacted) string pairs for values that need redaction.
func collectJSONLReplacements(v any) [][2]string {
	seen := make(map[string]bool)
	var repls [][2]string
	var walk func(v any)
	walk = func(v any) {
		switch val := v.(type) {
		case map[string]any:
			if shouldSkipJSONLObject(val) {
				return
			}
			for k, child := range val {
				if shouldSkipJSONLField(k) {
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range val {
				walk(child)
			}
		case string:
			redacted := String(val)
			if redacted != val && !seen[val] {
				seen[val] = true
				repls = append(repls, [2]string{val, redacted})
			}
		}
	}
	walk(v)
	return repls
}

// shouldSkipJSONLField returns true if a JSON key should be excluded from scanning/redaction.
// Skips "signature" (exact) and any key ending in "id" (case-insensitive).
func shouldSkipJSONLField(key string) bool {
	if key == "signature" {
		return true
	}
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "id") || strings.HasSuffix(lower, "ids")
}

// shouldSkipJSONLObject returns true if the object has "type":"image".
func shouldSkipJSONLObject(obj map[string]any) bool {
	t, ok := obj["type"].(string)
	return ok && t == "image"
}

// isSecret returns true if match is a high-entropy string that looks like a secret.
func isSecret(match string) bool {
	return shannonEntropy(match) > entropyThreshold
}

func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[byte]int)
	for i := range len(s) {
		freq[s[i]]++
	}
	length := float64(len(s))
	var entropy float64
	for _, count := range freq {
		p := float64(count) / length
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// jsonEncodeString returns the JSON encoding of s without HTML escaping.
func jsonEncodeString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
