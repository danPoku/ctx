// Copyright 2026 Dan Gyinaye Poku
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"testing"
	"time"
)

func TestDebouncerCoalescesRapidCalls(t *testing.T) {
	d := newDebouncer(30 * time.Millisecond)
	defer d.stop()

	for i := 0; i < 5; i++ {
		d.schedule("a")
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case key := <-d.ready:
		if key != "a" {
			t.Errorf("ready key = %q, want a", key)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for debounced key")
	}

	// Only one send should have happened — five schedule() calls within the
	// delay window must coalesce into exactly one.
	select {
	case key := <-d.ready:
		t.Errorf("got a second ready send (%q), want exactly one for five rapid calls", key)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing more arrives
	}
}

func TestDebouncerHandlesDistinctKeysIndependently(t *testing.T) {
	d := newDebouncer(20 * time.Millisecond)
	defer d.stop()

	d.schedule("a")
	d.schedule("b")

	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case key := <-d.ready:
			got[key] = true
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("timed out waiting for key %d", i)
		}
	}
	if !got["a"] || !got["b"] {
		t.Errorf("got = %v, want both a and b", got)
	}
}
