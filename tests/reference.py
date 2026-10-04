"""Independent reference implementation used to cross-check tinyreduce.

Deliberately written with plain nested lists, recursion, itertools and
builtins only -- it shares no indexing/stride/tree code with the library.
"""
import math
from itertools import product


class RefEmptyError(Exception):
    """Reference-side signal for mean/min/max over an empty slice."""


def get(nested, idx):
    for i in idx:
        nested = nested[i]
    return nested


def indices(shape):
    return product(*(range(s) for s in shape))


def out_key(idx, reduced):
    return tuple(v for d, v in enumerate(idx) if d not in reduced)


def ref_sum(vals):
    if not vals:
        return 0.0
    try:
        return math.fsum(vals)
    except ValueError:  # fsum rejects +inf/-inf mixes; fall back to IEEE
        s = 0.0
        for v in vals:
            s += v
        return s


def forward_and_grad(nested, shape, axes, op, masked=frozenset()):
    """Returns (out, grad, nondiff):
    out     : dict squeezed-output-index -> value
    grad    : dict input-index -> d(out)/d(input) with upstream 1
    nondiff : set of input indices where the grad is only a subgradient
    Raises RefEmptyError if any slice is empty and op has no identity.
    """
    reduced = set(axes)
    out_shape = tuple(s for d, s in enumerate(shape) if d not in reduced)
    groups = {}
    for idx in indices(shape):
        if idx in masked:
            continue
        groups.setdefault(out_key(idx, reduced), []).append(
            (idx, get(nested, idx)))

    out, grad, nondiff = {}, {}, set()
    for key in indices(out_shape):
        members = groups.get(key, [])
        if not members:
            if op != "sum":
                raise RefEmptyError(key)
            out[key] = 0.0
            continue
        vals = [v for _, v in members]
        if op == "sum":
            out[key] = ref_sum(vals)
            for idx, _ in members:
                grad[idx] = 1.0
        elif op == "mean":
            out[key] = ref_sum(vals) / len(vals)
            for idx, _ in members:
                grad[idx] = 1.0 / len(vals)
        else:
            extreme = min(vals) if op == "min" else max(vals)
            out[key] = extreme
            tied = [idx for idx, v in members
                    if v == extreme or (math.isnan(v) and math.isnan(extreme))]
            for idx in tied:
                grad[idx] = 1.0 / len(tied)
            if len(tied) > 1 or math.isnan(extreme):
                nondiff.update(tied)
    for idx in indices(shape):
        grad.setdefault(idx, 0.0)
    return out, grad, nondiff


def close(a, b, tol=1e-9):
    if math.isnan(a) and math.isnan(b):
        return True
    if a == b:  # covers +-inf
        return True
    return math.isclose(a, b, rel_tol=tol, abs_tol=tol)
