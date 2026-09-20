package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kojog/ctx/internal/ingest"
	"github.com/kojog/ctx/internal/ingest/claudecode"
	"github.com/kojog/ctx/internal/ingest/codex"
	"github.com/kojog/ctx/internal/store"
)

// agentSource pairs an agent with where its session logs live and how to
// get a fresh adapter for them (RunAll needs a new instance per file — see
// its doc for why: Codex's adapter is stateful within a file).
type agentSource struct {
	name       string
	root       func(home string) string
	newAdapter func() ingest.Adapter
}

var agentSources = []agentSource{
	{
		name:       "claude-code",
		root:       func(home string) string { return filepath.Join(home, ".claude", "projects") },
		newAdapter: func() ingest.Adapter { return claudecode.New() },
	},
	{
		name:       "codex",
		root:       func(home string) string { return filepath.Join(home, ".codex", "sessions") },
		newAdapter: func() ingest.Adapter { return codex.New() },
	},
}

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	path := fs.String("path", "", "ingest a single JSONL file instead of scanning the default session directories")
	agent := fs.String("agent", "claude-code", "which adapter to use with --path (claude-code or codex)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	db, err := store.Open(filepath.Join(home, ".ctx", "ctx.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := store.Migrate(db); err != nil {
		return err
	}

	if *path != "" {
		src, err := findAgentSource(*agent)
		if err != nil {
			return err
		}
		if err := ingest.Run(db, src.newAdapter(), *path); err != nil {
			return fmt.Errorf("%s: %w", *path, err)
		}
		fmt.Println("ingested", *path)
		return nil
	}

	ok, failed := 0, 0
	for _, src := range agentSources {
		results, err := ingest.RunAll(db, src.newAdapter, src.root(home))
		if err != nil {
			return fmt.Errorf("%s: %w", src.name, err)
		}
		for _, r := range results {
			if r.Err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "ctx ingest: %s: %v\n", r.Path, r.Err)
				continue
			}
			ok++
		}
	}
	fmt.Printf("ingested %d file(s), %d failed\n", ok, failed)
	return nil
}

func findAgentSource(name string) (agentSource, error) {
	for _, src := range agentSources {
		if src.name == name {
			return src, nil
		}
	}
	return agentSource{}, fmt.Errorf("unknown agent %q (want claude-code or codex)", name)
}
