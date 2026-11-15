# CLI reference

```
aicm [--config PATH] [--db PATH] [--json] [--quiet] [--verbose] COMMAND
```

Global flags:

| Flag | Meaning |
|---|---|
| `--config` | path to the TOML configuration (default `aicm.toml`) |
| `--db` | database path, overriding the config |
| `--json` | stdout carries JSON only; logs and progress go to stderr |
| `--quiet` | no progress output |
| `--verbose` | request-level detail on stderr |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | validation failed, partial failure, or an integrity problem |
| 2 | configuration / usage problem |
| 3 | credential missing (nothing was sent) |
| 4 | API / transport failure |

With `--json`, a failure is `{"error":{"code":2,"reason":"config_incomplete","message":"..."}}`.
Reasons are stable strings: `config_incomplete`, `config_invalid`, `auth_missing`,
`model_missing`, `questions_missing`,
`api_failure`, `partial_failure`, `evidence_inconsistent`, `question_set_invalid`,
`period_invalid`, `usage`.

## init

```
aicm init [--non-interactive] [--site-id ID] [--site-name NAME] [--own-domain HOST]
          [--market ja-JP] [--language ja] [--brand-terms A,B] [--competitors D1,D2]
          [--db-path PATH] [--report-dir DIR] [--out FILE] [--force] [--skip-db]
```

Writes the configuration and creates the project row. Interactive when stdin is a
terminal and `--non-interactive` is absent. A configuration file is never
overwritten without `--force`. Only the flags you actually pass override an
existing configuration.

## questions

```
aicm questions add --id Q01 --text "..." --category definition [--language ja] [--version V]
aicm questions list
aicm questions import --file questions.yml|questions.csv [--stage smoke] [--check] [--dry-run] [--language ja]
```

- Categories: `definition`, `comparison`, `regulation`, `effect`, `safety`,
  `usage`, `purchase`, `list`, `identification`, `dosage`.
- A question version is derived from the question text unless you pass `--version`.
  Editing a text therefore starts a new version and never silently reuses an old
  measurement.
- `import --check` validates and prints the summary without writing anything.
- CSV requires the header `id,text,category`; `language` and `version` are optional
  columns.

## run

```
aicm run [--provider openai|gemini|openai,gemini] [--source ...] [--model M]
         [--repeat N] [--stage smoke|initial|regular] [--concurrency N] [--rpm N]
         [--limit N] [--session S] [--dry-run] [--yes]
```

- One call = one question x one repeat x one provider/model. `--repeat` is 1..5.
- A call already recorded with the same conditions and session is skipped, so a
  resumed run never pays twice. `--session` defaults to today's JST date.
- `--dry-run` prints the questions, the planned call count and the checks, and
  sends nothing. It needs no credential: a missing key
  is reported as `auth: <provider> <ENV>=missing` (a real run would exit 3), but
  the plan still prints and the exit code stays 0.
- Without `--yes` the command asks for confirmation when stdin is a terminal, and
  refuses on a non-interactive stdin rather than spending silently.
- Human output:

```
questions: 5
target:    openai / <model>
condition: stage=smoke repeat=1 session=2026-11-15
calls:     5 planned, 0 already recorded (skipped)
run: calls=5 success=5 failed=0 cancelled=0 skipped=0 not_run=0
```

- `--json` output: `{"calls":5,"success":5,"failed":0,"cancelled":0,"skipped":0,"not_run":0,"exit_kind":"ok"}`.

## report

```
aicm report [--period 30d|A:B|YYYY-MM-DD|all] [--provider P] [--model M] [--stage S]
            [--compare previous|A:B] [--out DIR] [--stdout]
```

The file name carries the period and every group condition, so reports for
different conditions never overwrite each other:

```
report-2026-11-12_2026-11-12-openai-gpt-6-luna-smoke-qs-2eee65141222.md
```

The report language is `[report] language` (`en` default, `ja`). Rates always
carry `n`; `n = 0` prints `— (not measured)`. Without `--stdout` the file is written
to `report_dir` (default `reports/`).

## compare

```
aicm compare [--period 30d] [--baseline previous|A:B] [--provider P] [--model M]
             [--stage S] [--comparable-only]
```

One row per group: citation rate and brand mention rate in both periods, the
difference in percentage points, and a note when the numbers are not comparable
(different conditions, different `n`, or one side unmeasured).

## trends

```
aicm trends [--interval daily|weekly|monthly] [--period 90d] [--provider P] [--model M] [--stage S]
```

One table per group. Buckets are JST. The first bucket says `— (no history)`.

## competitors

```
aicm competitors add <domain> [--label NAME]
aicm competitors list
aicm competitors remove <domain>
```

Registered competitors are stored in the database and merged with the ones in the
configuration. They are applied at report time, so adding one does not invalidate
existing measurements.

## export

```
aicm export [--format csv|json|jsonl] [--out FILE] [--period all|30d|A:B]
            [--provider P] [--model M] [--stage S]
```

- `csv` — one flat row per run (spreadsheet friendly).
- `jsonl` — one JSON object per run, with its citations nested.
- `json` — one document with metadata and the rows.

Exports are derived: the database stays the source of truth and every export can
be regenerated.

## verify

```
aicm verify [--json]
```

Checks the database against itself and exits 1 when anything is found:

| Kind | Meaning |
|---|---|
| `run_without_response` | the run exists but its answer and raw response are gone |
| `response_without_run`, `citation_without_response`, `mention_without_run`, `usage_without_run` | a child row points at a missing parent |
| `citation_run_mismatch` | a citation belongs to a different run than its response |
| `citation_count_mismatch` | `runs.citation_count` disagrees with the citation rows |
| `answer_sha256_mismatch` | the stored text was edited after it was recorded |
| `success_with_empty_answer` | a successful run has no answer text |
| `success_without_completed_at` | a successful run has no completion time |
| `question_version_missing` | the run refers to a question version that is not stored |

## version

```
aicm version [--json]
```

Prints the build, the normaliser version, the supported providers and their
endpoint versions, and states that the live API is not verified.
