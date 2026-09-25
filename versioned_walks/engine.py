"""Exact or modular walk counting via lazily built DP blocks.

For a snapshot with adjacency matrix A (entries count parallel edges),
the engine caches block powers P[i] = A^(2^i) and block sums
S[i] = A^0 + A^1 + ... + A^(2^i - 1).  Blocks are built lazily per
(version, modulus) pair; a graph mutation produces a new version key,
which incrementally invalidates the stale blocks while old versions
keep their own caches intact.

Python ints give arbitrary precision; an optional modulus reduces every
intermediate value.  Modular counts must never drive k-th selection.
"""

from __future__ import annotations

from typing import List, Optional


def _mat_mul(A, B, mod):
    n = len(A)
    p = len(B[0]) if B else 0
    C = [[0] * p for _ in range(n)]
    for i in range(n):
        Ai = A[i]
        Ci = C[i]
        for k in range(n):
            a = Ai[k]
            if not a:
                continue
            Bk = B[k]
            if mod:
                for j in range(p):
                    Ci[j] = (Ci[j] + a * Bk[j]) % mod
            else:
                for j in range(p):
                    Ci[j] += a * Bk[j]
    return C


def _mat_add(A, B, mod):
    if mod:
        return [[(a + b) % mod for a, b in zip(ra, rb)] for ra, rb in zip(A, B)]
    return [[a + b for a, b in zip(ra, rb)] for ra, rb in zip(A, B)]


def _mat_vec(A, x, mod):
    out = []
    for row in A:
        acc = 0
        for a, xv in zip(row, x):
            if a and xv:
                acc += a * xv
        out.append(acc % mod if mod else acc)
    return out


def _vec_add(a, b, mod):
    if mod:
        return [(x + y) % mod for x, y in zip(a, b)]
    return [x + y for x, y in zip(a, b)]


class CountEngine:
    """Per-(version, modulus) DP blocks over one immutable snapshot."""

    def __init__(self, snapshot, modulus: Optional[int] = None):
        if modulus is not None and modulus <= 0:
            raise ValueError("modulus must be a positive integer")
        self.modulus = modulus
        self.vertices = sorted(snapshot.vertices)
        self.index = {v: i for i, v in enumerate(self.vertices)}
        n = len(self.vertices)
        adj = [[0] * n for _ in range(n)]
        for e in snapshot.edges:
            i = self.index[e.src]
            j = self.index[e.dst]
            adj[i][j] = (adj[i][j] + 1) % modulus if modulus else adj[i][j] + 1
        self.adj = adj
        ident = [[1 if i == j else 0 for j in range(n)] for i in range(n)]
        self._powers: List[list] = [adj]   # P[i] = A^(2^i)
        self._sums: List[list] = [ident]   # S[i] = sum_{L < 2^i} A^L

    def _ensure(self, i: int) -> None:
        mod = self.modulus
        while len(self._powers) <= i:
            P = self._powers[-1]
            S = self._sums[-1]
            self._sums.append(_mat_add(S, _mat_mul(P, S, mod), mod))
            self._powers.append(_mat_mul(P, P, mod))

    def prefix_sum_vec(self, m: int, x):
        """Return (sum_{L < m} A^L) @ x via binary block decomposition."""
        mod = self.modulus
        res = [0] * len(x)
        cur = list(x)
        i = 0
        while m:
            if m & 1:
                self._ensure(i)
                res = _vec_add(res, _mat_vec(self._sums[i], cur, mod), mod)
                cur = _mat_vec(self._powers[i], cur, mod)
            m >>= 1
            i += 1
        return res

    def count_range(self, source: str, target: str, lo: int, hi: int) -> int:
        """Number of source->target walks with length in [lo, hi)."""
        if hi <= lo:
            return 0
        si = self.index.get(source)
        ti = self.index.get(target)
        if si is None or ti is None:
            return 0
        n = len(self.vertices)
        x = [0] * n
        x[ti] = 1
        hi_val = self.prefix_sum_vec(hi, x)[si]
        lo_val = self.prefix_sum_vec(lo, x)[si]
        if self.modulus:
            return (hi_val - lo_val) % self.modulus
        return hi_val - lo_val
