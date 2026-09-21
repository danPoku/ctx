// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"strings"

	"github.com/danPoku/kaectx/internal/ingest"
)

// PatchFileTouches extracts the files an apply_patch call names. The tool's
// input is Codex's own patch envelope, not a diff:
//
//	*** Begin Patch
//	*** Update File: path        (optionally followed by "*** Move to: new")
//	*** Add File: path
//	*** Delete File: path
//	*** End Patch
//
// The file headers are structured, so unlike a shell command they can be read
// reliably. Anything that isn't an apply_patch call returns nil — `exec` and
// friends don't name files in a structured way.
func PatchFileTouches(toolName, input string) []ingest.FileTouch {
	if toolName != "apply_patch" {
		return nil
	}
	var out []ingest.FileTouch
	for _, l := range strings.Split(input, "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "*** Update File: "):
			out = append(out, ingest.FileTouch{Path: strings.TrimSpace(strings.TrimPrefix(l, "*** Update File: ")), Action: "edit"})
		case strings.HasPrefix(l, "*** Add File: "):
			out = append(out, ingest.FileTouch{Path: strings.TrimSpace(strings.TrimPrefix(l, "*** Add File: ")), Action: "create"})
		case strings.HasPrefix(l, "*** Delete File: "):
			out = append(out, ingest.FileTouch{Path: strings.TrimSpace(strings.TrimPrefix(l, "*** Delete File: ")), Action: "delete"})
		case strings.HasPrefix(l, "*** Move to: "):
			out = append(out, ingest.FileTouch{Path: strings.TrimSpace(strings.TrimPrefix(l, "*** Move to: ")), Action: "rename"})
		}
	}
	return out
}
