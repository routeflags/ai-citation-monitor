# Providers

The tool speaks to the provider APIs directly with `net/http`. No SDK is used, so
the request shape and the API version are visible in the code and are recorded in
every measurement.

> **Live API status: observed once per provider** (2026-11-15 / 2026-11-28).
> OpenAI `gpt-6.1-sol`, Gemini `gemini-3.1-pro-preview`, Anthropic
> `claude-sonnet-5-5`, Perplexity `openai/gpt-5.6-sol` and xAI `grok-4.7` have
> each been run against the real service with one question and one model, and the
> citation/usage extraction matched. One observation is not a supported-model
> list: CI never calls a provider, and the tests serve documented response shapes
> from `httptest`.

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
- **A grounding chunk's `uri` is often an opaque redirect.** A live answer returns
  `https://vertexaisearch.cloud.google.com/grounding-api-redirect/...` and names
  the real site in the chunk (`web.domain`, else `web.title`, e.g. `go.dev`). The
  adapter carries that name as the citation's source host, so the record keeps the
  URL the provider gave and the site the provider meant; without it every Gemini
  citation would look like Google and `cited_own` could never be true
  (`NormalizerVersion` 3). A title that is a sentence rather than a bare host is
  ignored, and the URL is read as usual.
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

## Perplexity (Agent API)

| Item | Value |
|---|---|
| Endpoint | `POST {base_url}/agent`, default base URL `https://api.perplexity.ai/v1` |
| Auth | `Authorization: Bearer $PERPLEXITY_API_KEY` (`api_key_env` in the config names the variable) |
| Search | request body `tools: [{"type": "web_search"}]` |
| Answer text | the `output_text` parts of the `message` output item, joined |
| Citations | the `search_results` item's `results[]` (`url`, `title`) plus `output[].content[].annotations[]` where `type = "url_citation"` (`url`, `title`, `start_index`, `end_index`); the same URL is counted once |
| Observability | `observed` when a content part carries an `annotations` field (even an empty array) or the response carries a `search_results` item; `unknown` when it carries neither |
| Usage | `usage.input_tokens`, `usage.output_tokens`, `usage.total_tokens`; tool calls from `usage.tool_calls_details.search_web.invocation`, else counted from the `search_results` items |
| Recorded endpoint version | `agent/v1+web_search` |
| Live status | observed once (`openai/gpt-5.6-sol`, 2026-11-28): 15 `search_results`, `annotations: []`; otherwise tested with fixtures |

Notes:

- **The search sources Perplexity reports are the citations.** The web-search
  documentation says to treat each `search_results` entry's `id` and `url` as the
  source of truth for citations, whether or not the model inserted `[N]` markers,
  so the adapter records those entries and any `url_citation` annotations the
  message carries, counting each URL once. It never parses the answer text
  (invariant 4): a URL that merely appears there is not a citation. This is
  provider-specific — Anthropic has a separate per-text-block citation list, so its
  candidate list is not counted (see the Anthropic section); Perplexity has no such
  separate list.
- **A search_results item is also the observability signal.** An item (even with an
  empty `results` array) or an `annotations` field makes the run `observed`; a
  response with neither is `unknown`, never "zero citations".
- **The API surface is multi-provider.** `model` may name `openai/...` or
  `anthropic/...`; the `provider` column names the Perplexity API surface, not the
  model vendor, so compare models through the `model` column.
- **`/agent` is the recorded path.** `https://api.perplexity.ai/v1/responses` is an
  OpenAI-compatible alias of the same service; this build spells `/agent` so the
  record says which surface produced the measurement.
