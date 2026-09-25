"""Counterexample shrinking: minimal inputs and minimal chunk partitions."""
from __future__ import annotations

from .streaming import (ChunkCounterexample, run_streaming, compare_outputs)


def _fails(impl_factory, x, boundaries, tol) -> bool:
    ref = run_streaming(impl_factory(), x, [])
    out = run_streaming(impl_factory(), x, boundaries)
    diff, _ = compare_outputs(ref, out, tol)
    return diff > tol


def shrink_chunk_counterexample(impl_factory, cex: ChunkCounterexample) -> ChunkCounterexample:
    """Greedy 1-minimal shrinking.

    Alternates two moves until fixpoint:
      * truncate the input to the shortest still-failing prefix;
      * ddmin-style removal of chunk boundaries (remove any single
        boundary while the failure persists).
      * minimise each boundary position (move it as far left as the
        failure allows).
    The result is 1-minimal: removing one more boundary or one more
    trailing sample makes the failure disappear.
    """
    x = list(cex.input)
    b = sorted(cex.boundaries)
    tol = cex.tolerance
    changed = True
    while changed:
        changed = False
        lo, hi = 1, len(x)
        while lo < hi:
            mid = (lo + hi) // 2
            if _fails(impl_factory, x[:mid], [p for p in b if p < mid], tol):
                hi = mid
            else:
                lo = mid + 1
        clipped = [p for p in b if p < lo]
        if lo < len(x) and _fails(impl_factory, x[:lo], clipped, tol):
            x = x[:lo]
            b = clipped
            changed = True
        i = 0
        while i < len(b):
            cand = b[:i] + b[i + 1:]
            if _fails(impl_factory, x, cand, tol):
                b = cand
                changed = True
            else:
                i += 1
        for i in range(len(b)):
            low = (b[i - 1] + 1) if i > 0 else 1
            lo, hi = low, b[i]
            while lo < hi:
                mid = (lo + hi) // 2
                if _fails(impl_factory, x, b[:i] + [mid] + b[i + 1:], tol):
                    hi = mid
                else:
                    lo = mid + 1
            if lo < b[i] and _fails(impl_factory, x, b[:i] + [lo] + b[i + 1:], tol):
                b = b[:i] + [lo] + b[i + 1:]
                changed = True
    ref = run_streaming(impl_factory(), x, [])
    out = run_streaming(impl_factory(), x, b)
    diff, idx = compare_outputs(ref, out, tol)
    return ChunkCounterexample(x, b, diff, idx, tol)
