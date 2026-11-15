# Architecture

## Shape of the tool

```
cmd/aicm            main(): os.Exit(cli.Execute())
internal/cli        command tree, exit codes, output routing
internal/config     aicm.toml (TOML), defaults, validation, warnings
internal/question   question-set inputs (YAML contract, CSV)
internal/runner     plan, resume, concurrency, record every attempt
internal/providers  provider interface + Result
  internal/providers/httpclient   the only package that dials the network
  internal/providers/openai       Responses API + web_search
  internal/providers/gemini       generateContent + google_search
internal/domain     URL normalisation, own-domain match, brand mention match
internal/storage/sqlite  migrations, append-only writes, reads, integrity checks
internal/analysis   the only definition of every rate (groups, coverage, share, trends)
internal/reporting  Markdown report + file naming (bilingual labels)
internal/export     CSV / JSON / JSONL
internal/i18n       English / Japanese presentation strings
migrations          embedded SQL, applied in order, tracked in schema_migrations
```

Dependencies, deliberately few: `spf13/cobra` (CLI), `gopkg.in/yaml.v3` (question
sets), `BurntSushi/toml` (config), `modernc.org/sqlite` (CGO-free SQLite),
`golang.org/x/text` (NFKC), `golang.org/x/time` (token bucket).

## One call, end to end

```
aicm run
  -> config.Load            (defaults, validation, secrets stay in the environment)
  -> storage.Open           (migrations, project row)
  -> runner.BuildPlan       (question x provider/model x repeat; skip recorded plan keys)
  -> auth check             (environment variable per provider; nothing sent when missing)
  -> runner.Execute         (bounded concurrency, one attempt per call)
       -> providers/httpclient.Do   (timeout, RPM, retry on 429/5xx only)
       -> provider adapter          (request shape, citation metadata, usage)
       -> domain normalisation      (citations, own domain, brand terms)
       -> storage.InsertRun         (one transaction: run + response + children)
  -> summary + exit code
```

`aicm report`, `compare`, `trends` and `export` only read the database; they never
call a provider.

## The provider boundary

`providers.Provider` is the only interface the runner knows:

```go
Name() string
EndpointVersion() string
Ask(ctx context.Context, req Request) (Result, error)
```

A `Result` always exists — success, provider error, transport error or cancellation
— so a failure is recorded like any other outcome. Provider JSON never leaves the
adapter package: the runner only sees extracted text, citations, usage numbers and
an observability flag.

`EndpointVersion()` is written into every run (`api_endpoint_version`). When an
adapter starts speaking a different API surface, the old and new measurements are
separated by that column instead of being silently mixed.

## Database

The tool owns the schema (`migrations/`), so a consumer repository only points
`--db` at its own file. `schema_migrations` records what has been applied;
migration 0001 uses `CREATE TABLE IF NOT EXISTS` so it can adopt a database that
was created by an older build.

| Table | Holds |
|---|---|
| `projects` | one site: `site_id`, `own_domain`, `subdomains`, `market`, `language` |
| `questions` | stable question keys with their current text |
| `question_versions` | every distinct text of a question, with a content hash |
| `question_sets` | provenance of an imported question set (version, sha256, path) |
| `competitors` | competitor domains, applied at report time |
| `runs` | one attempted call: conditions, plan key, status, observability, counts |
| `responses` | answer text, its sha256, the sanitised raw JSON, usage |
| `citations` | raw URL, normalised URL, dedupe key, host, source type, malformed flag |
| `mentions` | brand terms that hit the answer, with occurrences and an ambiguity flag |
| `usage_records` | tokens and tool calls the provider reported (no cost) |
| `v_runs_valid` | `SELECT * FROM runs WHERE status = 'success'` — the denominator |

### Append-only

Nothing in the tool issues `UPDATE` against `runs`, `responses`, `citations`,
`mentions` or `usage_records`. A repeat of the same call is skipped because
`runs.plan_key` is unique:

```
plan_key = sha256(project, question, question_version, provider, model,
                  repeat_index, stage, session)
```

The session defaults to today's JST date and can be set with `--session`, which is
how "measure again tomorrow" and "resume the interrupted run" are both expressed
without a second concept.

### Failures and unknown

- `runs.status`: `success` / `error` / `cancelled`. A failed call is a row.
- `runs.citation_observability`: `observed` (the provider returned citation
  metadata, possibly empty) or `unknown` (it returned none). `unknown` is excluded
  from citation-rate denominators and counted in the report.

## Integrity

`aicm verify` checks the database against itself, because the answer text and the
raw response live in the database — there is no separate cassette file to lose.
It reports runs without a response, children pointing at missing rows, a stored
`answer_sha256` that no longer matches the text, a successful run with an empty
answer, a `citation_count` that disagrees with the rows, and a run referring to a
question version that is not stored.

## Testing

- No test talks to a provider. The adapters are tested with `httptest` servers
  that serve real response fixtures (cited, not cited, error, empty, no metadata).
- `internal/cli` runs the real command tree against a local test server, so
  configuration, database, aggregation and reporting are exercised together.
- `TestNoProviderSDKIsImported` and `TestNoExternalMeasurementToolIsShelledOut`
  fail the build if a provider SDK, `os/exec`, or a call to another measurement
  tool appears in the source.
