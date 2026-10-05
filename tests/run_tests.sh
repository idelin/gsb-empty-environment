#!/bin/sh
# Known-answer tests + randomised cross-check against the brute forcer.
set -e
cd "$(dirname "$0")/.."
make

expect() { # $1=input $2=expected_S
  got=$(printf '%b' "$1" | ./atlas_solver | head -1)
  if [ "$got" != "$2" ]; then
    echo "FAIL: input [$1] expected S=$2 got S=$got" >&2; exit 1
  fi
  echo "ok: S=$got"
}

expect '1 1 shared\n3 2\n' 5
expect '2 1 shared\n2 2\n2 2\n' 7
expect '2 1 exclusive\n2 2\n2 2\n' 8
expect '5 0 shared\n2 2\n2 2\n2 2\n2 2\n2 2\n' 6
expect '2 0 shared\n4 1\n1 4\n' 4
expect '2 0 shared\n5 3\n5 3\n' 6
expect '3 2 exclusive\n1 1\n1 1\n1 1\n' 10

python3 tests/random_crosscheck.py "${1:-1}" "${2:-100}"
echo "ALL TESTS PASSED"
