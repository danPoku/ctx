// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package search

import (
	"strings"
	"unicode"
)

// previewChars is how much of a chunk a hybrid hit shows.
const previewChars = 400

// centerSnippet returns about width characters of text centred on where the
// query's words cluster, instead of the first width characters.
//
// Why: a chunk is "User: <question>\n\nAssistant: <answer>", and the first
// 400 characters are mostly the question. The answer, which is what the agent
// is looking for, sits further in. Showing the start is like an index card
// that always quotes the first line of the letter, however far down the
// relevant sentence is.
//
// The window is placed where the most DISTINCT query words fall inside it
// (ties go to the earliest). Matching is case-insensitive and by word prefix
// (first stemLen letters), a crude stand-in for the porter stemming FTS5
// applies, so "refactoring" still finds "refactor". A semantic-only hit may
// contain none of the words, in which case the start of the chunk is the best
// available preview.
func centerSnippet(text, query string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}

	terms := queryTerms(query)
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r) // rune-for-rune, so indexes line up with runes
	}

	// Every position where a term begins a word.
	type hit struct{ pos, term int }
	var hits []hit
	for ti, term := range terms {
		tr := []rune(term)
		for i := 0; i+len(tr) <= len(lower); i++ {
			if (i == 0 || !isWordRune(lower[i-1])) && runesHavePrefix(lower[i:], tr) {
				hits = append(hits, hit{i, ti})
			}
		}
	}
	if len(hits) == 0 {
		return string(runes[:width]) + " …"
	}

	// Try a window starting just before each hit; keep the one covering the
	// most distinct terms.
	bestStart, bestCount := 0, -1
	for _, h := range hits {
		start := h.pos - width/4
		if start < 0 {
			start = 0
		}
		if start+width > len(runes) {
			start = len(runes) - width
		}
		seen := map[int]bool{}
		for _, o := range hits {
			if o.pos >= start && o.pos < start+width {
				seen[o.term] = true
			}
		}
		if len(seen) > bestCount {
			bestStart, bestCount = start, len(seen)
		}
	}

	out := string(runes[bestStart : bestStart+width])
	if bestStart > 0 {
		out = "… " + out
	}
	if bestStart+width < len(runes) {
		out += " …"
	}
	return out
}

// stemLen is how many leading letters of a query word must match.
const stemLen = 5

// queryTerms extracts the plain words of an FTS5-ish query: lowercase,
// operators (AND/OR/NOT) and negated words dropped, punctuation stripped,
// long words cut to stemLen letters.
func queryTerms(query string) []string {
	var terms []string
	seen := map[string]bool{}
	for _, w := range strings.Fields(query) {
		if w == "AND" || w == "OR" || w == "NOT" || strings.HasPrefix(w, "-") {
			continue
		}
		for _, part := range strings.FieldsFunc(w, func(r rune) bool { return !isWordRune(r) }) {
			part = strings.ToLower(part)
			if len([]rune(part)) < 2 {
				continue
			}
			if r := []rune(part); len(r) > stemLen {
				part = string(r[:stemLen])
			}
			if !seen[part] {
				seen[part] = true
				terms = append(terms, part)
			}
		}
	}
	return terms
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func runesHavePrefix(s, prefix []rune) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}
