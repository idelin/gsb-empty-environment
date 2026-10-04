"""Multi-axis reductions: sum / mean / min / max.

Semantics
---------
* axes: negative axes are normalized, duplicates are rejected, the canonical
  evaluation order is sorted(axes) -- axis permutation never changes results.
* empty slices (zero-size reduction dims, or fully masked): sum -> 0.0
  (identity); mean / min / max raise EmptyReductionError.  A slice is "empty"
  iff it contains no unmasked element.
* masks: truthy entries are *excluded* (numpy masked-array convention).
* ties in min/max: gradient is split equally among *all* tied elements of the
  slice, counted globally -- the policy is independent of chunk boundaries.
  Tied points are marked non-differentiable (subgradient, not a gradient).
* execution: the reduction index space of every output slice is processed in
  chunks and combined with a binary tree; peak scratch workspace (number of
  simultaneously live partial slots) is recorded in ReductionStats.
"""
from __future__ import annotations

import math
from itertools import product

from .tensor import Tensor
from .errors import AxisError, EmptyReductionError, NonDifferentiableError


# --------------------------------------------------------------------- axes
def normalize_axes(axis, ndim):
    """None -> all axes; negatives normalized; duplicates rejected; sorted."""
    if axis is None:
        return tuple(range(ndim))
    if isinstance(axis, int) and not isinstance(axis, bool):
        axis = (axis,)
    axes = []
    try:
        iterator = iter(axis)
    except TypeError:
        raise AxisError(f"axis must be None, an int, or an iterable of ints, "
                        f"got {axis!r}") from None
    for a in iterator:
        if not isinstance(a, int) or isinstance(a, bool):
            raise AxisError(f"axis must be an int, got {a!r}")
        orig = a
        if a < 0:
            a += ndim
        if not 0 <= a < ndim:
            raise AxisError(
                f"axis {orig} is out of bounds for tensor of dimension {ndim}")
        axes.append(a)
    if len(set(axes)) != len(axes):
        raise AxisError(f"duplicate reduction axes are not allowed: {axes}")
    return tuple(sorted(axes))


# -------------------------------------------------------------------- stats
class ReductionStats:
    """Execution record of one reduction call.

    peak_workspace: maximum number of partial-result slots alive at once
    (tree execution keeps this at O(log2(chunks)) + O(1) per slice).
    """

    __slots__ = ("op", "slices", "elements", "chunks", "combines",
                 "peak_workspace")

    def __init__(self, op):
        self.op = op
        self.slices = 0
        self.elements = 0
        self.chunks = 0
        self.combines = 0
        self.peak_workspace = 0

    def __repr__(self):
        return (f"ReductionStats(op={self.op!r}, slices={self.slices}, "
                f"elements={self.elements}, chunks={self.chunks}, "
                f"combines={self.combines}, "
                f"peak_workspace={self.peak_workspace})")


class _Arena:
    """Counts simultaneously live scratch slots and records the peak."""

    __slots__ = ("live", "peak")

    def __init__(self):
        self.live = 0
        self.peak = 0

    def alloc(self):
        self.live += 1
        if self.live > self.peak:
            self.peak = self.live

    def free(self, n=1):
        self.live -= n


# ----------------------------------------------------------------- combines
def _combine_sum(a, b):
    return (a[0] + b[0], a[1] + b[1])


def _combine_min(a, b):
    av, an = a
    bv, bn = b
    a_nan, b_nan = math.isnan(av), math.isnan(bv)
    if a_nan and b_nan:
        return (av, an + bn)
    if a_nan:
        return a
    if b_nan:
        return b
    if av < bv:
        return a
    if bv < av:
        return b
    return (av, an + bn)  # global tie: counts add across chunks


def _combine_max(a, b):
    av, an = a
    bv, bn = b
    a_nan, b_nan = math.isnan(av), math.isnan(bv)
    if a_nan and b_nan:
        return (av, an + bn)
    if a_nan:
        return a
    if b_nan:
        return b
    if av > bv:
        return a
    if bv > av:
        return b
    return (av, an + bn)  # global tie: counts add across chunks


