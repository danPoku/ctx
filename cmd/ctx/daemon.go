package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kojog/ctx/internal/daemon"
)

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fallback := fs.Duration("fallback-interval", 5*time.Minute, "how often to do a full re-scan as a safety net for missed filesystem events")
	debounce := fs.Duration("debounce", 500*time.Millisecond, "how long a session file must go quiet before it's re-ingested")
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	db, _, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	sources := make([]daemon.Source, len(agentSources))
	for i, src := range agentSources {
		sources[i] = daemon.Source{Agent: src.name, Root: src.root(home), NewAdapter: src.newAdapter}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("ctx daemon: watching %d source(s), press Ctrl+C to stop\n", len(sources))
	for _, src := range sources {
		fmt.Printf("  %s: %s\n", src.Agent, src.Root)
	}

	return daemon.Run(ctx, db, sources, *fallback, *debounce)
}
