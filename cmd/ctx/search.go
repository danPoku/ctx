package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kojog/ctx/internal/search"
	"github.com/kojog/ctx/internal/store"
)

// searchFlagsWithValue lists this command's flags that take a value, so
// reorderFlagsFirst knows to keep each one paired with its following token
// when it hoists them ahead of the query words.
var searchFlagsWithValue = map[string]bool{"agent": true, "limit": true}

func runSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	agent := fs.String("agent", "", "restrict to one agent (default: all agents)")
	limit := fs.Int("limit", 10, "max results")
	if err := fs.Parse(reorderFlagsFirst(args, searchFlagsWithValue)); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: ctx search <query> [--agent NAME] [--limit N]")
	}
	query := strings.Join(fs.Args(), " ")

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

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	projectID, err := store.ResolveProject(db, cwd)
	if err != nil {
		return fmt.Errorf("resolve project for %s: %w", cwd, err)
	}

	results, err := search.Keyword(db, query, projectID, *agent, *limit)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("no results")
		return nil
	}
	for _, r := range results {
		fmt.Printf("[%s] %s  %s\n    %s\n\n", r.Agent, r.StartedAt, r.SessionID, r.Snippet)
	}
	return nil
}
