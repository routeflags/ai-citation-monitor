# AI Citation Monitor (`aicm`)

![AI Citation Monitor: the CLI title card over its own metrics — citation rate, brand mentions, competitor comparison, trends](docs/assets/hero.png)

**Measure how often AI-generated answers cite your website—reproducibly, from the command line.**

<sub>The image is an illustration; the numbers in it are not a measurement.</sub>

AI Citation Monitor is an open-source, Go-based CLI for measuring **GEO (Generative Engine Optimization)** and **AEO (Answer Engine Optimization)** outcomes. It runs a versioned set of questions against search-enabled AI APIs, stores citation evidence in SQLite, and reports visibility, competitor comparisons, and changes over time.

[日本語](README.ja.md) · [CLI reference](docs/cli-reference.md) · [Metrics](docs/metrics.md) · [Providers](docs/providers.md)

> [!IMPORTANT]
> **These are sample-based observations, not overall AI search rankings.** Results describe the configured questions, providers, models, stages, and runs only. They do not represent every user's ChatGPT, Gemini, or Claude experience. The same disclaimer is mandatory in generated reports.

## Why use it?

Traditional SEO analytics cannot directly tell you whether a particular AI answer cited your website. `aicm` makes that question testable and auditable:

- **Evidence-based citation tracking:** extract citations from provider response metadata, not URLs guessed from answer text.
- **Brand and question visibility:** track brand mentions, per-question citation rates, and question coverage.
- **Competitor comparisons:** measure competing domains under the same test conditions.
- **Historical trends:** compare periods and display daily, weekly, or monthly changes.
- **Reproducibility:** preserve question versions, model and API conditions, timestamps, answer hashes, and—when enabled—raw responses.
- **Automation-friendly CLI:** JSON output, stable exit codes, and CSV/JSON/JSONL exports; no web UI or hosted account required.

## Installation

`aicm` is one static binary — no C compiler, no runtime dependency — for macOS
and Linux on amd64 and arm64, and for Windows on amd64.

### Prebuilt binary

