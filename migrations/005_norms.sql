-- Normative corpus: full texts of laws and building codes, chunked by legal
-- unit (статья/часть/пункт) with embeddings for semantic search.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS norm_documents (
    code        TEXT PRIMARY KEY,
    title       TEXT NOT NULL DEFAULT '',
    edition     TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    file_hash   TEXT NOT NULL DEFAULT '',
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS norm_chunks (
    id        BIGSERIAL PRIMARY KEY,
    doc_code  TEXT NOT NULL REFERENCES norm_documents(code) ON DELETE CASCADE,
    ref       TEXT NOT NULL DEFAULT '',
    ref_norm  TEXT NOT NULL DEFAULT '',
    ord       INT  NOT NULL DEFAULT 0,
    text      TEXT NOT NULL,
    embedding vector(1536)
);

CREATE INDEX IF NOT EXISTS idx_norm_chunks_doc_ref ON norm_chunks(doc_code, ref_norm);
CREATE INDEX IF NOT EXISTS idx_norm_chunks_doc_ord ON norm_chunks(doc_code, ord);
CREATE INDEX IF NOT EXISTS idx_norm_chunks_embedding ON norm_chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 20);
