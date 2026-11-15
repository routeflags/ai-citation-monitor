# Metric definitions

Every rate in this tool is defined here, in `internal/analysis`, and nowhere else.
Callers never build a denominator themselves — a denominator that drifts makes the
number meaningless.

## Vocabulary

- **run** — one attempted call: one question, one provider, one model, one repeat.
  Success, failure and cancellation are all runs.
- **group** — `provider x model x stage`. Rates never cross a group.
- **judgeable run** — `status = success` **and** `citation_observability = observed`.
  Only these can be divided into a citation rate.
- **n** — the denominator. Always printed next to the rate.

## The seven metrics

| Metric | Numerator | Denominator |
|---|---|---|
| GEO citation rate | judgeable runs where `cited_own = 1` | judgeable runs in the group |
| Brand mention rate | successful runs where `brand_mentioned = 1` | successful runs in the group |
| AEO question citation rate | judgeable runs of that question where `cited_own = 1` | judgeable runs of that question |
| AEO question coverage | questions with at least one judgeable run that cited you | questions with at least one judgeable run |
| Competitor citation rate | judgeable runs whose citations include that domain | judgeable runs in the group |
| Citation share | citations of that domain | all citations of the group |
| Trend | per bucket: the rates above | the same denominators, per bucket |

Details that matter:

- **Brand mention rate divides by successful runs**, not by judgeable ones: the
  mention is a property of the answer text, which a successful run always has.
- **How a brand term is matched.** Both sides are normalised (NFKC, whitespace
  folded, lower case). A plain ASCII term must sit on word boundaries, so the
  brand `Go` matches `Go 1.27.4` but not `Google`. A term with any non-ASCII
  character is matched as a substring, because those scripts have no boundary to
  anchor on. A term that appears **only inside a longer word** is recorded as an
  ambiguous mention and left out of the rate: unknown, not "not mentioned". The
  report prints that count separately.
- **Competitor citation rate does not add up to 100%.** One answer can cite
  several domains, so it is a per-domain rate, not a partition.
- **Citation share counts malformed citations as one each.** A URL without a host
  still came back from the provider, so dropping it would make the shares disagree
  with the citation counts. It appears as `(no host)` in the report. The denominator
  is the group's own citation total, and the report prints that scope.
- **Coverage's denominator is the questions that have a judgeable answer**, not the
  number of questions in the set and not the number of runs. Questions with only
  failed or `unknown` answers are listed separately, and questions with no run at
  all are listed as "not measured".

## Formatting rules

- A rate is written as the percentage with its denominator: `40.0% (n=2/5)`.
- `n = 0` is written as `— (not measured)` (Japanese: `—（未測定）`) and the JSON
  `value` is `null`. It is never `0.0%`.
- A difference between periods is in **percentage points** (`+12.5pt`), never a
  relative change.

## Failure and unknown are not zero

| Situation | Where it goes |
|---|---|
| The call failed (provider error, transport error) | `status = error`, counted in the report, excluded from every denominator |
| The call was cancelled (Ctrl+C) | `status = cancelled`, counted, excluded |
| The answer arrived but the provider returned no citation metadata | `citation_observability = unknown`, counted, excluded from citation rates (but included in the brand mention rate) |
| The answer text is empty | treated as a failed fetch (`error_code = empty_answer`), not as "no citation" |

## Comparing periods

`aicm compare` aligns two periods on the group key and refuses to subtract when the
two sides are not the same measurement:

- a different `api_endpoint_version`, `question_set_sha256` or `tool_version` →
  "conditions differ, so no delta" (the differing fields are printed)
- one side has no runs → "not measured (one side has no runs)"
- different `n` → the delta is printed with a note saying so

`--comparable-only` hides the rows that cannot be compared.

Both `compare` and `trends` use the same three reason codes and the same wording:
`unmeasured` / `earlier_unmeasured`, `conditions_differ` (compare only), `n_differs`
and `no_history` (trends only).

## Trends

- `daily`, `weekly` (ISO week), `monthly`; buckets are **JST** because the calendar
  the reader uses is JST.
- The first bucket has nothing before it, so its delta is `— (no history)`.
- When the previous bucket has no measurement, the delta is `— (not measured)` and
  the note says why.
- When the previous bucket has a different denominator, the delta is printed with
  `reason = n_differs` and `detail = "<prev> vs <now>"`, using the same vocabulary
  and wording as `compare`. The two buckets are not the same sample size, so the
  difference in percentage points is read next to that note.

## What the tool deliberately cannot tell you

- Whether an answer is *correct*: that needs a ground truth and human review.
- The citation rate of the consumer ChatGPT / Gemini UI, or of every user.
- Anything causal: attribution needs the other half of the experiment.