- **Anything but `completed` is a failure.** `failed` (with the provider's own code
  when it sent one), `incomplete`, `in_progress`, `queued` (the tool does not poll)
  and `cancelled` (a provider cancellation, recorded as `cancelled_by_provider` and
  kept apart from the operator's Ctrl+C) stay out of every denominator.
- **`stream` is not sent and `background` is not used.** One request is one
  non-streaming answer, which is one run.

## xAI Grok

| Item | Value |
|---|---|
| Endpoint | `POST {base_url}/responses`, default base URL `https://api.x.ai/v1` |
| Auth | `Authorization: Bearer $XAI_API_KEY` (`api_key_env` in the config names the variable) |
| Search | request body `tools: [{"type": "web_search"}]` |
| Answer text | the `output_text` content of the `message` items, joined |
| Citations | `output[].content[].annotations[]` where `type = "url_citation"` **and the span is non-empty** (`url`, `title`, `start_index`, `end_index`); a 0..0 span is a source the search saw, not an attribution |
| Observability | `observed` when any `annotations` field is present (even an empty array) or a `web_search_call` item exists; `unknown` when neither does |
| Usage | `usage.input_tokens`, `usage.output_tokens`, `usage.total_tokens`; tool calls from `usage.server_side_tool_usage_details.web_search_calls`, or counted from the `web_search_call` items when that block is missing |
| Recorded endpoint version | `responses/v1+web_search+xai` |

Notes:

- **The top-level `citations` array is not the answer's citation list.** xAI
  documents a `citations` list on a search-enabled response as every URL the
  search *encountered*, and says the agent may look at a source and still not use
  it; the Responses response schema does not even declare the field. Either way it
  is not read. Only a `url_citation` annotation with a non-empty span is counted:
  the span is the position in the answer, and a zero-length span is not an
  attribution. A candidate list never makes a response `observed` on its own.
- **A live answer returned unpositioned annotations.** On 2026-11-28 a
  `grok-4.7` answer carried 96 `url_citation` annotations, all with
  `start_index = end_index = 0`, and no `[[N]](url)` markers in the text. They are
  the sources the search saw, so the run is `observed` with zero citations, not
  96. A citation is only recorded when the answer placed it at a span.
- **Inline citations are left at the API default.** The request sends no
  `include`. `include: ["no_inline_citations"]` would remove the positional links
  and leave the annotations as a list of encountered sources without a position in
  the text, which is a weaker claim than "the answer cited this". The recorded
  endpoint version assumes the default; changing it is a new surface.
- **The annotation `title` is the citation label**, not a page title: the text
  citation is a numbered marker, so the title is `"1"`, `"2"` (nullable). The
  adapter passes it through in `Result.Citations` and it survives inside the saved
  raw response; the `citations` table stores the URL, so whether a citation is
  yours is decided from the URL.
- **A search that cites nothing is an observed zero.** `annotations: []` next to a
  `web_search_call` item means the provider reported citation metadata and the
  answer attributed none of its sources. A set of annotations with no span
  (`0..0`) is the same observation: `observed`, zero attributed citations. Absent
  annotation metadata *and* no search item is `unknown`.
- **Only `completed` is a success.** `incomplete` takes its `error_code` from
  `incomplete_details.reason` (`max_output_tokens`, `max_prompt_tokens`,
  `max_time_limit`, `content_filter`), `in_progress` is not a final response, and
  an `error` object or a `refusal` content block is recorded with its own code
  (the refusal check runs before the empty-answer check, so a declined answer is
  not reported as an empty one). None of them counts as a zero-citation answer: a
  cut-off or blocked answer is excluded from every denominator.
- **Error bodies have no published schema.** The adapter accepts both the nested
  shape (`{"error": {"code", "message"}}`) and the flat one
  (`{"code": ..., "error": "message"}`), and falls back to `http_<status>` with
  the start of the body when neither is recognised. Credentials are redacted from
  the message and from the stored raw JSON as everywhere else.
- **The answer text is stored verbatim**, inline-citation markup and all. The
  `[[N]](url)` links are part of what the model wrote, and rewriting them would
  change `answer_sha256` and make `aicm verify` report an edited answer.
- **Live API status: observed once** (2026-11-28, `grok-4.7`). The call returned a
  completed answer, 16 `web_search_call` items and the usage totals reported by
  the API; its annotations were unpositioned, as described above. That is one
  observation, not a supported-model list.

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
- **A provider-reported cost is not read either.** xAI returns
  `usage.cost_in_nano_usd` and `usage.cost_in_usd_ticks`; the adapter does not
  map them, because the tool has no cost feature (D11) and no column to hold
  them.
- A run is bounded by its plan (questions x repeats x providers), which `--limit`
  and `--dry-run` let you check before anything is sent.
- A retried request can be charged twice by the provider; the run output counts
  the retries it made.

## Adding a provider

1. Implement `providers.Provider` in a new package under `internal/providers/`.
2. Map the provider's citation metadata to `Result.Citations` and set
   `Result.Observability` — never infer a citation from the answer text.
3. Add the provider to `config.ProviderNames()` (append: the order is what an
   operator sees), the `[providers.*]` block, and `Save()` so `aicm init` writes it.
4. Pick an `EndpointVersion` that cannot collide with another provider's (OpenAI
   and xAI both speak a Responses API; `+xai` is what keeps them apart).
5. Wire it into `buildProviders` (`internal/cli/run.go`) and the endpoint list in
   `aicm version` (`internal/cli/init.go`).
6. Add response fixtures and an `httptest` test for cited / not cited / error /
   empty / no-metadata shapes, plus any candidates-versus-citations shape the
   provider has, and one CLI end-to-end run that checks the recorded `provider`,
   `api_endpoint_version` and `cited_own`.
7. Add the package to `internal/cli/forbidden_test.go` (it must use `net/http`) and
   update `docs/providers.md`, `docs/architecture.md`, `docs/cli-reference.md`,
   `examples/config.toml`, `.env.example`, `README.md` and `README.ja.md`.
8. Add a decision record entry. Do **not** add the provider to
   `providers.LiveObservation` unless it has actually been called once against the
   real service.
