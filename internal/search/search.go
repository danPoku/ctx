// Package search runs the retrieval queries defined in queries/retrieval.sql
// against the store. Milestone 2 only wires up keyword (FTS) search;
// SearchHybrid needs the embedding worker (milestone 5).
package search

import (
	"database/sql"

	"github.com/kojog/ctx/internal/store"
	"github.com/kojog/ctx/queries"
)

// KeywordResult is one row of SearchKeyword's result set.
type KeywordResult struct {
	ChunkID   int64
	SessionID string
	Agent     string
	StartedAt string
	Snippet   string
	BM25      float64
}

// Keyword runs SearchKeyword: BM25-ranked full-text search over chunks,
// scoped to a project and optionally to one agent.
func Keyword(db *sql.DB, query string, projectID int64, agent string, limit int) ([]KeywordResult, error) {
	sqlText, err := store.LoadQuery(queries.FS, "retrieval.sql", "SearchKeyword")
	if err != nil {
		return nil, err
	}

	var agentArg any
	if agent != "" {
		agentArg = agent
	}

	rows, err := db.Query(sqlText,
		sql.Named("query", query),
		sql.Named("project_id", projectID),
		sql.Named("agent", agentArg),
		sql.Named("limit", limit),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []KeywordResult
	for rows.Next() {
		var r KeywordResult
		if err := rows.Scan(&r.ChunkID, &r.SessionID, &r.Agent, &r.StartedAt, &r.Snippet, &r.BM25); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
