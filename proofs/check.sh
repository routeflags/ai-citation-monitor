#!/bin/sh
# Check the report's arithmetic: the Lean proofs, then the rate vectors the model and
# the Go code must both reproduce.
#
# Run from anywhere; it enters proofs/ first. Lean is not a build dependency of the
# tool — the binary and the Go test suite never need it — so this is a separate target
# from `make check`.
#
# Install the toolchain once with elan; proofs/lean-toolchain pins the version.
set -eu
cd "$(dirname "$0")"

LEAN=${LEAN:-lean}
if ! command -v "$LEAN" >/dev/null 2>&1; then
  echo "lean not found: install elan (https://lean-lang.org/lean4) or set LEAN" >&2
  exit 1
fi

"$LEAN" Rate.lean
"$LEAN" --run Rate.lean
