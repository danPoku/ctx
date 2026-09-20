package store

import (
	"strings"
	"testing"

	"github.com/kojog/ctx/queries"
)

func TestLoadQueryExtractsNamedSection(t *testing.T) {
	sql, err := LoadQuery(queries.FS, "retrieval.sql", "SearchKeyword")
	if err != nil {
		t.Fatalf("LoadQuery: %v", err)
	}
	for _, want := range []string{"chunks_fts", "MATCH :query", ":project_id", "LIMIT :limit"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SearchKeyword body missing %q:\n%s", want, sql)
		}
	}
	// Must not spill into the next named query.
	if strings.Contains(sql, "SearchHybrid") {
		t.Errorf("SearchKeyword body leaked into the next query:\n%s", sql)
	}
}

func TestLoadQueryUnknownNameErrors(t *testing.T) {
	if _, err := LoadQuery(queries.FS, "retrieval.sql", "NoSuchQuery"); err == nil {
		t.Error("LoadQuery(unknown name) = nil error, want one")
	}
}
