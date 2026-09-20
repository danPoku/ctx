// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package claudecode

import "testing"

func TestCleanUserText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "real VS Code paste wrapper with id on the closing tag",
			in:   "\n\n<pasted_content id=\"7f4b\">\nRemember this marker word: ZEPHYRQUOKKA-VSC-1. Reply with one sentence acknowledging it.\n</pasted_content id=\"7f4b\">\n",
			want: "Remember this marker word: ZEPHYRQUOKKA-VSC-1. Reply with one sentence acknowledging it.",
		},
		{
			name: "typed text around a paste keeps both",
			in:   "Fix this:\n<pasted_content id=\"a1\">\nboom\n</pasted_content id=\"a1\">\nplease",
			want: "Fix this:\n\nboom\n\nplease",
		},
		{name: "ordinary prompt untouched", in: "  hello there  ", want: "  hello there  "},
		{name: "prompt that merely mentions the word", in: "what is pasted_content?", want: "what is pasted_content?"},
		{name: "other tags untouched", in: "<local-command-stdout>ok</local-command-stdout>", want: "<local-command-stdout>ok</local-command-stdout>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanUserText(c.in); got != c.want {
				t.Errorf("cleanUserText(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// End to end through ParseLine: the stored content is clean, and the raw line
// (with the wrapper) is preserved for anyone who needs the original.
func TestParseLineStripsPasteWrapperFromUserPromptOnly(t *testing.T) {
	a := New()
	user := `{"type":"user","message":{"role":"user","content":"\n\n<pasted_content id=\"7f4b\">\nhello\n</pasted_content id=\"7f4b\">\n"},"sessionId":"s1","timestamp":"2026-09-20T10:00:00Z"}`
	res, err := a.ParseLine([]byte(user))
	if err != nil || res.Message == nil {
		t.Fatalf("ParseLine: %v, message %v", err, res.Message)
	}
	if res.Message.Content != "hello" {
		t.Errorf("content = %q, want %q", res.Message.Content, "hello")
	}
	if string(res.Message.Raw) != user {
		t.Errorf("raw line was altered")
	}

	// An assistant reply that quotes the tag must not be rewritten.
	asst := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the <pasted_content id=\"x\"> tag wraps pastes"}]},"sessionId":"s1","timestamp":"2026-09-20T10:00:01Z"}`
	res, err = a.ParseLine([]byte(asst))
	if err != nil || res.Message == nil {
		t.Fatalf("ParseLine assistant: %v", err)
	}
	if res.Message.Content != `the <pasted_content id="x"> tag wraps pastes` {
		t.Errorf("assistant content was rewritten: %q", res.Message.Content)
	}
}
