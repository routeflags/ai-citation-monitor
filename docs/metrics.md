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
| Competitor citation rate | judgeable runs whose citations include that site (the registered domain, or a subdomain of it when subdomains are included) | judgeable runs in the group |
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
- **Competitor citation rate is counted per site, not per host.** With
  `subdomains = true`, a citation is attributed to the most specific configured domain
  (`own_domain` or a competitor) that contains its host, and otherwise to the host's
  registrable domain (eTLD+1). Registering `example.com` therefore counts
  `www.example.com` and `blog.example.com` on the same row, which is the same rule
  `ClassifyDomain` uses. A configured domain deeper than its registrable domain
  (`blog.example.com`) keeps its own row and does not absorb `shop.example.com`.
  With `subdomains = false` the row is the exact host, because "exact match only" is a
  statement about the unit of comparison. A host the public suffix list cannot
  resolve (`localhost`, an IP address, `github.io` on its own) keeps its own row.
  A public suffix cannot be registered as a competitor: `aicm competitors add`, `aicm
  init --competitors` and the config loader refuse it (exit 2), since every customer
  under it would become one competitor (D23, D26). Rows already in the database are
  not deleted; `aicm competitors list` marks them, and a report keeps applying them
  until the config or the row is fixed.
- **Competitor citation rate does not add up to 100%.** One answer can cite
  several domains, so it is a per-domain rate, not a partition.
- **Citation share counts hosts, not sites.** `example.com` and `www.example.com` are
  two rows there even when the competitor rate counts them as one site. The two tables
  answer different questions ("how many answers cited this site" versus "where did the
  citations point"), so they have different denominators: judgeable runs and citations.
- **Citation share counts malformed citations as one each.** A URL without a host
  still came back from the provider, so dropping it would make the shares disagree
  with the citation counts. It appears as `(no host)` in the report. The denominator
  is the group's own citation total, and the report prints that scope.
- **Coverage's denominator is the questions that have a judgeable answer**, not the
  number of questions in the set and not the number of runs. Questions with only
  failed or `unknown` answers are listed separately, and questions with no run at
  all are listed as "not measured".

## Brand terms status

A question set may declare `brand_terms_status: proposed | confirmed`. The value is
recorded on `question_sets` at import, and the report prints it in the conditions.
It is **provenance, not a measurement rule**: it changes no numerator and no
denominator.

- `proposed` — the term list is still a guess. The report says so next to the brand
  mention rate, because that rate depends on how the terms were chosen.
- `confirmed` — the list is settled. The report says so and adds no caution.
- not declared — the status was not recorded for the set behind the run: it was
  imported before the status was recorded, or the run names no question set (a run
  made after importing only a CSV list). The report says `not declared`, never
  `confirmed`.

The record is append-only. A set that was recorded without a status keeps that
value even when the same file is imported again: the SHA is the whole file, so the
same bytes are the same set, and `questions import` says what the record holds
(with a warning when the file declares something else). Change the question set —
and its `set_version` — to record a new status.

When a period mixes sets with different statuses, every status is listed
(`proposed` first) and a single `proposed` set is enough to keep the caution.

## Formatting rules

- A rate is written as the percentage with its denominator: `40.0% (n=2/5)`.
- **The percentage is rounded from the ratio, not from a float**: one decimal, and an
  exact halfway value goes to the even tenth. `1/16` prints `6.2%`, `23/80` prints
  `28.8%`, `1/2000` prints `0.0%` — the same tenth on every machine and Go version
  (`proofs/Rate.lean`, `tenths_spec`). A column of shares is therefore not required to
  add up to exactly `100.0%`; it stays within half a tenth per row (`share_column_bound`).
  `go test ./...` checks every numerator for every denominator up to 3000 against a
  second integer implementation, with exact rational arithmetic pinning every halfway
  value (`docs/decisions/20261128-rounding-exhaustive-test.md`, D25).
- **A difference between periods is in percentage points, and it is the difference
  between the two rates the table prints**: `1/5 -> 6/32` prints `20.0% -> 18.8%`, so the
  delta is `-1.2pt`. Subtracting the rows gives the delta column.
- `n = 0` is written as `— (not measured)` (Japanese: `—（未測定）`) and the JSON
  `value` is `null`. It is never `0.0%`.
- A difference between periods is in **percentage points** (`+12.5pt`), never a
  relative change.

## Failure and unknown are not zero

| Situation | Where it goes |
|---|---|
| The call failed (provider error, transport error) | `status = error`, counted in the report, excluded from every denominator |
| The call was cancelled (Ctrl+C) | `status = cancelled`, counted, excluded |
| The citation metadata was not observed (the call failed, or the answer arrived without it) | `citation_observability = unknown`, counted, excluded from citation rates (but included in the brand mention rate when the answer exists) |
| The answer text is empty | treated as a failed fetch (`error_code = empty_answer`), not as "no citation" |

### Status and observability are separate dimensions

The status columns and the unknown column are **not** a partition of the same runs:

- `runs_total = success + error + cancelled`, and each run is in exactly one of those.
- `runs_unknown` counts the runs whose citation metadata was not observed, **whatever
  their status**. A failed call usually has no observed citation metadata (no answer
  arrived), so it appears in `error` *and* `unknown`; a failed response that did carry
  citation metadata appears only in `error`.
- The citation-rate denominator is `success` **and** `observed`; the brand-mention
  denominator is every `success`.

A rate is therefore never a statement about unknown runs: unknown is an exclusion
reason, not a third status. If you want "answers that arrived but could not be judged",
read `success` together with `citation_observability`.

## Groups that mix measurement conditions

A group is `provider x model x stage`, so a period can put records of different
measurement conditions in one group: another question set, another API endpoint
version, or another `tool_version` (which carries the normaliser version). The rate is
still computed over the group's runs — it describes what was observed — but the group
is not one measurement, and the report says so.

`mixed_conditions` in `report --json` lists the fields that differ, for example
`tool_version: 0.1.0+norm3, 0.2.0+norm3`. The Markdown report prints the same line under
**Conditions**, keyed by the group label. An empty recorded value counts as a value of
its own and is printed as `—`: a run made without a question set was not measured with
the set the other runs name.

This is the same identity `aicm compare` uses to refuse a delta between periods
(`api_endpoint_version`, `question_set_sha256`, `tool_version`).

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
