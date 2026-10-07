#!/bin/sh
# Runs all tests: valid_* must exit 0 printing VALID, invalid_* must exit 1
# printing INVALID plus evidence. Also greps for expected evidence keywords.
fail=0
check() { # file expected_first_line keyword...
    f=$1; want=$2; shift 2
    out=$(./tri_checker "$f"); code=$?
    first=$(echo "$out" | head -1)
    if [ "$first" != "$want" ]; then
        echo "FAIL $f: expected $want, got $first"; fail=1; continue
    fi
    for kw in "$@"; do
        echo "$out" | grep -q "$kw" || { echo "FAIL $f: missing evidence '$kw'"; fail=1; }
    done
    [ $fail -eq 0 ] || true
    echo "ok   $f"
}
check tests/valid_fan.txt VALID
check tests/valid_tjunction.txt VALID
check tests/valid_collinear_polygon.txt VALID
check tests/valid_rational.txt VALID
check tests/valid_steiner.txt VALID
# equal area (sum == polygon area) but overlap + hole: must NOT be caught by area check
out=$(./tri_checker tests/invalid_equal_area_overlap_hole.txt)
echo "$out" | grep -q "^INVALID" || { echo "FAIL equal_area_overlap_hole verdict"; fail=1; }
echo "$out" | grep -q "unpaired interior edge" || { echo "FAIL equal_area_overlap_hole: no pairing evidence"; fail=1; }
echo "$out" | grep -q "area mismatch" && { echo "FAIL equal_area_overlap_hole: areas should be EQUAL"; fail=1; }
echo "ok   tests/invalid_equal_area_overlap_hole.txt"
# silent version: no crossings, no area mismatch - only edge pairing catches it
out=$(./tri_checker tests/invalid_equal_area_silent.txt)
echo "$out" | grep -q "^INVALID" || { echo "FAIL equal_area_silent verdict"; fail=1; }
echo "$out" | grep -q "unpaired interior edge" || { echo "FAIL equal_area_silent: no pairing evidence"; fail=1; }
echo "$out" | grep -q "crossing" && { echo "FAIL equal_area_silent: unexpected crossing"; fail=1; }
echo "$out" | grep -q "area mismatch" && { echo "FAIL equal_area_silent: areas should be EQUAL"; fail=1; }
echo "ok   tests/invalid_equal_area_silent.txt"
check tests/invalid_crossing.txt INVALID "crossing edges"
check tests/invalid_boundary_gap.txt INVALID "uncovered boundary segment"
check tests/invalid_outside.txt INVALID "crosses polygon boundary"
check tests/invalid_folded.txt INVALID "expected exactly 1" "area mismatch"
check tests/invalid_degenerate.txt INVALID "degenerate triangle"
check tests/invalid_selfintersect_polygon.txt INVALID "self-intersection"
if [ $fail -eq 0 ]; then echo "ALL TESTS PASSED"; else echo "SOME TESTS FAILED"; exit 1; fi
