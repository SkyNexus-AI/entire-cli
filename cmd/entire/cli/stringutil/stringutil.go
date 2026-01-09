// Package stringutil provides UTF-8 safe string manipulation utilities.
package stringutil

import (
	"unicode"
	"unicode/utf8"
)

// TruncateRunes truncates a string to at most maxRunes runes, appending suffix if truncated.
// This is safe for multi-byte UTF-8 characters unlike byte-based slicing.
func TruncateRunes(s string, maxRunes int, suffix string) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	// Leave room for the suffix
	suffixRunes := []rune(suffix)
	truncateAt := maxRunes - len(suffixRunes)
	if truncateAt < 0 {
		truncateAt = 0
	}
	return string(runes[:truncateAt]) + suffix
}

// CapitalizeFirst capitalizes the first rune of a string.
// This is safe for multi-byte UTF-8 characters unlike byte indexing.
func CapitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
