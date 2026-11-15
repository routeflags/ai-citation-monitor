# ai-citation-monitor (`aicm`)

Measure whether AI answers cite your site: citation rate, brand mention rate,
question coverage, competitor citations and trends, recorded so that every number
can be reproduced later.

> **These numbers are measurements of a sample.** An AI service does not expose
> its whole answer log, so every rate here is the observation of "this question
> set, this model, this many runs". They do **not** represent the consumer
> ChatGPT / Gemini UI or every user's answer. Every report starts with this
> statement, and it cannot be turned off.

日本語版: [README.ja.md](README.ja.md).

## Status (Phase 1 / MVP)

The table is deliberate: a row is "verified" only when a check in this repository
proves it. Nothing claims to have been measured against a live API.

| Area | State |
|---|---|
| `init` / `questions add,list,import` / `run --dry-run` / `report` / `compare` / `trends` / `export` / `verify` | **Verified** by `go test ./...` (offline) |
| OpenAI adapter (Responses API + `web_search`) | **Implemented, not verified against the live API.** Request/response shapes come from the published documentation and from captured responses used as reference; the tests serve those shapes from `httptest` |
| Gemini adapter (`generateContent` + `google_search`) | **Implemented, not verified against the live API.** Same caveat |
| Citation extraction from provider metadata | **Verified** with fixtures for cited / not-cited / error / empty / no-metadata responses |
| Retry (429 / 502 / 503 / 504, `Retry-After`), rate limit, concurrency, Ctrl+C resume | **Verified** by unit tests |
| Live measurement of any provider | **Not done.** It spends money and needs a credential, so it only happens with an explicit owner decision |

## What it measures

Seven numbers, each with its denominator always printed (`docs/metrics.md`):

1. **GEO citation rate** — of the answers that succeeded and returned citation
   metadata, the share that cited your domain at least once.
2. **Brand mention rate** — of the successful answers, the share that mentioned a
   registered brand term.
3. **AEO question citation rate** — per question, the share of answers that cited
   you.
4. **AEO question coverage** — of the questions with at least one judgeable
   answer, the share that ever produced a citation of you.
5. **Competitor citation rate** — per domain, the share of answers that cited it.
   One answer can cite several domains, so the column does not add up to 100%.
6. **Citation share** — per domain, its portion of all citations in the group.
   The scope of the denominator is printed.
7. **Trend** — daily / weekly / monthly series with the difference in percentage
   points against the previous bucket.

Two rules are enforced in the data model, not in the documentation:

- **No totals across providers, models or stages.** A rate divides inside one
  `provider x model x stage` group.
- **`n = 0` is `— (not measured)`, never `0.0%`.** A failure or an answer without
  citation metadata is counted and excluded from the denominator; it is never
  treated as "no citation".

## Install

Prebuilt binaries (macOS arm64/amd64, Linux arm64/amd64, Windows amd64) are
published on the Releases page. Or build from source:

```bash
go install github.com/routeflags/ai-citation-monitor/cmd/aicm@latest
```

## Quickstart

```bash
# 1) configuration + project row (no credential is written)
aicm init --non-interactive --site-id example-shop --own-domain example.shop \
  --brand-terms "Example"

# 2) questions (YAML contract or CSV)
aicm questions import --file examples/questions.yml

# 3) look before you spend: plan, estimate, auth check, zero network calls
#    (a missing key is reported, not required: the plan prints anyway)
aicm run --provider openai --stage smoke --repeat 1 --dry-run

# 4) export the key into the environment (the config only names the variable)
export OPENAI_API_KEY=...

# 5) run it
aicm run --provider openai --stage smoke --repeat 1 --yes

# 6) aggregate and report
aicm report --period 30d

# 7) check that the records still hold their own evidence
aicm verify
```

`aicm run` with no `--dry-run` stops at the first missing piece: a bad
configuration exits 2, a missing credential exits 3 (and nothing is sent).

## Commands

| Command | Purpose |
|---|---|
| `aicm init` | Create `aicm.toml` and the database project row (interactive or `--non-interactive`) |
| `aicm questions add\|list\|import` | Manage the fixed question set |
| `aicm run` | Ask the questions of the official search-enabled APIs and record every call |
| `aicm report` | Markdown report for a period |
| `aicm compare` | Two periods, per group, difference in percentage points |
| `aicm trends` | Daily / weekly / monthly series |
| `aicm competitors add\|list\|remove` | Domains to compare against |
| `aicm export` | CSV / JSON / JSONL from the database |
| `aicm verify` | Check the database against itself (missing answers, broken references, edited text) |
| `aicm version` | Tool version and the API surfaces this build speaks |

`--json` makes stdout JSON only (logs go to stderr). Full flags and output shapes:
[docs/cli-reference.md](docs/cli-reference.md).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Validation failed, or part of the measurement failed (the records are kept) |
| 2 | Configuration or usage problem |
| 3 | Credential missing — nothing was sent |
| 4 | API or transport failure |

Every failure also carries a stable `reason` string (`--json`: `error.reason`), so
a script can tell "no credential" from "bad config" without parsing text.

## What is stored, and what is not

- The database is the source of truth. One row per attempted call, with the
  conditions attached: provider, model, API endpoint version, search enabled,
  measurement method, question set version and hash, start and end time (UTC).
- The answer text and the provider's raw JSON are stored, so a citation can be
  re-checked later. Credential values are never written: the adapters redact them
  from error messages and from the stored raw JSON.
- A call is recorded even when it fails. A resumed run skips the calls already
  recorded (same question version, provider, model, repeat, stage and session),
  so a Ctrl+C never pays twice.
- `aicm verify` checks the database against itself: a run without an answer, a
  citation pointing at a missing response, an answer whose stored hash no longer
  matches, a successful run with an empty answer.

## Documentation

- [docs/architecture.md](docs/architecture.md) — packages, data flow, schema
- [docs/metrics.md](docs/metrics.md) — the exact numerator and denominator of every rate
- [docs/providers.md](docs/providers.md) — endpoints, citation metadata, retries, what is unverified
- [docs/cli-reference.md](docs/cli-reference.md) — every command and flag
- [documents/decisions](documents/decisions/) — the Phase 1 decision record (Japanese)

## Requirements and scope

- Go 1.26 or newer to build; a single static binary to run.
- CLI only. No web UI, no server, no external database, no scheduled execution.
- Phase 1 measures OpenAI and Gemini. Perplexity is out of scope for now.

## License

Apache-2.0. See [LICENSE](LICENSE).
