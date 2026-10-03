"""Independent brute-force references and small-domain enumeration checks.

The naive reference below is deliberately written independently from the
chunked engine (plain left-to-right loops, no chunking, no tree), and the
engine is checked against it by enumerating small domains: empty dims,
full masks, +/-inf, ties, and aliased gradient accumulation.
"""
from __future__ import annotations

import math
import random
from itertools import product

from .reduce import (OPS, SUM, MEAN, MIN, MAX, UndefinedReductionError,
                     WorkspaceTracker, GradStore,
                     reduce as engine_reduce, backward as engine_backward)
from .tensor import Tensor, iter_indices_for


# ---------------------------------------------------------------- naive ref

def naive_normalize_axes(axes, ndim):
    if axes is None:
        return tuple(range(ndim))
    if isinstance(axes, int):
        axes = (axes,)
    seen = set()
    out = []
    for ax in axes:
        ax = int(ax)
        if ax < 0:
            ax += ndim
        if not 0 <= ax < ndim:
            raise ValueError(f"axis out of range: {ax}")
        if ax in seen:
            raise ValueError(f"duplicate axis: {ax}")
        seen.add(ax)
        out.append(ax)
    return tuple(sorted(out))


def _naive_groups(t, axes_n):
    reduced = set(axes_n)
    groups = {}
    for idx in t.iter_indices():
        pos = t.flat_pos(idx)
        if t.included_pos(pos):
            key = tuple(v for i, v in enumerate(idx) if i not in reduced)
            groups.setdefault(key, []).append(pos)
    return groups


def _naive_combine(vals, op):
    if not vals:
        if op == SUM:
            return 0.0
        raise UndefinedReductionError(f"{op} of empty set")
    if op in (SUM, MEAN):
        total = 0.0
        for v in vals:
            total += v
        return total if op == SUM else total / len(vals)
    if any(v != v for v in vals):
        return float("nan")
    best = vals[0]
    for v in vals[1:]:
        if (op == MIN and v < best) or (op == MAX and v > best):
            best = v
    return best


class NaiveResult:
    def __init__(self, value, values, groups, axes):
        self.value = value
        self.values = values
        self.groups = groups
        self.axes = axes


def naive_reduce(t, op, axes=None, keepdims=False):
    """Reference reduction: plain loops, no chunking."""
    axes_n = naive_normalize_axes(axes, t.ndim)
    reduced = set(axes_n)
    out_shape = tuple(d for i, d in enumerate(t.shape) if i not in reduced)
    kept_shape = tuple(1 if i in reduced else d for i, d in enumerate(t.shape))
    groups = _naive_groups(t, axes_n)
    values = {}
    for out_idx in iter_indices_for(out_shape):
        vals = [t.data[p] for p in groups.get(out_idx, [])]
        values[out_idx] = _naive_combine(vals, op)
    final_shape = kept_shape if keepdims else out_shape
    buf = []
    for idx in iter_indices_for(final_shape):
        out_idx = (tuple(v for i, v in enumerate(idx) if i not in reduced)
                   if keepdims else idx)
        buf.append(values[out_idx])
    return NaiveResult(Tensor(buf, final_shape), values, groups, axes_n)


def _same(a, b):
    return a == b or (a != a and b != b)


def naive_grad_buffer(t, op, axes_n, up_map, groups, values, tie_policy):
    """Reference gradient as a flat list over t's underlying buffer."""
    grad = [0.0] * len(t.data)
    for out_idx, positions in groups.items():
        g = up_map[out_idx]
        if op == SUM:
            for p in positions:
                grad[p] += g
        elif op == MEAN:
            share = g / len(positions)
            for p in positions:
                grad[p] += share
        else:
            target = values[out_idx]
            tied = [p for p in positions if _same(t.data[p], target)]
            if tie_policy == "uniform":
                share = g / len(tied)
                for p in tied:
                    grad[p] += share
            else:
                grad[tied[0]] += g  # positions are in row-major order
    return grad


# ------------------------------------------------------------- enumeration

