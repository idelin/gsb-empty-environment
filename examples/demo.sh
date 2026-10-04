#!/bin/sh
# Self-contained demo: builds a sandbox tree, then audits actual accesses
# against examples/manifest.json.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(mktemp -d)

mkdir -p "$ROOT/app/config" "$ROOT/app/data" "$ROOT/app/logs" "$ROOT/vault"
echo '{}'            > "$ROOT/app/config/settings.json"
echo 'dark'          > "$ROOT/app/config/theme.txt"
echo 'a,b'           > "$ROOT/app/data/input.csv"
echo 'top-secret'    > "$ROOT/vault/secret.txt"
# Symlink staying inside the declared dir (authorized):
ln -s theme.txt "$ROOT/app/config/theme-link"
# Symlink escaping the declared dir (NOT authorized by the dir declaration):
ln -s ../../vault/secret.txt "$ROOT/app/config/secret-link"

CLI="$HERE/../dist/src/cli.js"
CODE=0
node "$CLI" check \
  --manifest "$HERE/manifest.json" \
  --workload "$HERE/workload.json" \
  --base "$ROOT" \
  --report "$ROOT/report.json" \
  --supplement-out "$ROOT/supplement.json" || CODE=$?

echo "--- report:      $ROOT/report.json"
echo "--- supplement:"
cat "$ROOT/supplement.json"
echo "--- sandbox kept at $ROOT"
exit $CODE
