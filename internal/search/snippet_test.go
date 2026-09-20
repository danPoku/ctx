// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package search

import (
	"strings"
	"testing"
)

func TestCenterSnippet(t *testing.T) {
	question := "User: " + strings.Repeat("how do we handle the thing ", 30)
	answer := "Assistant: " + strings.Repeat("filler words go here ", 30) + "the fix was to replay the first line into a fresh adapter " + strings.Repeat("and more filler ", 30)
	chunk := question + "\n\n" + answer

	tests := []struct {
		name, text, query string
		wantContains      []string
		wantPrefix        string
		wantExact         string // when set, the whole result
	}{
		{
			name: "centres on a hit deep in the answer, not the first 400 chars",
			text: chunk, query: "replay fresh adapter",
			wantContains: []string{"replay the first line into a fresh adapter"},
		},
		{
			name: "matches by word prefix, like FTS stemming",
			text: chunk, query: "replaying",
			wantContains: []string{"replay the first line"},
		},
		{
			name: "operators and quotes are ignored",
			text: chunk, query: `"fresh adapter" AND replay NOT unrelated`,
			wantContains: []string{"fresh adapter"},
		},
		{
			name: "no match falls back to the start of the chunk",
			text: chunk, query: "zzzqqq",
			wantPrefix: "User: how do we handle",
		},
		{
			name: "short text is returned whole",
			text: "User: hi\n\nAssistant: hello", query: "hello",
			wantExact: "User: hi\n\nAssistant: hello",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := centerSnippet(tc.text, tc.query, 400)
			if tc.wantExact != "" && got != tc.wantExact {
				t.Errorf("got %q, want %q", got, tc.wantExact)
			}
			for _, w := range tc.wantContains {
				if !strings.Contains(got, w) {
					t.Errorf("snippet %q does not contain %q", got, w)
				}
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("snippet %q does not start with %q", got, tc.wantPrefix)
			}
			if tc.wantExact == "" {
				if n := len([]rune(got)); n > 400+4 { // width plus the two "…" markers
					t.Errorf("snippet is %d chars, want about 400", n)
				}
			}
		})
	}

	t.Run("prefers a window holding more distinct query words", func(t *testing.T) {
		text := strings.Repeat("padding ", 100) + "alpha " + strings.Repeat("padding ", 100) +
			"alpha beta gamma " + strings.Repeat("padding ", 100)
		got := centerSnippet(text, "alpha beta gamma", 400)
		if !strings.Contains(got, "beta") || !strings.Contains(got, "gamma") {
			t.Errorf("snippet %q missed the window with all three words", got)
		}
	})

	t.Run("never splits a multi-byte character", func(t *testing.T) {
		text := strings.Repeat("é", 600) + " needle " + strings.Repeat("ü", 600)
		got := centerSnippet(text, "needle", 400)
		if !strings.Contains(got, "needle") {
			t.Errorf("snippet lost the hit")
		}
		if strings.ContainsRune(got, '�') {
			t.Error("snippet contains a replacement character; a rune was split")
		}
	})
}
