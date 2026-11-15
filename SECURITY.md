# Security policy

## Reporting a vulnerability

Please **do not open a public issue** for a security problem. Use GitHub's private
reporting instead: *Security* tab → *Report a vulnerability* (or
https://github.com/routeflags/ai-citation-monitor/security/advisories/new).

We will confirm receipt, tell you whether it is in scope, and coordinate a fix
and a disclosure date with you. Please do not publish details before that.

## What this project considers a security problem

- A credential (`OPENAI_API_KEY`, `GEMINI_API_KEY`, `ANTHROPIC_API_KEY`, or any
  value named by `api_key_env`) reaching a database row, a log line, an exported
  file, or an error message.
- A stored record that can be made to look like a successful measurement when the
  call failed, or a citation that can be forged without provider metadata.
- A number that can be made to lie: a rate without its denominator, a total across
  provider/model/stage, or an unmeasured period reported as `0.0%`.
- Memory exhaustion or a crash caused by a malicious provider response, a huge
  answer, a hostile URL, or a malformed question set.
- Path traversal through a configuration value (`db`, `report_dir`, a fixture
  path), or a file written outside the configured directory.

## Out of scope

- The measurement being *wrong about the provider*: this tool records a sample.
  A citation the model did not make, or did not make for you, is a finding about
  the AI service, not a vulnerability here.
- Cost or billing questions: the tool does not price a call (see decision D11).
- Anything that requires a valid credential you do not own, or that only affects
  a modified build.

## What we do in return

- Supported version: the latest release. Fixes land on `main` and are released
  as a tag; there is no backport branch in Phase 1.
- If the problem is a leaked credential, rotate the key yourself first: the tool
  never writes it, so the leak is in a log or a shell history, not in the database.

## Handling secrets when testing

- Never put a real key in a test, a fixture, an issue, or a screenshot. Use a
  dummy value; the adapters only need *a* header value to be exercised.
- `aicm` reads keys from the environment at call time. The configuration stores
  the variable name only, and `aicm init` refuses to write a value that looks like
  a credential.
