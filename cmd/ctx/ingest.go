package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kojog/ctx/internal/ingest"
	"github.com/kojog/ctx/internal/ingest/claudecode"
	"github.com/kojog/ctx/internal/store"
)

func runIngest(args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	path := fs.String("path", "", "ingest a single JSONL file instead of scanning ~/.claude/projects")
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

	adapter := claudecode.New()

	if *path != "" {
		if err := ingest.Run(db, adapter, *path); err != nil {
			return fmt.Errorf("%s: %w", *path, err)
		}
		fmt.Println("ingested", *path)
		return nil
	}

	root := filepath.Join(home, ".claude", "projects")
	results, err := ingest.RunAll(db, adapter, root)
	if err != nil {
		return err
	}
	ok, failed := 0, 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "ctx ingest: %s: %v\n", r.Path, r.Err)
			continue
		}
		ok++
	}
	fmt.Printf("ingested %d file(s), %d failed\n", ok, failed)
	return nil
}
