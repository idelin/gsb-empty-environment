#!/bin/sh
# Deterministic regression tests for mask_bool.
set -e
cd "$(dirname "$0")/.."
BIN=./mask_bool
fail=0

check() { # name input expected_optimal
    name=$1; input=$2; want=$3
    got=$(printf '%s\n' "$input" | $BIN | awk '/^OPTIMAL/{print $2}')
    if [ "$got" = "$want" ]; then
        echo "ok   $name (K=$got)"
    else
        echo "FAIL $name: want K=$want got K=$got"
        fail=1
    fi
    # every non-empty run must pass its own reconstruction verification
    v=$(printf '%s\n' "$input" | $BIN | grep -c 'VERIFY.*union=ok')
    [ "$v" = 1 ] || { echo "FAIL $name: verification line bad"; fail=1; }
}

check "ring 4x4 minus center"   '4 4
1
0 0 3 3
1
1 1 2 2' 4

check "L shape"                 '3 3
1
0 0 2 2
1
0 1 1 2' 2

check "checkerboard 3x3"        '3 3
5
0 0 0 0
0 2 0 2
1 1 1 1
2 0 2 0
2 2 2 2
0' 5

check "keep minus itself"       '2 2
1
0 0 1 1
1
0 0 1 1' 0

check "full 12x12"              '12 12
1
0 0 11 11
0' 1

check "two disjoint keeps"      '4 4
2
0 0 1 1
2 2 3 3
0' 2

check "overlap keeps merge"     '4 4
2
0 0 1 1
1 1 2 2
0' 3

check "stripes 12x12"           "$(printf '12 12\n6\n0 0 0 11\n2 0 2 11\n4 0 4 11\n6 0 6 11\n8 0 8 11\n10 0 10 11\n0')" 6

check "plus sign 5x5"           '5 5
2
0 2 4 2
2 0 2 4
0' 3

[ $fail = 0 ] && echo "ALL TESTS PASSED" || exit 1
