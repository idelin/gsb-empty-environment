#!/bin/sh
# Reproducible test + throughput run with a workspace-local Go cache.
set -e
ROOT=$(cd "$(dirname "$0")" && pwd)
export GOCACHE="$ROOT/.gocache"
export GOPATH="$ROOT/.gopath"
GO=go
if command -v /opt/homebrew/bin/go >/dev/null 2>&1; then
  GO=/opt/homebrew/bin/go
fi
"$GO" test -count=1 ./...
"$GO" test -run XXX -bench 'BenchmarkBatch' -benchmem -count=3 ./fp
