#!/bin/sh
# Runs every case in tests/cases.
#   valid_*.txt    -> exit code 0 expected
#   invalid_*.txt  -> exit code 1 expected
# Optional directives in a case file:
#   # expect: <substr>         output must contain <substr>
#   # expect-absent: <substr>  output must NOT contain <substr>
cd "$(dirname "$0")/.." || exit 1
BIN=./tricheck
pass=0
fail=0
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

for f in tests/cases/*.txt; do
    base=$(basename "$f")
    case "$base" in
        valid_*)   want=0 ;;
        invalid_*) want=1 ;;
        *)         continue ;;
    esac
    out=$("$BIN" "$f" 2>&1)
    got=$?
    ok=1
    if [ "$got" -ne "$want" ]; then
        ok=0
        echo "    want exit $want, got $got"
    fi
    grep '^# expect: ' "$f" | sed 's/^# expect: //' > "$tmp"
    while IFS= read -r sub; do
        [ -z "$sub" ] && continue
        if ! printf '%s\n' "$out" | grep -qF -- "$sub"; then
            ok=0
            echo "    missing expected substring: $sub"
        fi
    done < "$tmp"
    grep '^# expect-absent: ' "$f" | sed 's/^# expect-absent: //' > "$tmp"
    while IFS= read -r sub; do
        [ -z "$sub" ] && continue
        if printf '%s\n' "$out" | grep -qF -- "$sub"; then
            ok=0
            echo "    forbidden substring present: $sub"
        fi
    done < "$tmp"
    if [ "$ok" -eq 1 ]; then
        pass=$((pass + 1))
        echo "PASS $base"
    else
        fail=$((fail + 1))
        echo "FAIL $base"
        printf '%s\n' "$out" | sed 's/^/    /'
    fi
done

echo "----------------------------------------"
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
