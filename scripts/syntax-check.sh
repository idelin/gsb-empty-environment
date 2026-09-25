#!/usr/bin/env sh
set -eu

base_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

for file in \
  "$base_dir/src/image.ts" \
  "$base_dir/src/contours.ts" \
  "$base_dir/scripts/sample.ts" \
  "$base_dir/test/contours.test.ts"
do
  sh "$base_dir/scripts/run-node.sh" --check "$file"
done
