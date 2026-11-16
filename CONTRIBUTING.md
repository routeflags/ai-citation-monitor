# Contributing

Thanks for taking a look. This is a measurement tool: the numbers it prints are
only worth something if the way they are produced stays honest and reproducible.
Changes are judged against that first.

## Before you start

- **Bug reports and questions**: open an issue with the template. For a wrong
  number, include the database row (or the exported JSONL line) that produced it,
  plus the command you ran.
- **Security problems**: do not open an issue. Follow [SECURITY.md](SECURITY.md).

## Setting up

```bash
git clone https://github.com/routeflags/ai-citation-monitor
cd ai-citation-monitor
go build ./...
go test ./...
```

Requirements: Go (the version in `go.mod`), no C compiler (SQLite is CGO-free).

## What a change must keep true

These are invariants, not preferences. Each one has a test; if you change the
behaviour, change the test in the same commit and say why.

1. **Rates always carry `n`.** `n = 0` prints “— (not measured)”, never `0.0%`. A
   failure or an answer without citation metadata is counted, then excluded from
   the denominator; it is never treated as “no citation”.
2. **No totals across provider, model or stage.** A rate divides inside one
   group. If you need a new grouping, add it to the group key and to the report
   header; do not sum across the existing groups.
3. **Measurements are append-only.** `runs`, `responses`, `citations`, `mentions`
   and `usage_records` are only ever inserted. Re-running a plan skips calls that
   are already recorded.
4. **Citations come from provider metadata only.** A URL that appears in the
   answer text is not a citation. Absent metadata is `unknown`, not zero.
5. **A failed call is still recorded**, with its error, and stays out of the
   denominators.
6. **No credential value is stored, logged or written to the database.** The
   configuration names environment variables; adapters redact anything that looks
   like the key from errors and from the stored raw JSON.
7. **The tool has no cost feature.** It does not price a call, and there is no
   budget stop (see D11 in `documents/decisions/`).

## Tests

```bash
gofmt -l .          # must print nothing
go build ./...
go vet ./...
go test ./...         # no network, no credential, no API key
go test -race ./...   # on Linux
```

`make check` runs the same list (plus `golangci-lint` and `govulncheck`) in one
command; `make tools` installs the pinned versions of both.

- No test may call a real provider. Adapters are tested with `httptest` and
  fixtures that copy the shape of captured responses.
- If you add a provider, add fixtures for: cited, not cited, provider error, empty
  answer, and a response without citation metadata.
- A timestamp in a record comes from the injected clock (`App.Now`), never from a
  `time.Now` call inside an adapter. A test pins a date; a stray wall-clock read
  makes it start failing on a later day instead of the day the bug landed.
- The exported surface (`export --format json|csv|jsonl`) is part of the contract;
  changing a column is a breaking change and needs a decision record entry.

## Code layout

See [docs/architecture.md](docs/architecture.md). Two rules keep it coherent:

- Provider-specific JSON never leaves `internal/providers/<name>`.
- Every rate is defined in `internal/analysis` only. Nothing else divides.

## Commits and pull requests

- Small commits, imperative subject (`fix(domain): ...`, `docs: ...`), and a body
  that says why. Say what is unverified when something is.
- One behaviour change per PR. Update the docs that state the behaviour, and the
  decision record when a product rule changes.
- Do not commit a database (`*.db`), recorded answers, cassettes or `.env`. They
  are in `.gitignore`; keep them there.
- If a change alters an invariant above, call it out in the PR description so it
  can be reviewed as a decision, not as a detail.

## Documentation language

- `README.md`, `docs/`, CLI help, error messages and code comments: **English**.
- `README.ja.md` is a translation and must be updated in the same commit.
- `documents/` (decision record, plan, the original instruction) stays in
  Japanese.
