#!/usr/bin/env bash
# Run the build-state reconstruction on the bundled local example.
# Requires Python 3.11+ (standard library only; no network access).
set -u
python3 -m buildstate.cli \
  --definition examples/build.json \
  --events examples/events.jsonl
code=$?
echo "(exit code: $code)"
exit $code
