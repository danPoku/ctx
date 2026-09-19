// Command ctx is the single binary for the local multi-agent context store:
// ingesting agent sessions, indexing them, and serving them back over MCP
// and the CLI. This milestone only wires up `ctx init`.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kojog/ctx/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "init":
		if err := runInit(); err != nil {
			fmt.Fprintln(os.Stderr, "ctx init:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "ctx: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: ctx <command> [flags]

commands:
  init    create ~/.ctx and its database`)
}

func runInit() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".ctx")

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := secureDir(dir); err != nil {
		fmt.Fprintf(os.Stderr, "ctx init: warning: %v\n", err)
	}

	dbPath := filepath.Join(dir, "ctx.db")
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	result, err := store.Migrate(db)
	if err != nil {
		return err
	}

	fmt.Printf("initialized %s\n", dbPath)
	if !result.VectorsLoaded {
		fmt.Fprintf(os.Stderr, "ctx init: warning: semantic search unavailable, sqlite-vec did not load: %v\n", result.VectorsErr)
	}
	return nil
}

// secureDir makes sure dir is readable only by its owner. ~/.ctx holds
// redacted transcripts pulled from every coding agent on the machine —
// silently accepting a pre-existing world- or group-readable directory is
// the wrong default. os.MkdirAll(dir, 0700) only sets that mode when it
// creates the directory; if ~/.ctx already existed with looser permissions
// (created by hand, or by a future version with a different default),
// MkdirAll leaves it alone, so this checks and tightens it explicitly.
func secureDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	if info.Mode().Perm() != 0700 {
		if err := os.Chmod(dir, 0700); err != nil {
			return fmt.Errorf("%s is mode %#o (not 0700) and could not be tightened: %w", dir, info.Mode().Perm(), err)
		}
	}
	return nil
}
