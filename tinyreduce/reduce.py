"""Multi-axis reductions (sum/mean/min/max) with gradients.

Semantics
---------
* ``axes``: None reduces all axes; negative axes are normalized to
  ``axis + ndim``; duplicate axes (after normalization) are rejected.
* Empty reduction set (empty dims or fully masked group):
  ``sum -> 0.0``; ``mean``/``min``/``max`` raise ``UndefinedReductionError``.
* Masked elements are excluded as if absent.
* Chunked tree execution: each output group's included elements are folded
  in chunks of ``chunk_size``, then chunk partials are combined pairwise
  (binary tree). All combiners are associative, so results and gradients
  are independent of ``chunk_size``.
* Tie policy for min/max gradient: ``"uniform"`` splits the upstream
  gradient equally among all tied elements; ``"first"`` gives everything
  to the tied element with the lowest row-major logical index. Both are
  computed from the *global* tie set, hence consistent across chunks.
* Non-differentiable points (min/max ties, where only a subgradient
  exists) are *marked* on the result (``nondiff_outputs`` /
  ``nondiff_inputs``); ``backward(..., on_nondiff="raise")`` refuses to
  silently resolve them -- required before any higher-order use.
* NaN propagates to the reduced value; NaN elements count as tied with
  each other.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .tensor import Tensor, iter_indices_for

SUM = "sum"
MEAN = "mean"
MIN = "min"
MAX = "max"
OPS = (SUM, MEAN, MIN, MAX)

TIE_UNIFORM = "uniform"
TIE_FIRST = "first"
_TIE_POLICIES = (TIE_UNIFORM, TIE_FIRST)


class UndefinedReductionError(ValueError):
    """Reduction has no defined value (mean/min/max of an empty set)."""


class NonDifferentiableError(ArithmeticError):
    """Gradient requested at a non-differentiable point in strict mode."""


def normalize_axes(axes, ndim):
    """Normalize ``axes`` to a sorted tuple of unique non-negative ints."""
    if axes is None:
        return tuple(range(ndim))
    if isinstance(axes, int):
        axes = (axes,)
    out = []
    for ax in axes:
        ax = int(ax)
        if ax < 0:
            ax += ndim
        if ax < 0 or ax >= ndim:
            raise ValueError(f"axis {ax} out of range for ndim {ndim}")
        if ax in out:
            raise ValueError(f"duplicate axis {ax} after normalization")
        out.append(ax)
    return tuple(sorted(out))


class WorkspaceTracker:
    """Counts live partial accumulators during tree reduction."""

    def __init__(self):
        self.current = 0
        self.peak = 0

    def alloc(self, n=1):
        self.current += n
        if self.current > self.peak:
            self.peak = self.current

    def free(self, n=1):
        self.current -= n
        if self.current < 0:
            raise AssertionError("workspace underflow")


def _same_value(a, b):
    return a == b or (a != a and b != b)  # NaN counts as equal to NaN


class _ExtremeAcc:
    """Associative accumulator for min/max: value + tie bookkeeping."""

    __slots__ = ("value", "ties", "first_lin")

    def __init__(self, value, first_lin, ties=1):
        self.value = value
        self.ties = ties
        self.first_lin = first_lin


def _merge_extreme(a, b, pick_min):
    a_nan = a.value != a.value
    b_nan = b.value != b.value
    if a_nan or b_nan:
        ties = (a.ties if a_nan else 0) + (b.ties if b_nan else 0)
        return _ExtremeAcc(float("nan"), -1, ties)
    if pick_min:
        if a.value < b.value:
            return a
        if b.value < a.value:
            return b
    else:
        if a.value > b.value:
            return a
        if b.value > a.value:
            return b
    return _ExtremeAcc(a.value, min(a.first_lin, b.first_lin), a.ties + b.ties)


def _tree_combine(items, chunk_size, make, merge, tracker):
    """Fold ``items`` in chunks, then combine partials as a binary tree."""
    partials = []
    n = len(items)
    for start in range(0, n, chunk_size):
        acc = make(items[start])
        stop = min(start + chunk_size, n)
        for i in range(start + 1, stop):
            acc = merge(acc, make(items[i]))
        partials.append(acc)
        tracker.alloc()
    while len(partials) > 1:
        nxt = []
        n = len(partials)
        i = 0
        while i < n:
            if i + 1 < n:
                nxt.append(merge(partials[i], partials[i + 1]))
                tracker.free(2)
                tracker.alloc(1)
                i += 2
            else:
                nxt.append(partials[i])
                i += 1
        partials = nxt
    out = partials[0]
    tracker.free(1)
    return out


@dataclass
class ReductionResult:
    op: str
    value: Tensor                # keepdims-shaped if keepdims else squeezed
    axes: tuple                  # normalized, sorted
    keepdims: bool
    out_shape: tuple             # squeezed output shape
    kept_shape: tuple            # shape with 1s at reduced axes
    nondiff_outputs: frozenset   # squeezed output coords at non-differentiable points
    nondiff_inputs: frozenset    # buffer positions of tied input elements
    workspace_peak: int          # peak live partial accumulators
    tie_policy: str
    groups: dict = field(repr=False)    # out_idx -> [(buffer_pos, linear_index)]
    counts: dict = field(repr=False)    # out_idx -> included element count
    values: dict = field(repr=False)    # out_idx -> reduced value


class GradStore:
    """Gradient buffers keyed by identity of the underlying data buffer.

    Views of the same buffer (aliasing) accumulate into one shared
    gradient buffer.
    """

    def __init__(self):
        self._grads = {}

    def buffer_for(self, tensor):
        key = id(tensor.data)
        buf = self._grads.get(key)
        if buf is None:
            buf = [0.0] * len(tensor.data)
            self._grads[key] = buf
        return buf

    def accumulate(self, tensor, pos, value):
        self.buffer_for(tensor)[pos] += value

    def grad_tensor(self, tensor):
        """Gradient as a Tensor mirroring ``tensor``'s view (shared buffer)."""
        return Tensor(self.buffer_for(tensor), tensor.shape,
                      tensor.strides, tensor.offset)