Download the archive for your platform from the [latest release](https://github.com/routeflags/ai-citation-monitor/releases/latest), together with the `checksums.txt` published beside it:

| Platform | Archive |
|---|---|
| macOS, Apple silicon | `ai-citation-monitor_<version>_darwin_arm64.tar.gz` |
| macOS, Intel | `ai-citation-monitor_<version>_darwin_amd64.tar.gz` |
| Linux, x86-64 | `ai-citation-monitor_<version>_linux_amd64.tar.gz` |
| Linux, arm64 | `ai-citation-monitor_<version>_linux_arm64.tar.gz` |
| Windows, x86-64 | `ai-citation-monitor_<version>_windows_amd64.zip` |

Check the archive against the published checksum before running it:

```bash
VERSION=0.1.2                                       # the release you downloaded
ARCHIVE=ai-citation-monitor_${VERSION}_darwin_arm64.tar.gz   # your platform

grep "$ARCHIVE" checksums.txt | shasum -a 256 -c -  # Linux: ... | sha256sum -c -
tar xzf "$ARCHIVE"
mkdir -p ~/.local/bin && install -m 0755 aicm ~/.local/bin/aicm   # or: sudo install -m 0755 aicm /usr/local/bin/aicm
aicm version
```

The archive has no directory level: it holds `aicm`, `LICENSE`, the READMEs, `docs/` and `examples/`. On Windows, compare `(Get-FileHash .\ai-citation-monitor_0.1.2_windows_amd64.zip -Algorithm SHA256).Hash` with the line in `checksums.txt`, expand the `.zip`, and put the folder on your `PATH`.

### With Go

Go **1.26.11 or newer** (per `go.mod`):

```bash
go install github.com/routeflags/ai-citation-monitor/cmd/aicm@latest
```

`go install` writes to `$(go env GOBIN)`, or to `$(go env GOPATH)/bin` when `GOBIN` is unset; that directory has to be on your `PATH`.

### From source

```bash
git clone https://github.com/routeflags/ai-citation-monitor
cd ai-citation-monitor
make aicm     # ./aicm, stamped with the git version (`go build -o aicm ./cmd/aicm` also works)
make check    # gofmt, vet, build, test, race, lint, govulncheck — what CI runs
```

There is no Homebrew, apt/dnf, Scoop or container package yet; pick one of the three ways above.

## Quick start

### 1. Create a project and import questions

```bash
aicm init --non-interactive \
  --site-id example-shop \
  --own-domain example.shop \
  --brand-terms "Example"

aicm questions import --file examples/questions.yml
```

The example question set uses `site_id: example-shop`. For a real project, replace the sample domain, brand terms, and questions with your own and keep their versions stable between comparisons.

### The question file

A question set is a small YAML file. The minimum that works:

```yaml
set_version: "1.0.0"
site_id: example-shop
own_domain: example.shop
market: ja-JP
language: ja

brand_terms:
  - Example
competitors: []

questions:
  - { id: Q01, text: "What is Example?", category: definition }
  - { id: Q02, text: "How does Example compare with Rival?", category: comparison }

stages:
  smoke:
    runs_per_question: 1
    question_ids: [Q01]
  initial:
    runs_per_question: 3
    question_ids: [Q01, Q02]
```

`aicm questions import --file questions.yml --check` validates the file and writes nothing: the stage nesting (`smoke` ⊆ `initial` ⊆ `regular` when present), `runs_per_question` (1–5), and the round-trip count per stage. Drop `--check` to store the set; `aicm questions list` shows what is stored.

- `set_version` is the identity of the set. Change it in the same edit that changes a question text, or a later comparison reads as a change in citations when only the wording moved.
- `category` is one of `definition`, `comparison`, `regulation`, `effect`, `safety`, `usage`, `purchase`, `list`, `identification`, `dosage`.
- `competitors: []` is allowed. The import warns (`I042`) that the share table will show own / other only.

### 2. Preview the calls before spending money

```bash
aicm run --provider openai --model YOUR_SUPPORTED_MODEL \
  --stage smoke --repeat 1 --dry-run
```

`--dry-run` does not call a provider or require an API key. The tool does not calculate a monetary quote; check provider pricing separately.

### 3. Run a measurement

```bash
export OPENAI_API_KEY="your-api-key"

aicm run --provider openai --model YOUR_SUPPORTED_MODEL \
  --stage smoke --repeat 1 --yes
```

Models must be specified with `--model` or in `aicm.toml`; the tool does not guess a model. **A real run calls a paid third-party API.** Start with a small question set and `--limit` if needed. Keep API keys out of configuration files and version control.

### 4. Inspect the results

```bash
aicm report --period 30d
aicm compare --period 30d --baseline previous
aicm trends --interval weekly --period 90d
aicm verify
```

Reports are written to the configured report directory (default `reports/`). For CSV or JSON data use `aicm export`.

## Metrics

| Metric | Interpretation |
|---|---|
| **GEO citation rate** | Share of successful answers with judgeable citation metadata that cited your domain at least once |
| **Brand mention rate** | Share of successful answers containing a registered brand term |
| **AEO per-question citation rate** | Citation rate calculated separately for each question |
| **AEO question coverage** | Share of judgeable questions that cited your domain at least once |
| **Competitor citation rate** | Share of judgeable answers citing each registered competitor domain |
| **Citation share** | A domain's share of citations in the stated group and denominator |
| **Trends** | Time-series measurements and changes in percentage points from the preceding bucket |

**Interpretation matters:** every rate reports its denominator `n`. `n = 0` means *not measured*, never 0% visibility. Failed calls or responses without citation observability are not silently treated as uncited answers. Rates are grouped by **provider × model × stage**, rather than mixed across incompatible conditions. Competitor citation rates can add up to more than 100% because an answer can cite multiple domains. For exact numerators and denominators, see [metrics definitions](docs/metrics.md).

## CLI commands

| Command | Purpose |
|---|---|
| `aicm init` | Initialize project configuration and SQLite storage |
| `aicm questions add\|list\|import` | Manage and version questions |
| `aicm run` | Execute, preview, and record provider calls |
| `aicm report` | Generate a Markdown report |
| `aicm compare` | Compare periods under recorded conditions |
| `aicm trends` | Display daily, weekly, or monthly trends |
| `aicm competitors add\|list\|remove` | Manage competitor domains |
| `aicm export` | Export CSV, JSON, or JSONL |
| `aicm verify` | Check stored evidence and referential integrity |
| `aicm version` | Display build and provider API information |

Global `--json` mode keeps stdout machine-readable and sends diagnostics to stderr. See the [complete CLI reference](docs/cli-reference.md) for command flags, output examples, and exit codes.

## Supported providers and validation status

| Provider | Integration | Evidence level |
|---|---|---|
| OpenAI | Responses API + `web_search` | One live single-question observation on **2026-11-15**; offline fixture tests |
| Google Gemini | `generateContent` + `google_search` | One live single-question observation on **2026-11-15**; offline fixture tests |
| Anthropic Claude | Messages API + `web_search_20250305` | One live single-question observation on **2026-11-15**; offline fixture tests |

These observations verify specific response shapes, **not universal model support or a completed production measurement**. A full live question-set study has not been performed. Tests also cover citation extraction, retry behavior, rate limits, concurrent calls, and interruption/resume behavior. See [provider details](docs/providers.md).

## Privacy, reliability, and cost

- **Local-first:** SQLite is the source of truth; no hosted backend or external database is required.
- **Evidence retention:** full mode stores answer text and provider JSON for later inspection, with credential redaction. Use `aicm run --store hash-only` or `store_answer_body = false` to omit those bodies; it preserves aggregate metrics but limits later reinspection.
- **Resumable:** previously recorded question/provider/model/repeat/stage/session conditions are skipped on rerun. This includes recorded failures; use a **new `--session`** after fixing a failure if you want to retry it.
- **Explicit spending:** interactive runs require confirmation; non-interactive real runs require `--yes`. Retries at the HTTP level may still incur provider charges.
- **No ranking promises:** citation rates depend on question selection, geography/language settings, provider behavior, model versions, and the observation period.

## Scope and project documentation

Phase 1 is a **CLI-only** tool supporting OpenAI, Gemini, and Anthropic. It does not include a web UI, server, scheduler, or Perplexity adapter.

- [Architecture](docs/architecture.md) — code structure, data flow, storage
- [Metric definitions](docs/metrics.md) — exact calculation rules
- [Provider integrations](docs/providers.md) — response metadata and API caveats
- [CLI reference](docs/cli-reference.md) — commands, flags, error handling
- [Configuration example](examples/config.toml) and [questions example](examples/questions.yml)
- [Contributing](CONTRIBUTING.md) and [Security](SECURITY.md)
- [Phase 1 decisions (Japanese)](documents/decisions/)

## Philosophy

This column outlines the principles that guide how we engage with OSS. These principles are the reason we continue our day-to-day OSS activities and writing. It reframes long-unreviewed social structures as a form of technical debt. We would be glad if you take a look.

- [Engineering Blog: Society Also Has Technical Debt](https://www.thch-vape.shop/guide/column/git-log--oneline--all--society)

## Related Article

- [How to Measure AI SEO Performance: Tracking GEO/AEO Citation Rates](https://blog.routeflags.com/2026/11/16/ai-seoの効果測定方法とは？geo・aeoの引用率を可視化す/) — An in-depth guide to AI citation monitoring, published by ROUTE FLAGS Co., Ltd. (Japanese).

## License

Apache License 2.0. See [LICENSE](LICENSE).