_COMBINES = {"sum": _combine_sum, "mean": _combine_sum,
             "min": _combine_min, "max": _combine_max}


# --------------------------------------------------------------------- tree
def _tree_push(stack, partial, combine, arena, stats):
    """Push one chunk partial; cascade pairwise combines (binary counter)."""
    stats.chunks += 1
    arena.alloc()
    level = 0
    while level < len(stack) and stack[level] is not None:
        left = stack[level]
        stack[level] = None
        arena.alloc()                       # slot for the combined partial
        partial = combine(left, partial)
        stats.combines += 1
        arena.free(2)                       # the two inputs are released
        level += 1
    if level == len(stack):
        stack.append(None)
    stack[level] = partial


def _tree_finish(stack, combine, arena, stats):
    result = None
    for level in range(len(stack)):
        p = stack[level]
        if p is None:
            continue
        if result is None:
            result = p
        else:
            arena.alloc()
            result = combine(result, p)
            stats.combines += 1
            arena.free(2)
    if result is not None:
        arena.free()                        # final partial slot released
    return result


# ------------------------------------------------------------------ forward
def _forward(op, t, axes, keepdims, mask, chunk_size, stats):
    reduced = set(axes)
    red_dims = [d for d in range(t.ndim) if d in reduced]
    if keepdims:
        out_shape = tuple(1 if d in reduced else t.shape[d]
                          for d in range(t.ndim))
    else:
        out_shape = tuple(t.shape[d] for d in range(t.ndim)
                          if d not in reduced)
    out = Tensor.zeros(out_shape)
    meta = {}                               # full-length key -> (count, ties, value)
    arena = _Arena()
    combine = _COMBINES[op]
    red_sizes = [t.shape[d] for d in red_dims]

    for out_idx in out.indices():
        if keepdims:
            key = out_idx
        else:
            it = iter(out_idx)
            key = tuple(0 if d in reduced else next(it)
                        for d in range(t.ndim))
        stack = []
        chunk = None
        chunk_len = 0
        n = 0
        for red_idx in product(*(range(s) for s in red_sizes)):
            full = list(key)
            for d, r in zip(red_dims, red_idx):
                full[d] = r
            full = tuple(full)
            if mask is not None and mask.get(full) != 0.0:
                continue
            x = t.get(full)
            n += 1
            if chunk is None:
                chunk = (x, 1)
                chunk_len = 1
            else:
                chunk = combine(chunk, (x, 1))
                stats.combines += 1
                chunk_len += 1
            if chunk_size is not None and chunk_len == chunk_size:
                _tree_push(stack, chunk, combine, arena, stats)
                chunk = None
                chunk_len = 0
        if chunk is not None:
            _tree_push(stack, chunk, combine, arena, stats)
        total = _tree_finish(stack, combine, arena, stats)

        if total is None:                   # empty slice
            if op == "sum":
                value, count, ties = 0.0, 0, 0
            else:
                raise EmptyReductionError(
                    f"{op} of an empty slice is undefined (output index "
                    f"{out_idx} has no unmasked elements)")
        else:
            value, count = total
            ties = count if op in ("min", "max") else 0
            if op == "mean":
                value = value / count
        out.set(out_idx, value)
        meta[key] = (count, ties, value)
        stats.slices += 1
        stats.elements += n

    stats.peak_workspace = arena.peak
    return out, meta