def reduce(t, op, axes=None, keepdims=False, chunk_size=1024,
           tie_policy=TIE_UNIFORM, tracker=None):
    """Reduce ``t`` over ``axes`` with a chunked tree; returns ReductionResult."""
    if op not in OPS:
        raise ValueError(f"unknown op {op!r}; expected one of {OPS}")
    if not isinstance(chunk_size, int) or chunk_size < 1:
        raise ValueError("chunk_size must be a positive integer")
    if tie_policy not in _TIE_POLICIES:
        raise ValueError(f"tie_policy must be one of {_TIE_POLICIES}")
    axes_n = normalize_axes(axes, t.ndim)
    reduced = set(axes_n)
    out_shape = tuple(d for i, d in enumerate(t.shape) if i not in reduced)
    kept_shape = tuple(1 if i in reduced else d for i, d in enumerate(t.shape))
    tracker = tracker if tracker is not None else WorkspaceTracker()

    groups = {}
    lin = 0
    for idx in t.iter_indices():
        pos = t.flat_pos(idx)
        if t.included_pos(pos):
            out_idx = tuple(v for i, v in enumerate(idx) if i not in reduced)
            groups.setdefault(out_idx, []).append((pos, lin))
        lin += 1

    values = {}
    counts = {}
    nondiff_outputs = set()
    nondiff_inputs = set()
    pick_min = op != MAX
    for out_idx in iter_indices_for(out_shape):
        members = groups.get(out_idx, [])
        counts[out_idx] = len(members)
        if not members:
            if op == SUM:
                values[out_idx] = 0.0
                continue
            raise UndefinedReductionError(
                f"{op} is undefined for an empty reduction set "
                f"(output index {out_idx})")
        if op in (SUM, MEAN):
            total = _tree_combine([t.data[p] for p, _ in members], chunk_size,
                                  lambda v: v, lambda a, b: a + b, tracker)
            values[out_idx] = total if op == SUM else total / len(members)
        else:
            data = t.data
            acc = _tree_combine(
                members, chunk_size,
                lambda item: _ExtremeAcc(data[item[0]], item[1]),
                lambda a, b: _merge_extreme(a, b, pick_min),
                tracker)
            values[out_idx] = acc.value
            if acc.ties > 1:
                nondiff_outputs.add(out_idx)
                for p, _ in members:
                    if _same_value(data[p], acc.value):
                        nondiff_inputs.add(p)

    final_shape = kept_shape if keepdims else out_shape
    buf = []
    for idx in iter_indices_for(final_shape):
        out_idx = (tuple(v for i, v in enumerate(idx) if i not in reduced)
                   if keepdims else idx)
        buf.append(values[out_idx])
    value = Tensor(buf, final_shape)
    return ReductionResult(op=op, value=value, axes=axes_n, keepdims=keepdims,
                           out_shape=out_shape, kept_shape=kept_shape,
                           nondiff_outputs=frozenset(nondiff_outputs),
                           nondiff_inputs=frozenset(nondiff_inputs),
                           workspace_peak=tracker.peak,
                           tie_policy=tie_policy,
                           groups=groups, counts=counts, values=values)


