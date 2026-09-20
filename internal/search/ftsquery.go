package search

import (
	"regexp"
	"strings"
)

// sanitizeFTS5Query makes an arbitrary user/agent-typed string safe to bind
// as an FTS5 MATCH query.
//
// The bug this exists to fix, discovered dogfooding real search queries:
// FTS5's query-string parser treats an unquoted hyphen or colon INSIDE a
// bareword as column-filter syntax — "sqlite-vec" fails with "no such
// column: vec", "foo:bar" fails with "no such column: foo". This is not an
// exotic edge case: ordinary technical terms ("sqlite-vec", "go-sqlite3",
// "non-negotiable") and Windows paths ("C:\Users\...") all trigger it, so
// an unsanitized query crashes on completely normal input.
//
// The fix quotes only the tokens that need it, leaving legitimate FTS5
// syntax alone:
//   - text already inside "double quotes" passes through verbatim (the
//     user deliberately wrote a phrase query)
//   - AND / OR / NOT (exact uppercase) pass through as operators
//   - "(" and ")" pass through as grouping
//   - leading-minus exclusion ("-term") passes through — the ambiguity is
//     specifically an EMBEDDED hyphen mid-word, not a leading one
//   - a bareword of only letters/digits/underscore, optionally with a
//     trailing "*" (prefix match), passes through unchanged
//   - anything else gets wrapped in "double quotes" (embedded quotes
//     doubled, FTS5/SQL's standard escape) so FTS5's parser sees a literal
//     string instead of attempting to parse it as syntax
var (
	quotedSpanRE = regexp.MustCompile(`"[^"]*"`)
	// A safe word is an optional run of "(" (grouping), an optional
	// leading "-" (NOT-prefix), the actual bareword (letters/digits/
	// underscore, possibly empty so a lone "(" or ")" matches), an
	// optional trailing "*" (prefix match), and an optional run of ")".
	// Glued-together grouping like "(a" or "b)" is how people actually
	// type FTS5 parenthetical queries, so this has to accept it rather
	// than only recognizing "(" and ")" as their own whitespace-delimited
	// tokens.
	safeWordRE = regexp.MustCompile(`^\(*-?[A-Za-z0-9_]*\*?\)*$`)
)

func sanitizeFTS5Query(query string) string {
	// Placeholder-protect already-quoted spans so word-splitting below
	// doesn't tear a deliberate multi-word phrase query apart.
	var quoted []string
	protected := quotedSpanRE.ReplaceAllStringFunc(query, func(span string) string {
		quoted = append(quoted, span)
		return "\x00" + string(rune(len(quoted)-1)) + "\x00"
	})

	words := strings.Fields(protected)
	for i, w := range words {
		if strings.HasPrefix(w, "\x00") {
			continue // restored below
		}
		switch {
		case w == "AND" || w == "OR" || w == "NOT":
		case safeWordRE.MatchString(w):
		default:
			words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
		}
	}
	result := strings.Join(words, " ")

	for i, span := range quoted {
		result = strings.ReplaceAll(result, "\x00"+string(rune(i))+"\x00", span)
	}
	return result
}
