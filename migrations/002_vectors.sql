-- =============================================================================
-- Migration 002: semantic vectors (requires the sqlite-vec extension)
-- =============================================================================
--
-- Kept separate from 001 on purpose: if the extension fails to load, keyword
-- search still works. The catalogue stays open even when the
-- "find similar cases" desk is closed.
--
-- DIMENSION MUST MATCH YOUR EMBEDDING MODEL. 768 = nomic-embed-text (Ollama).
-- bge-small = 384, text-embedding-3-small = 1536. Changing models means a new
-- table (chunk_vectors_v2) and a re-embed — you can't mix rulers of different
-- lengths in one measurement.
--
-- Column roles inside vec0:
--   project_id  PARTITION KEY -> vectors are physically grouped per project, so a
--                                project-scoped search only scans that project's
--                                shelf. The "within this client" query gets cheap.
--   agent       metadata      -> filterable DURING the KNN search, which is what
--                                makes "within-agent" retrieval correct. Filtering
--                                after a top-10 search could leave you with 1 hit.
--   session_id  auxiliary (+) -> carried along for display, not filterable.
-- =============================================================================

CREATE VIRTUAL TABLE IF NOT EXISTS chunk_vectors USING vec0(
    chunk_id    INTEGER PRIMARY KEY,            -- = chunks.id
    project_id  INTEGER PARTITION KEY,
    agent       TEXT,
    embedding   FLOAT[768] distance_metric=cosine,
    +session_id TEXT
);

-- vec0 tables can't hold foreign keys, so we clean up by hand: when a chunk
-- goes, its vector goes with it.
CREATE TRIGGER IF NOT EXISTS chunks_vec_ad AFTER DELETE ON chunks BEGIN
    DELETE FROM chunk_vectors WHERE chunk_id = old.id;
END;

INSERT OR IGNORE INTO schema_migrations(version, name) VALUES (2, 'vectors');
