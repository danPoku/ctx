-- =============================================================================
-- Named queries for writing notes — see queries/retrieval.sql for the
-- reading side (SearchNotes) and the format/scoping conventions.
-- =============================================================================

-- name: SaveNote :exec
INSERT INTO notes(project_id, session_id, agent, kind, title, body, tags)
VALUES (:project_id, :session_id, :agent, :kind, :title, :body, :tags);
