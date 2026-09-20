package main

import (
	"reflect"
	"testing"
)

func TestReorderFlagsFirst(t *testing.T) {
	flagsWithValue := map[string]bool{"agent": true, "limit": true}

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "flags already first pass through unchanged",
			in:   []string{"--agent", "codex", "some", "query"},
			want: []string{"--agent", "codex", "some", "query"},
		},
		{
			name: "flags after the query get hoisted",
			in:   []string{"some", "query", "--agent", "codex"},
			want: []string{"--agent", "codex", "some", "query"},
		},
		{
			name: "flag interleaved mid-query",
			in:   []string{"some", "--limit", "5", "query"},
			want: []string{"--limit", "5", "some", "query"},
		},
		{
			name: "--flag=value form doesn't consume the next token",
			in:   []string{"some", "query", "--limit=5"},
			want: []string{"--limit=5", "some", "query"},
		},
		{
			name: "a query word that happens to start with a dash is left alone",
			in:   []string{"-foo", "bar"},
			want: []string{"-foo", "bar"},
		},
		{
			name: "no flags at all",
			in:   []string{"just", "a", "query"},
			want: []string{"just", "a", "query"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reorderFlagsFirst(c.in, flagsWithValue)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("reorderFlagsFirst(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
