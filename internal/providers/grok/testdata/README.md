# Grok fixtures

These are written from the published xAI documentation (Responses reference,
Web Search, Citations, Tool Usage Details, `openapi.json` on 2026-11-26), not
from a captured live response: this build has never called `api.x.ai`.

| File | Shape |
|---|---|
| `cited.json` | search item + message with positional `url_citation` annotations |
| `not-cited.json` | search item + `annotations: []` (observed, zero citations) |
| `candidates-only.json` | search item + message with **no** annotations, plus the top-level `citations` list the Citations page describes (a guard: candidates are not citations) |
| `no-citation-metadata.json` | message with no annotations and no search item (unknown), no `total_tokens` |
| `empty.json` | `output: []` (empty answer) |
| `error-auth.json` | 401 with the nested error envelope |
| `error-auth-flat.json` | 403 with the flat `code` + `error` string envelope, echoing the key so redaction is tested |
