package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/kojog/ctx/internal/embed"
	"github.com/kojog/ctx/internal/store"
	"github.com/kojog/ctx/queries"
)

// HybridResult is one row of SearchHybrid/SearchHybridByAgent's result set.
type HybridResult struct {
	ChunkID   int64
	Score     float64
	SessionID string
	Agent     string
	Title     string
	StartedAt string
	Preview   string
}

// Hybrid runs Reciprocal Rank Fusion of keyword (FTS) and semantic (vector)
// search over chunks. It embeds query itself via client, so callers don't
// need to know embedding details — same contract as Keyword, just backed by
// two ranking signals instead of one.
//
// SearchHybrid and SearchHybridByAgent are two separate named queries (see
// retrieval.sql's comment on SearchHybridByAgent for why vec0 needs its own
// copy rather than a shared ":agent IS NULL OR ..." clause); which one runs
// is an implementation detail callers shouldn't need to think about.
func Hybrid(ctx context.Context, db *sql.DB, client embed.Embedder, query string, projectID int64, agent string, limit int) ([]HybridResult, error) {
	vec, err := client.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	vecJSON, err := json.Marshal(vec)
	if err != nil {
		return nil, err
	}

	queryName := "SearchHybrid"
	args := []any{
		sql.Named("query", query),
		sql.Named("query_embedding", string(vecJSON)),
		sql.Named("project_id", projectID),
		sql.Named("limit", limit),
	}
	if agent != "" {
		queryName = "SearchHybridByAgent"
		args = append(args, sql.Named("agent", agent))
	}

	sqlText, err := store.LoadQuery(queries.FS, "retrieval.sql", queryName)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []HybridResult
	for rows.Next() {
		var r HybridResult
		var title sql.NullString
		if err := rows.Scan(&r.ChunkID, &r.Score, &r.SessionID, &r.Agent, &title, &r.StartedAt, &r.Preview); err != nil {
			return nil, err
		}
		r.Title = title.String
		results = append(results, r)
	}
	return results, rows.Err()
}
