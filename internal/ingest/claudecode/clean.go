// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"regexp"
	"strings"
)

// pastedContentTag matches the wrapper the VS Code extension puts around text
// the user pasted into the prompt box. Real sample from a session file:
//
//	"\n\n<pasted_content id=\"7f4b\">\nRemember this...\n</pasted_content id=\"7f4b\">\n"
//
// Note the unusual closing tag, which repeats the id. Both forms are matched.
var pastedContentTag = regexp.MustCompile(`</?pasted_content(?:\s+id="[^"]*")?\s*>`)

// cleanUserText removes UI wrapper tags from a user prompt so the stored
// content, and therefore chunk text, search snippets and embeddings, is what
// the person actually wrote. The tags are pure interface noise: they shift
// every embedding toward "pasted_content" and clutter every snippet. The
// original line stays verbatim in messages.raw, so nothing is lost.
//
// Only wrapper tags are removed; the pasted text between them is kept.
func cleanUserText(s string) string {
	if !strings.Contains(s, "pasted_content") {
		return s
	}
	return strings.TrimSpace(pastedContentTag.ReplaceAllString(s, ""))
}
