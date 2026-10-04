#!/bin/sh
# Runs the three-replica partition scenario against the orset library.
set -e
cd "$(dirname "$0")/.."
# Some sandboxes expose a read-only default build cache.
if ! go env GOCACHE >/dev/null 2>&1 || [ ! -w "$(go env GOCACHE 2>/dev/null)" ]; then
	export GOCACHE="${TMPDIR:-/tmp}/orset-gocache"
fi
go run ./cmd/partitiontest
