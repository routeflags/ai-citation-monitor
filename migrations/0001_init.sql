-- ai-citation-monitor schema, migration 0001.
--
-- The tool owns the schema (D5): consumers point --db at their own file.
-- Append-only: nothing in the tool issues UPDATE against runs / responses /
-- citations / mentions / usage_records. Re-running a measurement window skips
-- the calls whose plan_key already exists.
--
-- Invariants encoded here:
--   * every rate is computed from runs rows, so the denominator lives in one place
--     (v_runs_valid)
--   * conditions travel with the measurement (provider / model /
--     api_endpoint_version / search_enabled / measurement_method /
--     question_set_version / question_set_sha256 / started_at / completed_at)
--   * a run without a response row is detectable (see `aicm verify`)
--   * failure and unknown are states, not zero: status = success|error|cancelled,
--     citation_observability = observed|unknown

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

-- The project is the site under measurement.
CREATE TABLE IF NOT EXISTS projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id     TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL DEFAULT '',
    own_domain  TEXT NOT NULL,
    subdomains  INTEGER NOT NULL DEFAULT 1 CHECK (subdomains IN (0, 1)),
    market      TEXT NOT NULL DEFAULT '',
    language    TEXT NOT NULL DEFAULT 'ja',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- A question is stable by question_key; its text lives in question_versions.
CREATE TABLE IF NOT EXISTS questions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id   INTEGER NOT NULL REFERENCES projects(id),
    question_key TEXT NOT NULL,
    text         TEXT NOT NULL,
    category     TEXT NOT NULL DEFAULT '',
    language     TEXT NOT NULL DEFAULT '',
    active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at   TEXT NOT NULL,
    UNIQUE (project_id, question_key)
);

-- One row per distinct question text. The version string is derived from the
-- text by default, so editing a question cannot silently reuse the old version
-- (measurements of different texts are not comparable).
CREATE TABLE IF NOT EXISTS question_versions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    question_id    INTEGER NOT NULL REFERENCES questions(id),
    version        TEXT NOT NULL,
    text           TEXT NOT NULL,
    category       TEXT NOT NULL DEFAULT '',
    content_sha256 TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    UNIQUE (question_id, version)
);

-- Competitor domains can be registered (measured against) or observed (seen
-- cited but not registered).
CREATE TABLE IF NOT EXISTS competitors (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    domain     TEXT NOT NULL,
    label      TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT 'registered' CHECK (source IN ('registered', 'observed')),
    created_at TEXT NOT NULL,
    UNIQUE (project_id, domain)
);

-- Provenance of an imported question set (YAML / CSV). Optional but recorded so
-- reports can show which question set version produced a run.
CREATE TABLE IF NOT EXISTS question_sets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id),
    set_version TEXT NOT NULL,
    sha256      TEXT NOT NULL UNIQUE,
    path        TEXT NOT NULL,
    loaded_at   TEXT NOT NULL
);

-- One row per attempted call. plan_key = hash of every condition that identifies
-- the call, so resume can skip completed calls without guessing.
CREATE TABLE IF NOT EXISTS runs (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id             INTEGER NOT NULL REFERENCES projects(id),
    question_id            INTEGER NOT NULL REFERENCES questions(id),
    question_version       TEXT NOT NULL,
    repeat_index           INTEGER NOT NULL CHECK (repeat_index >= 1),
    provider               TEXT NOT NULL,
    model                  TEXT NOT NULL,
    api_endpoint_version   TEXT NOT NULL,
    search_enabled         INTEGER NOT NULL CHECK (search_enabled IN (0, 1)),
    measurement_method     TEXT NOT NULL CHECK (measurement_method IN ('official_api')),
    stage                  TEXT NOT NULL DEFAULT '',
    question_set_version   TEXT NOT NULL DEFAULT '',
    question_set_sha256    TEXT NOT NULL DEFAULT '',
    plan_key               TEXT NOT NULL UNIQUE,
    session                TEXT NOT NULL DEFAULT '',
    started_at_utc         TEXT NOT NULL,
    completed_at_utc       TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL CHECK (status IN ('success', 'error', 'cancelled')),
    citation_observability TEXT NOT NULL CHECK (citation_observability IN ('observed', 'unknown')),
    cited_own              INTEGER NOT NULL DEFAULT 0 CHECK (cited_own IN (0, 1)),
    citation_count         INTEGER NOT NULL DEFAULT 0 CHECK (citation_count >= 0),
    brand_mentioned        INTEGER NOT NULL DEFAULT 0 CHECK (brand_mentioned IN (0, 1)),
    tool_version           TEXT NOT NULL,
    inserted_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE (question_id, question_version, provider, model, repeat_index, stage, session)
);