# ----------------------------------------------------------------- backward
def _build_grad(op, t, meta, axes, keepdims, mask, upstream):
    """Dense gradient w.r.t. `t`; ties split equally; ties/nans marked."""
    reduced = set(axes)
    red_dims = [d for d in range(t.ndim) if d in reduced]
    red_sizes = [t.shape[d] for d in red_dims]
    g = Tensor.zeros(t.shape)
    nondiff = set()
    for key, (count, ties, value) in meta.items():
        if keepdims:
            up = upstream.get(key)
        else:
            up = upstream.get(tuple(key[d] for d in range(t.ndim)
                                    if d not in reduced))
        for red_idx in product(*(range(s) for s in red_sizes)):
            full = list(key)
            for d, r in zip(red_dims, red_idx):
                full[d] = r
            full = tuple(full)
            if mask is not None and mask.get(full) != 0.0:
                continue
            if op == "sum":
                g.set(full, up)
            elif op == "mean":
                g.set(full, up / count)
            else:  # min / max: equal split among all tied extrema
                x = t.get(full)
                hit = (x == value) or (math.isnan(x) and math.isnan(value))
                if hit:
                    g.set(full, up / ties)
                    if ties > 1 or math.isnan(value):
                        nondiff.add(full)

    def second_backward(_up):
        if nondiff:
            pts = ", ".join(str(p) for p in sorted(nondiff))
            raise NonDifferentiableError(
                f"{op} gradient is only a subgradient at input point(s) "
                f"{pts}; higher-order differentiation is undefined there")
        return (Tensor.zeros(t.shape),)     # d2(sum)/d2(mean)/unique extremum

    g._nondiff = nondiff
    g._ctx = ((t,), second_backward)
    return g


# ------------------------------------------------------------------ public
def _as_mask(mask, t):
    if mask is None:
        return None
    if not isinstance(mask, Tensor):
        mask = Tensor(mask)
    if mask.shape != t.shape:
        raise ValueError(
            f"mask shape {mask.shape} must equal input shape {t.shape}")
    return mask


def _reduce(op, t, axis, keepdims, mask, chunk_size):
    if not isinstance(t, Tensor):
        t = Tensor(t)
    axes = normalize_axes(axis, t.ndim)
    m = _as_mask(mask, t)
    if chunk_size is not None and (not isinstance(chunk_size, int)
                                   or isinstance(chunk_size, bool)
                                   or chunk_size < 1):
        raise ValueError("chunk_size must be a positive int or None")
    stats = ReductionStats(op)
    out, meta = _forward(op, t, axes, keepdims, m, chunk_size, stats)
    out.reduction_stats = stats
    if t.requires_grad:
        out.requires_grad = True

        def backward_fn(up, op=op, t=t, meta=meta, axes=axes,
                        keepdims=keepdims, m=m):
            return (_build_grad(op, t, meta, axes, keepdims, m, up),)

        out._ctx = ((t,), backward_fn)
    return out


def reduce_sum(t, axis=None, keepdims=False, mask=None, chunk_size=None):
    return _reduce("sum", t, axis, keepdims, mask, chunk_size)


def reduce_mean(t, axis=None, keepdims=False, mask=None, chunk_size=None):
    return _reduce("mean", t, axis, keepdims, mask, chunk_size)


def reduce_min(t, axis=None, keepdims=False, mask=None, chunk_size=None):
    return _reduce("min", t, axis, keepdims, mask, chunk_size)


def reduce_max(t, axis=None, keepdims=False, mask=None, chunk_size=None):
    return _reduce("max", t, axis, keepdims, mask, chunk_size)


def value_and_grad(t, op, axis=None, keepdims=False, mask=None,
                   chunk_size=None, upstream=None):
    """Functional gradient interface.

    Returns (value, grad).  `grad` carries `.nondiff_indices` marks and a
    second-order context: calling grad.backward(...) raises
    NonDifferentiableError at marked points, otherwise yields zeros.
    """
    if op not in ("sum", "mean", "min", "max"):
        raise ValueError(f"unknown reduction op: {op!r}")
    if not isinstance(t, Tensor):
        t = Tensor(t)
    axes = normalize_axes(axis, t.ndim)
    m = _as_mask(mask, t)
    stats = ReductionStats(op)
    out, meta = _forward(op, t, axes, keepdims, m, chunk_size, stats)
    out.reduction_stats = stats
    if upstream is None:
        upstream = Tensor.ones(out.shape)
    elif not isinstance(upstream, Tensor):
        upstream = Tensor(upstream, out.shape)
    if upstream.shape != out.shape:
        raise ValueError(
            f"upstream shape {upstream.shape} != output shape {out.shape}")
    grad = _build_grad(op, t, meta, axes, keepdims, m, upstream)
    return out, grad
