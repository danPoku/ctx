// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/danPoku/kaectx/internal/ingest"
)

func TestPatchFileTouches(t *testing.T) {
	cases := []struct {
		name, tool, input string
		want              []ingest.FileTouch
	}{
		{
			name: "every header kind, in order",
			tool: "apply_patch",
			input: "*** Begin Patch\r\n*** Update File: a.go\r\n@@\n-x\n+y\n*** Add File: b.go\n+z\n" +
				"*** Delete File: c.go\n*** Update File: d.go\n*** Move to: e.go\n*** End Patch",
			want: []ingest.FileTouch{
				{Path: "a.go", Action: "edit"}, {Path: "b.go", Action: "create"}, {Path: "c.go", Action: "delete"},
				{Path: "d.go", Action: "edit"}, {Path: "e.go", Action: "rename"},
			},
		},
		{name: "a header-looking line inside a hunk body is not a header", tool: "apply_patch",
			input: "*** Update File: a.go\n@@\n+ *** Add File: fake.go\n",
			want:  []ingest.FileTouch{{Path: "a.go", Action: "edit"}}},
		{name: "exec never yields touches", tool: "exec", input: "*** Update File: a.go"},
		{name: "empty patch", tool: "apply_patch", input: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PatchFileTouches(c.tool, c.input); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// Only session_meta / turn_context carry cwd; every later line must inherit
// it, or file paths stay absolute and commits can't be resolved.
func TestAdapterCarriesCWDToLaterLines(t *testing.T) {
	a := New()
	if _, err := a.ParseLine([]byte(`{"type":"session_meta","timestamp":"2026-01-01T00:00:00Z","payload":{"session_id":"s","cwd":"/repo"}}`)); err != nil {
		t.Fatal(err)
	}
	call, _ := json.Marshal(map[string]any{"type": "response_item", "timestamp": "2026-01-01T00:00:01Z",
		"payload": map[string]any{"type": "custom_tool_call", "call_id": "1", "name": "apply_patch", "input": "*** Update File: /repo/a.go"}})
	res, err := a.ParseLine(call)
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.CWD != "/repo" {
		t.Errorf("CWD = %q, want /repo", res.Session.CWD)
	}
	if len(res.Message.FileTouches) != 1 {
		t.Errorf("FileTouches = %+v", res.Message.FileTouches)
	}
	// turn_context can move the working directory.
	res, _ = a.ParseLine([]byte(`{"type":"turn_context","timestamp":"2026-01-01T00:00:02Z","payload":{"cwd":"/other","model":"m"}}`))
	if res.Session.CWD != "/other" {
		t.Errorf("turn_context CWD = %q", res.Session.CWD)
	}
}
