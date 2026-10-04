#!/bin/sh
# Integration tests for the dircopy binary.
# Usage: tests/integration.sh [path-to-dircopy]
set -u

DIRCOPY=${1:-./dircopy}
FAILURES=0
WORK=$(mktemp -d /tmp/dircopy-it.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

pass() { echo "ok - $1"; }
fail() { echo "FAIL - $1"; FAILURES=$((FAILURES + 1)); }
check() { # check <description> <command...>
    desc=$1; shift
    if "$@" >/dev/null 2>&1; then pass "$desc"; else fail "$desc"; fi
}
ino() { stat -f %i "$1" 2>/dev/null || stat -c %i "$1"; }

# --- 1. Hard links inside the root are preserved as shared inodes ----------
T=$WORK/t1; mkdir -p "$T/src/sub"
echo shared > "$T/src/a.txt"
ln "$T/src/a.txt" "$T/src/b.txt"
ln "$T/src/a.txt" "$T/src/sub/c.txt"
"$DIRCOPY" "$T/src" "$T/dst" >/dev/null 2>&1
if [ "$(ino "$T/dst/a.txt")" = "$(ino "$T/dst/b.txt")" ] && \
   [ "$(ino "$T/dst/a.txt")" = "$(ino "$T/dst/sub/c.txt")" ] && \
   [ "$(cat "$T/dst/b.txt")" = shared ]; then
    pass "hard-link group shares one inode in target"
else
    fail "hard-link group shares one inode in target"
fi
check "target link count is 3" [ "$(stat -f %l "$T/dst/a.txt" 2>/dev/null || stat -c %h "$T/dst/a.txt")" = 3 ]

# --- 2. Same content, different inodes stay distinct -----------------------
T=$WORK/t2; mkdir -p "$T/src"
echo dup > "$T/src/x.txt"; echo dup > "$T/src/y.txt"
"$DIRCOPY" "$T/src" "$T/dst" >/dev/null 2>&1
if [ "$(ino "$T/dst/x.txt")" != "$(ino "$T/dst/y.txt")" ]; then
    pass "same-content distinct inodes are not merged"
else
    fail "same-content distinct inodes are not merged"
fi

# --- 3. Symlinks are copied as symlinks, never followed --------------------
T=$WORK/t3; mkdir -p "$T/src"
echo real > "$T/src/real.txt"
ln -s real.txt "$T/src/rel_link"
ln -s /nonexistent-target "$T/src/dangling"
"$DIRCOPY" "$T/src" "$T/dst" >/dev/null 2>&1
if [ -L "$T/dst/rel_link" ] && [ -L "$T/dst/dangling" ] && \
   [ "$(readlink "$T/dst/rel_link")" = real.txt ] && \
   [ "$(readlink "$T/dst/dangling")" = /nonexistent-target ]; then
    pass "symlinks recreated with original targets"
else
    fail "symlinks recreated with original targets"
fi

# --- 4. Same-inode objects outside the root are not pulled in --------------
T=$WORK/t4; mkdir -p "$T/src"
echo outside > "$T/outside.txt"
ln "$T/outside.txt" "$T/src/inside.txt"
echo plain > "$T/src/plain.txt"
"$DIRCOPY" "$T/src" "$T/dst" >/dev/null 2>&1
count=$(find "$T/dst" -type f | wc -l | tr -d ' ')
if [ "$count" = 2 ] && [ ! -e "$T/dst/outside.txt" ] && \
   [ "$(cat "$T/dst/inside.txt")" = outside ] && \
   [ "$(ino "$T/dst/inside.txt")" != "$(ino "$T/outside.txt")" ]; then
    pass "out-of-root hard-link target not incorporated"
else
    fail "out-of-root hard-link target not incorporated"
fi

# --- 5. Identity change after scan is rejected and reported ----------------
T=$WORK/t5; mkdir -p "$T/src"
echo v1 > "$T/src/victim.txt"
echo keep > "$T/src/keeper.txt"
GATE=$T/gate
DIRCOPY_TEST_GATE=$GATE "$DIRCOPY" "$T/src" "$T/dst" --report "$T/report.txt" \
    >"$T/out.log" 2>"$T/err.log" &
pid=$!
for i in $(seq 1 100); do       # wait until the scan phase has finished
    grep -q "scan complete" "$T/err.log" 2>/dev/null && break
    sleep 0.05
done
rm "$T/src/victim.txt"          # replace: same path, new inode
echo v2 > "$T/src/victim.txt"
: > "$GATE"                     # release the copy phase
wait $pid
rc=$?
if [ $rc -eq 2 ] && [ ! -e "$T/dst/victim.txt" ] && \
   [ -f "$T/dst/keeper.txt" ] && grep -q "victim.txt" "$T/report.txt" && \
   grep -qi "identity changed\|vanished" "$T/report.txt"; then
    pass "post-scan identity change rejected and reported"
else
    fail "post-scan identity change rejected and reported (rc=$rc)"
    cat "$T/err.log"
fi

# --- 6. Partial failure: rejected item cleaned up, rest copied -------------
T=$WORK/t6; mkdir -p "$T/src"
echo ok > "$T/src/good.txt"
echo secret > "$T/src/bad.txt"
chmod 000 "$T/src/bad.txt"
"$DIRCOPY" "$T/src" "$T/dst" --report "$T/report.txt" >"$T/out.log" 2>"$T/err.log"
rc=$?
chmod 644 "$T/src/bad.txt"
leftovers=$(find "$T/dst" -name '.dircopy-tmp.*' -o -name '*.dircopy-tmp.*' | wc -l | tr -d ' ')
if [ $rc -eq 2 ] && [ -f "$T/dst/good.txt" ] && [ ! -e "$T/dst/bad.txt" ] && \
   [ "$leftovers" = 0 ] && grep -q "bad.txt" "$T/report.txt"; then
    pass "partial failure cleans up and keeps report"
else
    fail "partial failure cleans up and keeps report (rc=$rc leftovers=$leftovers)"
fi

# --- 7. Unsupported types are rejected, not silently copied ----------------
T=$WORK/t7; mkdir -p "$T/src"
mkfifo "$T/src/pipe"
"$DIRCOPY" "$T/src" "$T/dst" >/dev/null 2>&1
rc=$?
if [ $rc -eq 2 ] && [ ! -e "$T/dst/pipe" ]; then
    pass "fifo rejected as unsupported type"
else
    fail "fifo rejected as unsupported type (rc=$rc)"
fi

# --- 8. Cross-device copy (only if a second device is available) -----------
SECOND_DEV=""
for cand in /Volumes/*; do
    [ -d "$cand" ] || continue
    if [ "$(stat -f %d "$cand" 2>/dev/null)" != "$(stat -f %d "$WORK")" ] && \
       [ -w "$cand" ]; then
        SECOND_DEV=$cand; break
    fi
done
if [ -n "$SECOND_DEV" ]; then
    T=$WORK/t8; mkdir -p "$T/src"
    echo dev > "$T/src/one.txt"; ln "$T/src/one.txt" "$T/src/two.txt"
    DST="$SECOND_DEV/dircopy-xdev.$$"
    "$DIRCOPY" "$T/src" "$DST" >/dev/null 2>&1
    if [ "$(ino "$DST/one.txt")" = "$(ino "$DST/two.txt")" ]; then
        pass "cross-device copy preserves sharing (dst on $SECOND_DEV)"
    else
        fail "cross-device copy preserves sharing"
    fi
    rm -rf "$DST"
else
    echo "skip - cross-device copy (no writable second device; covered by unit keying test)"
fi

echo
if [ $FAILURES -eq 0 ]; then
    echo "integration tests: all passed"
else
    echo "integration tests: $FAILURES failure(s)"
fi
exit $FAILURES