-- The answer text is stored (instruction section 5) and the hash lets a report
-- prove the text was not edited afterwards.
CREATE TABLE IF NOT EXISTS responses (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id                INTEGER NOT NULL UNIQUE REFERENCES runs(id),
    answer_text           TEXT NOT NULL DEFAULT '',
    answer_sha256         TEXT NOT NULL,
    raw_response_json     TEXT NOT NULL DEFAULT '',
    citation_observability TEXT NOT NULL CHECK (citation_observability IN ('observed', 'unknown')),
    error_code            TEXT NOT NULL DEFAULT '',
    error_message         TEXT NOT NULL DEFAULT '',
    input_tokens          INTEGER,
    output_tokens         INTEGER,
    tool_usage            TEXT NOT NULL DEFAULT '',
    estimated_cost        REAL,
    cost_currency         TEXT NOT NULL DEFAULT '',
    cost_status           TEXT NOT NULL CHECK (cost_status IN ('estimated', 'unknown')),
    created_at            TEXT NOT NULL
);

-- One row per cited URL. raw_url keeps what the provider returned;
-- normalized_url / dedupe_key / hostname are derived. Malformed URLs are kept
-- (source_type = 'unknown', malformed = 1) so citation counts stay additive.
CREATE TABLE IF NOT EXISTS citations (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id         INTEGER NOT NULL REFERENCES runs(id),
    response_id    INTEGER NOT NULL REFERENCES responses(id),
    position       INTEGER NOT NULL CHECK (position >= 1),
    raw_url        TEXT NOT NULL,
    normalized_url TEXT NOT NULL DEFAULT '',
    dedupe_key     TEXT NOT NULL DEFAULT '',
    hostname       TEXT,
    source_type    TEXT NOT NULL CHECK (source_type IN ('own', 'competitor', 'other', 'unknown')),
    is_own         INTEGER NOT NULL CHECK (is_own IN (0, 1)),
    malformed      INTEGER NOT NULL CHECK (malformed IN (0, 1)),
    UNIQUE (response_id, position)
);

-- Brand / competitor term hits in the answer text. ambiguous = 1 marks matches
-- that cannot be attributed (for example a brand term that is also a common noun).
CREATE TABLE IF NOT EXISTS mentions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id       INTEGER NOT NULL REFERENCES runs(id),
    response_id  INTEGER NOT NULL REFERENCES responses(id),
    term         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('brand', 'competitor')),
    matched_text TEXT NOT NULL DEFAULT '',
    occurrences  INTEGER NOT NULL DEFAULT 1 CHECK (occurrences >= 0),
    ambiguous    INTEGER NOT NULL DEFAULT 0 CHECK (ambiguous IN (0, 1)),
    UNIQUE (response_id, term, kind)
);

-- Cost / usage per run. Tokens are NULL when the provider did not report them
-- (never filled in by guessing). cost_status = unknown is not zero cost.
CREATE TABLE IF NOT EXISTS usage_records (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id         INTEGER NOT NULL UNIQUE REFERENCES runs(id),
    provider       TEXT NOT NULL,
    model          TEXT NOT NULL,
    input_tokens   INTEGER,
    output_tokens  INTEGER,
    total_tokens   INTEGER,
    tool_calls     INTEGER,
    cost_status    TEXT NOT NULL CHECK (cost_status IN ('estimated', 'unknown')),
    cost_currency  TEXT NOT NULL DEFAULT 'USD',
    estimated_cost REAL,
    price_basis    TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_runs_period ON runs (started_at_utc);
CREATE INDEX IF NOT EXISTS idx_runs_group ON runs (provider, model, stage);
CREATE INDEX IF NOT EXISTS idx_runs_question ON runs (question_id, provider, model);
CREATE INDEX IF NOT EXISTS idx_citations_run ON citations (run_id);
CREATE INDEX IF NOT EXISTS idx_citations_host ON citations (hostname);
CREATE INDEX IF NOT EXISTS idx_mentions_run ON mentions (run_id);

-- The denominator is fixed here: a run counts only when it succeeded. Unknown
-- citation observability is excluded from citation rates by the analysis layer
-- (counted, but not divided).
CREATE VIEW IF NOT EXISTS v_runs_valid AS
    SELECT * FROM runs WHERE status = 'success';
