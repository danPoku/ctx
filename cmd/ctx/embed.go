package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/kojog/ctx/internal/embed"
)

func runEmbed(args []string) error {
	fs := flag.NewFlagSet("embed", flag.ContinueOnError)
	batchSize := fs.Int("batch", 50, "chunks to embed per Ollama round trip")
	baseURL := fs.String("ollama-url", embed.DefaultBaseURL, "Ollama server URL")
	model := fs.String("model", embed.DefaultModel, "embedding model name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, _, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	client := embed.NewOllamaClient(*baseURL, *model)

	total, err := embed.Drain(context.Background(), db, client, *batchSize)
	if err != nil {
		if total > 0 {
			fmt.Printf("embedded %d chunk(s) before hitting an error\n", total)
		}
		return err
	}
	fmt.Printf("embedded %d chunk(s)\n", total)
	return nil
}
