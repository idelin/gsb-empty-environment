"""Shared test helpers: an independent dense triple-loop oracle."""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from sr_engine import get_semiring  # noqa: E402


def dense_mul(A, B, sr, m, k, n):
    """Reference C = A x B computed with a plain dense triple loop."""
    C = {}
    for i in range(m):
        for j in range(n):
            acc = sr.zero
            for kk in range(k):
                a = A.get((i, kk))
                b = B.get((kk, j))
                if a is None or b is None:
                    continue
                acc = sr.add(acc, sr.mul(a, b))
            if not sr.is_zero(acc):
                C[(i, j)] = acc
    return C


def apply_ops_dense(A, B, ops, sr):
    """Apply engine-style ops to dense dicts with the same canonicalisation."""
    for op in ops:
        if op[0] == "set":
            _, target, i, j, v = op
            v = sr.validate(v)
            M = A if target == "A" else B
            if sr.is_zero(v):
                M.pop((i, j), None)
            else:
                M[(i, j)] = v
        elif op[0] == "del":
            _, target, i, j = op
            (A if target == "A" else B).pop((i, j), None)
        else:
            raise ValueError(op)


def dense_witnesses(A, B, sr, m, k, n, i, j):
    """Oracle witness list: ascending k attaining the aggregate."""
    ks = []
    for kk in range(k):
        a = A.get((i, kk))
        b = B.get((kk, j))
        if a is not None and b is not None:
            ks.append(kk)
    if sr.name == "minplus":
        C = dense_mul(A, B, sr, m, k, n)
        best = C.get((i, j))
        if best is None:
            return []
        return [kk for kk in ks if sr.mul(A[(i, kk)], B[(kk, j)]) == best]
    return ks


def random_value(rng, sr):
    if sr.name == "boolean":
        return True
    if sr.name == "int":
        return rng.choice([-3, -2, -1, 1, 2, 3])
    return rng.randint(-4, 9)  # minplus: positive and negative distances


def random_script(rng, sr, m, k, n, steps, max_ops=4):
    """Random transaction script mixing sets and deletes on both sides."""
    txns = []
    for t in range(steps):
        ops = []
        for _ in range(rng.randint(1, max_ops)):
            target = rng.choice("AB")
            if target == "A":
                i, j = rng.randrange(m), rng.randrange(k)
            else:
                i, j = rng.randrange(k), rng.randrange(n)
            if rng.random() < 0.3:
                ops.append(["del", target, i, j])
            else:
                ops.append(["set", target, i, j, random_value(rng, sr)])
        txns.append({"txn_id": f"t{t}", "ops": ops})
    return txns


def oracle_versions(sr, m, k, n, txns):
    """Expected (A, B, C) after every version, computed densely."""
    A, B = {}, {}
    out = [({}, {}, dense_mul(A, B, sr, m, k, n))]
    for txn in txns:
        apply_ops_dense(A, B, txn["ops"], sr)
        out.append((dict(A), dict(B), dense_mul(A, B, sr, m, k, n)))
    return out