def _upstream_map(upstream, result):
    if isinstance(upstream, (int, float)):
        g = float(upstream)
        return {idx: g for idx in iter_indices_for(result.out_shape)}
    if not isinstance(upstream, Tensor):
        raise TypeError("upstream must be a Tensor or a scalar")
    reduced = set(result.axes)
    if upstream.shape == result.kept_shape:
        out = {}
        for idx in upstream.iter_indices():
            out_idx = tuple(v for i, v in enumerate(idx) if i not in reduced)
            out[out_idx] = upstream.get(idx)
        return out
    if upstream.shape == result.out_shape:
        return {idx: upstream.get(idx) for idx in upstream.iter_indices()}
    raise ValueError(
        f"upstream shape {upstream.shape} matches neither reduced shape "
        f"{result.out_shape} nor keepdims shape {result.kept_shape}")


def backward(t, result, upstream=1.0, store=None, on_nondiff="mark",
             tie_policy=None):
    """Gradient of the reduction w.r.t. ``t``'s view; accumulates into ``store``.

    ``upstream`` may be a Tensor shaped like the reduced value (squeezed or
    keepdims) or a scalar, which broadcasts to every output element.
    ``on_nondiff="raise"`` turns marked non-differentiable points into
    ``NonDifferentiableError`` instead of silently emitting a subgradient.
    Returns the gradient as a Tensor shaped like ``t``.
    """
    if on_nondiff not in ("mark", "raise"):
        raise ValueError("on_nondiff must be 'mark' or 'raise'")
    if on_nondiff == "raise" and result.nondiff_outputs:
        raise NonDifferentiableError(
            f"{result.op} is non-differentiable at outputs "
            f"{sorted(result.nondiff_outputs)} (tied extrema); refusing to "
            f"silently resolve ties for higher-order use")
    policy = tie_policy if tie_policy is not None else result.tie_policy
    if policy not in _TIE_POLICIES:
        raise ValueError(f"tie_policy must be one of {_TIE_POLICIES}")
    up = _upstream_map(upstream, result)
    store = store if store is not None else GradStore()
    data = t.data
    for out_idx, members in result.groups.items():
        g = up[out_idx]
        if result.op == SUM:
            for p, _ in members:
                store.accumulate(t, p, g)
        elif result.op == MEAN:
            share = g / result.counts[out_idx]
            for p, _ in members:
                store.accumulate(t, p, share)
        else:
            v = result.values[out_idx]
            tied = [p for p, _ in members if _same_value(data[p], v)]
            if policy == TIE_UNIFORM:
                share = g / len(tied)
                for p in tied:
                    store.accumulate(t, p, share)
            else:  # TIE_FIRST: members are in row-major logical order
                store.accumulate(t, tied[0], g)
    return store.grad_tensor(t)