def _close(a, b):
    if a != a and b != b:
        return True
    if a == b:
        return True
    return math.isclose(a, b, rel_tol=1e-12, abs_tol=1e-12)


def _axes_plans(ndim):
    plans = [None]
    if ndim == 0:
        plans.append(())
    if ndim >= 1:
        plans.extend([0, -1])
    if ndim >= 2:
        plans.extend([1, (0, 1), (1, 0)])
    if ndim >= 3:
        plans.extend([(0, 2), (2, 0)])
    return plans


def run_enumeration(seed=20261004, sampled_configs=40, chunks=(1, 1 << 30),
                    progress=False):
    """Exhaustively check the engine against the naive reference.

    Covers: empty dims, full masks, +/-inf, ties, NaN-free domains,
    both tie policies, axis permutations, and extreme chunk sizes.
    Returns a report dict.
    """
    report = {"configs": 0, "reductions": 0, "grad_checks": 0,
              "nondiff_checks": 0, "failures": [], "peak_workspace": 0}

    def fail(msg):
        if len(report["failures"]) < 20:
            report["failures"].append(msg)

    domain4 = (-math.inf, -0.5, 1.0, math.inf)
    domain3 = (-math.inf, 0.5, math.inf)
    domain6 = (-math.inf, -2.0, -0.5, 0.0, 1.0, math.inf)
    plans = [((), "full", domain4), ((1,), "full", domain4),
             ((3,), "full", domain4), ((2, 2), "full", domain3),
             ((0,), "full", domain4), ((2, 0), "full", domain4),
             ((0, 2), "full", domain4),
             ((2, 3), "sample", domain6), ((2, 2, 2), "sample", domain6)]
    rng_up = random.Random(seed)
    for shape, mode, domain in plans:
        numel = 1
        for d in shape:
            numel *= d
        if mode == "full":
            value_cases = list(product(domain, repeat=numel))
            mask_cases = list(product((True, False), repeat=numel))
        else:
            rng = random.Random(seed + numel)
            value_cases = [tuple(rng.choice(domain) for _ in range(numel))
                           for _ in range(sampled_configs)]
            mask_cases = [tuple([True] * numel)]
            for _ in range(7):
                mask_cases.append(tuple(rng.random() < 0.75
                                        for _ in range(numel)))
        axes_plans = _axes_plans(len(shape))
        if progress:
            print(f"  shape={shape}: {len(value_cases)} values x "
                  f"{len(mask_cases)} masks x {len(axes_plans)} axes")
        for values in value_cases:
            for mask in mask_cases:
                t = Tensor(list(values), shape, mask=list(mask))
                report["configs"] += 1
                for op in OPS:
                    for axes in axes_plans:
                        try:
                            nres = naive_reduce(t, op, axes)
                            n_err = None
                        except UndefinedReductionError as exc:
                            nres = None
                            n_err = exc
                        for chunk in chunks:
                            tracker = WorkspaceTracker()
                            try:
                                res = engine_reduce(t, op, axes=axes,
                                                    chunk_size=chunk,
                                                    tracker=tracker)
                                e_err = None
                            except UndefinedReductionError as exc:
                                res = None
                                e_err = exc
                            report["reductions"] += 1
                            report["peak_workspace"] = max(
                                report["peak_workspace"], tracker.peak)
                            tag = (f"shape={shape} values={values} mask={mask} "
                                   f"op={op} axes={axes} chunk={chunk}")
                            if (n_err is None) != (e_err is None):
                                fail(f"empty-semantics mismatch [{tag}]: "
                                     f"naive_err={n_err} engine_err={e_err}")
                                continue
                            if n_err is not None:
                                continue
                            got_v = res.value.logical_values()
                            want_v = nres.value.logical_values()
                            if (res.value.shape != nres.value.shape
                                    or not all(_close(a, b)
                                               for a, b in zip(got_v, want_v))):
                                fail(f"value mismatch [{tag}]: "
                                     f"{got_v} vs {want_v}")
                                continue
                            if op in (MIN, MAX):
                                expected = set()
                                for out_idx, positions in nres.groups.items():
                                    target = nres.values[out_idx]
                                    ties = sum(1 for p in positions
                                               if _same(t.data[p], target))
                                    if ties > 1:
                                        expected.add(out_idx)
                                report["nondiff_checks"] += 1
                                if set(res.nondiff_outputs) != expected:
                                    fail(f"nondiff mismatch [{tag}]: "
                                         f"{sorted(res.nondiff_outputs)} vs "
                                         f"{sorted(expected)}")
                            up_vals = [rng_up.choice((-2.0, -0.5, 0.5, 1.5))
                                       for _ in range(res.value.numel)]
                            upstream = Tensor(up_vals, res.value.shape)
                            up_map = {idx: upstream.get(idx)
                                      for idx in upstream.iter_indices()}
                            for policy in ("uniform", "first"):
                                store = GradStore()
                                grad_view = engine_backward(
                                    t, res, upstream, store=store,
                                    tie_policy=policy)
                                got = grad_view.logical_values()
                                want = naive_grad_buffer(
                                    t, op, nres.axes, up_map, nres.groups,
                                    nres.values, policy)
                                report["grad_checks"] += 1
                                if (len(got) != len(want)
                                        or not all(_close(a, b)
                                                   for a, b in zip(got, want))):
                                    fail(f"grad mismatch ({policy}) [{tag}]: "
                                         f"{got} vs {want}")
    return report


