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
  internal/providers/anthropic    Messages API + web_search_20250305 (server tool)
  internal/providers/perplexity   Agent API + web_search (server tool)
  internal/providers/grok         xAI Responses API + web_search
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
`golang.org/x/text` (NFKC), `golang.org/x/time` (token bucket), `golang.org/x/net`
(IDN mapping and the public suffix list).

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

**The schema is the public contract.** Columns for a particular consumer's local
tool (for example that tool's version) are not added: evidence identity is
`tool_version` plus `api_endpoint_version`, the question-set version/SHA and
`plan_key`. A consumer that needs another shape converts an `aicm export`, not the
stored rows (D19).

| Table | Holds |
|---|---|
| `projects` | one site: `site_id`, `own_domain`, `subdomains`, `market`, `language` |
| `questions` | stable question keys with their current text |
| `question_versions` | every distinct text of a question, with a content hash |
| `question_sets` | provenance of an imported question set (version, sha256, path, `brand_terms_status`) |
| `competitors` | competitor domains, applied at report time |
| `runs` | one attempted call: conditions, plan key, status, observability, counts |
| `responses` | answer text, its sha256, the sanitised raw JSON, usage, `body_stored` |
| `citations` | raw URL, normalised URL, dedupe key, host, source type, malformed flag |
| `mentions` | brand terms that hit the answer, with occurrences and an ambiguity flag |
| `usage_records` | tokens and tool calls the provider reported (no cost) |
| `v_runs_valid` | `SELECT * FROM runs WHERE status = 'success'` — the denominator |

`question_sets` also carries the declared status of the imported brand terms
(`proposed` / `confirmed`, migration 0003). It is provenance: a report resolves it
from the `question_set_sha256` recorded on its runs, never from the YAML file on
disk, and the value changes no rate. An empty value means "not declared" (a set
recorded before the column existed, or a run with no question set at all) and is
never read as confirmed. Because a set is identified by the SHA of the whole file,
an identical re-import cannot change the recorded status: `questions import` prints
the recorded value and warns when the file declares a different one.

### Append-only

Nothing in the tool issues `UPDATE` against `runs`, `responses`, `citations`,
`mentions` or `usage_records`. A repeat of the same call is skipped because
`runs.plan_key` is unique (`sha256` of every condition) and `runs` additionally
holds `UNIQUE (question_id, question_version, provider, model, repeat_index, stage,
session)` — one row per measurement condition, whether it succeeded or not.

```
plan_key = sha256(project, question, question_version, provider, model,
                  repeat_index, stage, session)
```

**Two hashes look alike and are not.** `question_versions.version` is a short,
prefixed identity (`sha:<12 hex>`) because it names a version in reports;
`responses.answer_sha256` is the full 64-character hex of the answer text with no
prefix, because it is compared byte for byte by `aicm verify`. Storing one in the
other's place makes a correct answer look edited.

`responses.body_stored` is 1 when the answer text and the raw JSON are there and 0
when the run was recorded hash-only (`--store hash-only`, `store_answer_body =
false`). A hash-only row is a choice, not evidence loss: `verify` skips the hash and
emptiness checks for it, and nothing in `report` or `export` needs the text.

The second constraint is why a recorded *failure* is not resent in the same
session: the condition already has its row. `aicm run` reports those calls and
points at `--session`, which is how a retry becomes a new condition.

The session defaults to today's JST date and can be set with `--session`, which is
how "measure again tomorrow" and "resume the interrupted run" are both expressed
without a second concept.

### Failures and unknown

- `runs.status`: `success` / `error` / `cancelled`. A failed call is a row.
- `runs.citation_observability`: `observed` (the provider returned citation
  metadata, possibly empty) or `unknown` (it returned none). `unknown` is excluded
  from citation-rate denominators and counted in the report. Status and observability
  are separate dimensions: a run is in exactly one status, and a failed run normally
  has no observed citation metadata either, so it is counted in both `error` and
  `unknown` (`docs/metrics.md`).

## What counts as your site

`own_domain` decides whether a citation is yours, so it is a measurement rule, not a
presentation setting (`NormalizerVersion` is bumped when it changes and is written
into every run as part of `tool_version`).

- **The comparison is by label, in punycode.** Both sides go through
  `domain.CanonicalHost` (lower case, no trailing dot, `golang.org/x/net/idna`), so
  `https://例え.テスト/` and `https://xn--r8jz45g.xn--zckzah/` are one population.
- **`subdomains = true` matches on a label boundary only**: `www.example.com`
  matches `example.com`, `notexample.com` never does.
- **A public suffix is not an own domain.** `own_domain = "github.io"` or `"co.jp"`
  is a configuration error (exit 2): every customer under that suffix would count as
  one site, and the citation rate would be meaningless. The check uses
  `golang.org/x/net/publicsuffix`, including the private section (hosting suffixes).
- **Known limit**: a public suffix in `competitors` is not rejected by the config
  loader, only `own_domain` is. Registering `co.jp` in `aicm.toml` would over-match the
  same way. `aicm competitors add` does refuse a public suffix, because a competitor row
  stands for a whole site (D23).

## What a competitor row stands for

The competitor citation rate is counted per site, while the citation share table is
counted per host. `domain.AggregationDomain` decides a citation's row: with
`subdomains = true` it is the most specific configured domain (`own_domain` or a
competitor) that contains the host, and otherwise the host's registrable domain
(eTLD+1, from `golang.org/x/net/publicsuffix`). With `subdomains = false` the row is the
exact host, because that setting is about the unit of comparison. The same function
decides the row of a configured competitor, so a registration deeper than its
registrable domain stays its own row instead of absorbing its siblings (D23).

## Measurement identity

`analysis.MeasurementIdentity` is the set of conditions a rate has to share before it
can be read as the same measurement: `api_endpoint_version`, `tool_version`,
`question_set_version` and `question_set_sha256`. `compare` refuses a delta when two
periods differ on three of them (`api_endpoint_version`, `question_set_sha256`,
`tool_version`; an empty value counts as absent, not as a difference), and `Build`
records every identity field that differs inside one group as `mixed_conditions`
instead of pretending the average is one measurement. The group key stays
`provider x model x stage` (invariant 2), so the rates keep their denominator and the
mix is visible rather than silently removed (D24). `trends` does not yet compare
conditions between buckets.

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
