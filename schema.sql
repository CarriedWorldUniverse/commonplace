-- commonplace schema. Idempotent (IF NOT EXISTS throughout).

CREATE TABLE IF NOT EXISTS entry (
    id          TEXT PRIMARY KEY,         -- ULID-ish; opaque id minted on store
    org         TEXT NOT NULL,            -- herald org (tenant scope)
    owner       TEXT NOT NULL,            -- herald agent/subject id (actor)
    topic       TEXT NOT NULL,
    content     TEXT NOT NULL,
    visibility  TEXT NOT NULL DEFAULT 'private', -- 'private' | 'org'
    tags        TEXT NOT NULL DEFAULT '[]',      -- JSON array of strings
    created_at  TEXT NOT NULL,            -- RFC3339
    updated_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS entry_org_idx ON entry(org);
CREATE INDEX IF NOT EXISTS entry_org_owner_idx ON entry(org, owner);

-- Vector store. MVP: plain float32-BLOB column, brute-force cosine at
-- search time (see plan D1). embedding is little-endian float32[dim].
-- dim is fixed per-deployment by the embedding model.
CREATE TABLE IF NOT EXISTS entry_vec (
    entry_id   TEXT PRIMARY KEY REFERENCES entry(id) ON DELETE CASCADE,
    dim        INTEGER NOT NULL,
    embedding  BLOB NOT NULL
);

-- FTS5 keyword index over topic+content. Contentless-external: we keep
-- entry as the source of truth and mirror into FTS on write.
CREATE VIRTUAL TABLE IF NOT EXISTS entry_fts USING fts5(
    topic,
    content,
    entry_id UNINDEXED
);

-- (future, NOT MVP) the substrate for concept-graph / learning-paths.
-- The schema NAMES the growth path; the MVP does not build it.
-- CREATE TABLE IF NOT EXISTS entry_edge (
--     from_id  TEXT NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
--     to_id    TEXT NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
--     kind     TEXT NOT NULL,   -- relationship type (typed-node future)
--     weight   REAL NOT NULL DEFAULT 1.0,
--     PRIMARY KEY (from_id, to_id, kind)
-- );
