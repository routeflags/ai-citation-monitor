# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Styled terminal output** (`charm.land/lipgloss/v2`). The human output on
  stdout now carries weight and colour for structure: table headers and the
  label column of the `run` plan are bold, a rising citation or mention rate is
  green, a falling one red, and a rate with no measurement is dim. The styling
  adds escapes only — the text and the column widths are identical with and
  without it, so a piped or logged transcript reads exactly like the terminal.
- Colour is used only when stdout is a terminal. `--json` and stderr are never
  styled, and `--no-color`, `NO_COLOR` and `TERM=dumb` turn it off. `--no-color`
  is the new global flag for the same opt-out.
- **Measurement conditions per trend bucket.** `aicm trends --json` points now carry
  `identities` (the measurement identities recorded in the bucket, distinct) and
  `mixed_conditions` (the identity fields that differ inside one bucket), so a
  consumer can tell a bucket that averaged two tool versions from one measurement.
- `aicm competitors list --json` adds `invalid`: the registered values that are public
  suffixes and would over-match. The existing `competitors` object is unchanged.

### Changed

- **A public suffix in `competitors` is a configuration error (exit 2).** It was
  already refused by `competitors add`, but a value written in `aicm.toml` or passed to
  `aicm init --competitors` was accepted and then counted every site under the suffix
  as one competitor. Existing rows in the `competitors` table are not deleted;
  `competitors list` marks them, and read-only commands keep working so existing data
  stays readable.
- **`aicm trends` prints no delta across different measurement conditions.** When
  `api_endpoint_version`, `question_set_sha256` or `tool_version` changes between
  adjacent buckets, the bucket keeps its rate and `n` but the delta is the unmeasured
  mark with `reason = conditions_differ` — the rule `compare` already applied between
  periods. `n_differs` is unchanged and is reported when the conditions agree.

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

[0.2.0]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.2.0
[0.1.3]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.3
[0.1.2]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.2
[0.1.1]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.1
[0.1.0]: https://github.com/routeflags/ai-citation-monitor/releases/tag/v0.1.0