def run_alias_enumeration(seed=7, chunks=(1, 1 << 30)):
    """Check gradient accumulation through aliased (overlapping) views."""
    report = {"configs": 0, "failures": []}
    rng = random.Random(seed)
    domain = (-1.0, 0.5, 2.0)
    for case in range(64):
        data = [rng.choice(domain) for _ in range(5)]
        base = Tensor(data, (5,))
        view_a = base.slice_axis(0, 0, 3)   # buffer positions 0..2
        view_b = base.slice_axis(0, 2, 5)   # buffer positions 2..4
        op = OPS[case % len(OPS)]
        up_a = rng.choice(domain)
        up_b = rng.choice(domain)
        for chunk in chunks:
            store = GradStore()
            ra = engine_reduce(view_a, op, chunk_size=chunk)
            rb = engine_reduce(view_b, op, chunk_size=chunk)
            engine_backward(view_a, ra, up_a, store=store)
            engine_backward(view_b, rb, up_b, store=store)
            got = list(store.buffer_for(base))
            want = [0.0] * 5
            for view, up in ((view_a, up_a), (view_b, up_b)):
                nres = naive_reduce(view, op)
                buf = naive_grad_buffer(view, op, nres.axes, {(): up},
                                        nres.groups, nres.values, "uniform")
                for i, g in enumerate(buf):
                    want[i] += g
            report["configs"] += 1
            if not all(_close(x, y) for x, y in zip(got, want)):
                if len(report["failures"]) < 20:
                    report["failures"].append(
                        f"alias mismatch op={op} data={data} chunk={chunk}: "
                        f"{got} vs {want}")
    return report


def finite_difference_check(seed=99, eps=1e-6):
    """Central finite differences vs analytic backward, per op."""
    rng = random.Random(seed)
    errors = {}
    for op in OPS:
        values = [rng.uniform(-3.0, 3.0) for _ in range(6)]
        t = Tensor(list(values), (2, 3))
        res = engine_reduce(t, op, axes=(0, 1))
        grad = engine_backward(t, res, 1.0).logical_values()
        numeric = []
        for i in range(6):
            plus = list(values)
            minus = list(values)
            plus[i] += eps
            minus[i] -= eps
            fp = engine_reduce(Tensor(plus, (2, 3)), op, axes=(0, 1)).value.get(())
            fm = engine_reduce(Tensor(minus, (2, 3)), op, axes=(0, 1)).value.get(())
            numeric.append((fp - fm) / (2.0 * eps))
        errors[op] = max(abs(a - b) for a, b in zip(grad, numeric))
    return errors
