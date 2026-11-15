# Providers

The tool speaks to the provider APIs directly with `net/http`. No SDK is used, so
the request shape and the API version are visible in the code and are recorded in
every measurement.

> **Live API status: not verified.** The adapters are implemented from the
> published documentation and validated against fixtures that copy the shape of
> captured provider responses, served by `httptest`. Nobody has spent money on a
> live call from this repository yet, so the first live run is an owner decision,
> not a test.

## OpenAI

| Item | Value |
|---|---|
| Endpoint | `POST {base_url}/responses`, default base URL `https://api.openai.com/v1` |
| Auth | `Authorization: Bearer $OPENAI_API_KEY` (`api_key_env` in the config names the variable) |
| Search | request body `tools: [{"type": "web_search"}]` |
| Answer text | `output_text`, falling back to joining the message items' text |
| Citations | `output[].content[].annotations[]` where `type = "url_citation"` (`url`, `title`, `start_index`, `end_index`) |
| Observability | `observed` when the response contains any `annotations` field or a `web_search_call` item; `unknown` when it contains neither |
| Usage | `usage.input_tokens`, `usage.output_tokens`, `usage.total_tokens`; tool calls counted from `web_search_call` items |
| Recorded endpoint version | `responses/v1+web_search` |

Notes:

- `output_text` is a convenience join and carries no links, so citations are read
  only from the annotations. A URL that happens to appear inside the answer text is
  **not** a citation.
- The request does not force `tool_choice`. Whether the model searches is the
  provider's decision; if it answers without searching, that is a real observation
  (a judgeable answer with zero citations), not an error.
- The model name is never guessed. Set `[providers.openai] model` in the config or
  pass `--model`; a missing model is a configuration error (exit 2).

## Gemini

| Item | Value |
|---|---|
| Endpoint | `POST {base_url}/models/{model}:generateContent`, default base URL `https://generativelanguage.googleapis.com/v1beta` |
| Auth | `x-goog-api-key: $GEMINI_API_KEY` |
| Search | request body `tools: [{"google_search": {}}]` |
| Answer text | `candidates[].content.parts[].text` |
| Citations | `candidates[].groundingMetadata.groundingChunks[].web.uri` |
| Observability | `observed` when a candidate carries `groundingMetadata` (even with zero chunks); `unknown` when no candidate does |
| Usage | `usageMetadata.promptTokenCount`, `candidatesTokenCount`, `totalTokenCount`; tool calls counted from `webSearchQueries` |
| Recorded endpoint version | `generateContent/v1beta+google_search` |

Notes:

- Gemini also documents an interactions API with a step-based response. This build
  speaks `generateContent`, which is what the recorded endpoint version says. When
  that changes, the version string changes with it so old and new measurements are
  not mixed.
- A blocked prompt (`promptFeedback.blockReason`) is recorded as an error
  (`error_code = blocked`), not as a zero-citation answer.

## Anthropic (Claude)

| Item | Value |
|---|---|
| Endpoint | `POST {base_url}/v1/messages`, default base URL `https://api.anthropic.com` |
| Auth | `x-api-key: $ANTHROPIC_API_KEY`, plus `anthropic-version: 2026-10-21` |
| Search | request body `tools: [{"type": "web_search_20250305", "name": "web_search", "max_uses": 5}]` |
| Answer text | the `text` blocks of `content[]`, joined |
| Citations | `content[].citations[]` where `type = "web_search_result_location"` (`url`, `title`, `cited_text`) |
| Observability | `observed` when a `web_search_tool_result` block exists or a text block carries a `citations` field; `unknown` when neither does |
| Usage | `usage.input_tokens`, `usage.output_tokens`, tool calls from `usage.server_tool_use.web_search_requests` (or counted from the `server_tool_use` blocks when that block is missing) |
| Recorded endpoint version | `messages/v1+web_search_20250305+api_2026-10-21` |
| Required request field | `max_tokens` (setting: `[providers.anthropic] max_tokens`, default 8192) |

Notes:

- **Only the answer's own citations count.** A `web_search_tool_result` block lists
  what the search returned; a page in that list that the answer never cites is
  **not** a citation. Counting it would inflate every rate with pages the model
  rejected.
- **`total_tokens` stays empty.** The Messages API reports input and output
  tokens but no total, and this tool does not add one up (a guessed number is not
  a measurement).
- **Search errors arrive inside a 200.** When the search tool fails, the block's
  `content` is a single `{"type": "web_search_tool_result_error", ...}` object
  instead of a list. That is metadata (the search ran), not an answer error, so the
  run stays successful and is `observed` with zero citations for that search.
- **Stop reasons that make an answer unusable are errors.** `max_tokens` (the
  answer was cut off), `refusal` (the model declined — that is not a zero-citation
  answer), `pause_turn` (the turn paused for a long search; this tool sends one
  request per call) and `tool_use` (a client tool was requested; this adapter sends
  server tools only) are recorded with `error_code` equal to the reason, and stay
  out of every denominator. Raise `max_tokens` rather than accepting a truncated
  answer.
- **The tool version is part of the surface.** A new `web_search_YYYYMMDD` type or
  a new `anthropic-version` produces a new `api_endpoint_version`, so measurements
  from different surfaces are never mixed.

## Rules that apply to all of them

- **One call = one run.** The runner never batches several questions into one
  request: the conditions of a run have to be exact.
- **A timeout on every request** (`timeout_seconds`, default 90) and a
  `context.Context` that is cancelled by Ctrl+C.
- **Retries only where a retry can help:** HTTP 429, 500, 502, 503, 504 and
  retryable transport failures. `Retry-After` is honoured when present; otherwise
  exponential backoff with jitter, capped. Authentication and validation failures
  are returned at once — retrying them only burns quota.
- **Rate limit and concurrency** are set in `[limits]` (`rpm`, `concurrency`).
  The limit is per provider, so one provider does not consume another's bucket.
- **Credentials are read from the environment at call time** and are redacted from
  error messages and from the stored raw JSON. The configuration holds the variable
  name only, and `aicm init` refuses to write a value that looks like a credential.
- **Empty answers are failures.** If the provider returns no text and no citations,
  the run is recorded with `error_code = empty_answer`. Recording it as "no
  citation" would silently lower every rate.
- **The raw response is stored** (`responses.raw_response_json`) with credentials
  removed, so a citation can be re-checked later without calling the API again.

## Cost

- The tool does not price a call. It records the tokens and tool calls the
  provider reported (`usage_records`), because those are measurements the provider
  made, and leaves the money to the provider's own billing dashboard.
- A run is bounded by its plan (questions x repeats x providers), which `--limit`
  and `--dry-run` let you check before anything is sent.
- A retried request can be charged twice by the provider; the run output counts
  the retries it made.

## Adding a provider

1. Implement `providers.Provider` in a new package under `internal/providers/`.
2. Map the provider's citation metadata to `Result.Citations` and set
   `Result.Observability` — never infer a citation from the answer text.
3. Add the provider to `config.ProviderNames()` and the `[providers.*]` block.
4. Add response fixtures and an `httptest` test for cited / not cited / error /
   empty / no-metadata shapes.
