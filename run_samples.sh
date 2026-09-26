#!/bin/sh
# Run every example scenario through the CLI.
set -e
for f in examples/*.json; do
  echo "=== $f ==="
  python3 cli.py "$f"
  echo
done
