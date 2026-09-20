package codex

import (
	"regexp"
	"strings"
)

var (
	// numericCharRef matches numeric HTML character references such as
	// "&#x20;". The Codex extension writes one for a leading space in the
	// prompt box. Only numeric references are decoded, not named ones like
	// "&lt;", so code a user typed literally is left alone.
	numericCharRef = regexp.MustCompile(`&#(?:[xX][0-9a-fA-F]+|[0-9]+);`)

	// markdownEscape matches a backslash before "_" or "*", the markdown
	// escapes the extension adds ("search\_context"). The set is deliberately
	// tiny: real user text also contains backslashes that are NOT escapes,
	// such as the Windows paths C:\Users\... and C:\...\Notes, whose \U, \N and
	// \d must be left exactly as written.
	markdownEscape = regexp.MustCompile(`\\([_*])`)
)

// cleanUserText undoes the markdown/HTML escaping the Codex extension applies
// to what the user typed, restoring the original text for search. The raw
// line, with the escaping intact, stays in messages.raw.
func cleanUserText(s string) string {
	if !strings.ContainsAny(s, `&\`) {
		return s
	}
	s = numericCharRef.ReplaceAllStringFunc(s, decodeCharRef)
	s = markdownEscape.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

// decodeCharRef decodes one numeric character reference, returning it
// unchanged if it is not a valid code point.
func decodeCharRef(ref string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(ref, "&#"), ";")
	base := 10
	if body != "" && (body[0] == 'x' || body[0] == 'X') {
		body, base = body[1:], 16
	}
	var n int64
	for _, c := range body {
		var d int64
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int64(c-'A') + 10
		default:
			return ref
		}
		n = n*int64(base) + d
		if n > 0x10FFFF {
			return ref
		}
	}
	if n == 0 || (n >= 0xD800 && n <= 0xDFFF) {
		return ref
	}
	return string(rune(n))
}
