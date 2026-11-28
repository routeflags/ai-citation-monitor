# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-11-28

### Added

- Styled human-readable terminal output with `--no-color`, `NO_COLOR`, and
  `TERM=dumb` support. JSON and stderr remain unstyled.
- `aicm trends --json` now includes each bucket's distinct `identities` and
  `mixed_conditions`; `aicm competitors list --json` adds `invalid` without
  changing the existing `competitors` object.
- Permanent exhaustive rounding coverage for all 4,504,500 `(n, d)` pairs with
  `n ≤ d ≤ 3000`, checked against an independent integer oracle.

### Changed

- **Competitor citation rates are attributed per site, not per cited host.** A
  registered domain now includes its subdomains when configured; citation share
  remains host-based. This changes the values in
  `aicm report --json` `competitor_rate[].domain` (for example,
  `www.rival.example` may now be attributed to `rival.example`).
- Reports expose `mixed_conditions` when a group contains different measurement
  identities rather than presenting the average as one measurement.
- A public suffix in `competitors` is a configuration error (exit 2). Existing
  database rows are preserved; `competitors list` marks invalid values and
  read-only commands remain usable.
- `aicm trends` suppresses deltas across adjacent buckets when
  `api_endpoint_version`, `question_set_sha256`, or `tool_version` differs,
  using `reason = conditions_differ`. `n_differs` is unchanged when conditions
  match.
- Error status and citation observability are explicitly separate dimensions;
  failed and unobserved runs may appear in both counts.

### Fixed

- Detect provider response bodies exceeding the 16 MiB limit instead of passing
  truncated JSON to adapters.
- Correct trend denominators when failed and citation-unknown runs share a bucket.
- Use `int64` for rate-rounding arithmetic to match the proof model's integer
  range.

## [0.2.0] - 2026-11-28

### Added

- **Perplexity Agent API provider** (`perplexity`, `agent/v1+web_search`). It
  sends `POST {base_url}/agent` with the server-side `web_search` tool and
  records the `search_results` entries together with any `url_citation`
  annotations, counting each URL once. The published web-search documentation
  names `search_results` as the source of truth for citations, so an answer with
  an empty `annotations` array still records the sources it used.
- **xAI Grok Responses API provider** (`grok`, `responses/v1+web_search+xai`).
  It sends `POST {base_url}/responses` with `web_search` and records only
  `url_citation` annotations that carry a non-empty span in the answer. The
  top-level `citations` list (and an annotation with a `0..0` span) is the set of
  sources the search saw, not an attribution, and is not counted.
- `[providers.perplexity]` and `[providers.grok]` configuration blocks,
  `PERPLEXITY_API_KEY` / `XAI_API_KEY` defaults, `aicm init` output and
  `--provider perplexity|grok` support.
- Adapter fixtures and `httptest` tests for cited / not cited / error / empty /
  no-metadata / unpositioned shapes, plus one CLI end-to-end test per provider.
- One live single-question observation per new provider, recorded in
  `providers.LiveObservation`: Perplexity `openai/gpt-5.6-sol` and Grok
  `grok-4.7`, both on 2026-11-28.

### Changed

- Provider docs, architecture, CLI reference, README (English and Japanese) and
  the example configuration list the two new surfaces.
- `aicm version` reports the live observation for every supported provider.

## [0.1.3] - 2026-11-16

### Fixed

- The database-directory error no longer names the directory twice.

### Documentation

- Installation paths, a smallest question-file example, the project philosophy
  and the owner's debug-verification runbook.

## [0.1.2] - 2026-11-16

### Added

- `--store hash-only` / `store_answer_body = false`, for a database that is
  committed without answer bodies.
- `make check`, mirroring the CI gate (gofmt, vet, build, test, race, lint,
  govulncheck).

### Fixed

- The report language follows the site language instead of always writing
  English, and the Japanese report typo is fixed.
- `aicm version` states which providers were observed live.
- Integrity checks are pinned to broken rows.

### Documentation

- Three installation paths for the release archives.

## [0.1.1] - 2026-11-15

### Fixed

- Gemini grounding redirects record the site named in the chunk, not the
  redirect URL, so an own citation is no longer hidden behind a Google host.

## [0.1.0] - 2026-11-15

### Added

- `aicm`, a Go CLI for measuring AI answer citations: questions, run, report,
  compare, trends, export and verify.
- OpenAI Responses, Gemini `generateContent` and Anthropic Messages adapters,
  each with its server-side web search tool.

### Changed

- The cost and budget feature was removed. The tool records the usage the
  provider reports but does not price a call.

### Fixed

- Brand terms match on word boundaries rather than by length.

[0.3.0]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.3.0
[0.2.0]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.2.0
[0.1.3]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.3
[0.1.2]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.2
[0.1.1]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.1
[0.1.0]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.0
