"""Composite-index access path compiler.

A predicate (already in negation normal form) is compiled per candidate
index into scan *intervals* plus a *guard* residual predicate:

* prefix index columns constrained to exact point values extend the key
  prefix (cartesian product for multiple points);
* the first column constrained by a non-point range terminates the prefix
  and produces closed/open full-length endpoints;
* a column with no constraint terminates the prefix with a prefix interval
  and every later (suffix-only) constraint stays residual;
* OR branches become more intervals; overlapping intervals from different
  branches are merged only into their exact contiguous hull, with a guard
  equal to the OR of the branches' full predicates;
* exceeding the interval budget replaces the interval set by one superset
  hull guarded by the full predicate.  No branch is ever dropped silently.
"""
from __future__ import annotations

import functools
import hashlib
import json
import math
from dataclasses import dataclass, replace

from .core import NULL, MIN, MAX, ColumnSpec, cmp_value, compare_keys
from . import predicates as P


@dataclass(frozen=True)
class CostModel:
    page_read: float = 10.0
    lookup: float = 4.0
    compare: float = 1.0
    keys_per_page: int = 8
    rows_per_page: int = 16


@dataclass(frozen=True)
class ColRange:
    low: object = None       # None = unbounded below
    low_inc: bool = True
    high: object = None      # None = unbounded above
    high_inc: bool = True

    def is_point(self):
        return (self.low is not None and self.high is not None
                and self.low_inc and self.high_inc and self.low == self.high)


@dataclass(frozen=True)
class Interval:
    low: tuple
    low_inc: bool
    high: tuple
    high_inc: bool
    guard: object = None    # residual predicate; None means exact
    source: object = None   # full originating predicate

    def contains(self, specs, key) -> bool:
        c = compare_keys(specs, self.low, key)
        if c > 0 or (c == 0 and not self.low_inc):
            return False
        c = compare_keys(specs, key, self.high)
        if c > 0 or (c == 0 and not self.high_inc):
            return False
        return True


def _not_null_range(spec):
    """Index-order range covering exactly the non-NULL keys."""
    if spec.nulls_first:
        return ColRange(NULL, False, None, True)
    return ColRange(None, True, NULL, False)


def _cmp_ranges_from_cmp(op, v, spec):
    """Range of a comparison in *index order* (direction + NULL placement).

    A comparison is never TRUE for NULL, so the result is intersected with
    the not-null range of the column.
    """
    if v is NULL:
        return []
    if op == "eq":
        return [ColRange(v, True, v, True)]
    if op in ("gt", "ge"):
        inc = op == "ge"
        if spec.desc:
            r = ColRange(None, True, v, inc)
        else:
            r = ColRange(v, inc, None, True)
    elif op in ("lt", "le"):
        inc = op == "le"
        if spec.desc:
            r = ColRange(v, inc, None, True)
        else:
            r = ColRange(None, True, v, inc)
    else:
        return None  # ne is not a single contiguous range
    out = _intersect_two(r, _not_null_range(spec), spec)
    return [] if out is None else [out]


def extract(term, col, spec):
    """Range set that term imposes on column ``col``.

    Returns a list of disjoint ColRanges, [] for a contradiction (e.g.
    "a = NULL"), or None when the term cannot be expressed as ranges.
    """
    if isinstance(term, P.Cmp) and term.col == col:
        return _cmp_ranges_from_cmp(term.op, term.value, spec)
    if isinstance(term, P.In) and term.col == col:
        points = sorted({v for v in term.values if v is not NULL},
                        key=functools.cmp_to_key(lambda a, b: cmp_value(spec, a, b)))
        return [ColRange(v, True, v, True) for v in points]
    if isinstance(term, P.IsNull) and term.col == col:
        return [ColRange(NULL, True, NULL, True)]
    if isinstance(term, P.NotNull) and term.col == col:
        return [_not_null_range(spec)]
    if isinstance(term, P.And):
        out = None
        for ch in term.children:
            r = extract(ch, col, spec)
            if r is None:
                return None  # non-extractable conjunct: keep term residual
            out = r if out is None else intersect_ranges(out, r, spec)
        return out
    if isinstance(term, P.Or):
        parts = [extract(ch, col, spec) for ch in term.children]
        if any(r is None for r in parts):
            return None
        out = []
        for r in parts:
            out.extend(r)
        return normalize_ranges(out, spec)
    return None


def _max_low(spec, r1, r2):
    if r1.low is None:
        return r2.low, r2.low_inc
    if r2.low is None:
        return r1.low, r1.low_inc
    c = cmp_value(spec, r1.low, r2.low)
    if c > 0:
        return r1.low, r1.low_inc
    if c < 0:
        return r2.low, r2.low_inc
    return r1.low, r1.low_inc and r2.low_inc


def _min_high(spec, r1, r2):
    if r1.high is None:
        return r2.high, r2.high_inc
    if r2.high is None:
        return r1.high, r1.high_inc
    c = cmp_value(spec, r1.high, r2.high)
    if c < 0:
        return r1.high, r1.high_inc
    if c > 0:
        return r2.high, r2.high_inc
    return r1.high, r1.high_inc and r2.high_inc


def _intersect_two(r1, r2, spec):
    low, low_inc = _max_low(spec, r1, r2)
    high, high_inc = _min_high(spec, r1, r2)
    if low is not None and high is not None:
        c = cmp_value(spec, low, high)
        if c > 0 or (c == 0 and not (low_inc and high_inc)):
            return None
    return ColRange(low, low_inc, high, high_inc)


def intersect_ranges(r1s, r2s, spec):
    out = []
    for a in r1s:
        for b in r2s:
            r = _intersect_two(a, b, spec)
            if r is not None:
                out.append(r)
    return normalize_ranges(out, spec)


def _ranges_cmp(spec, a, b):
    if a.low is None and b.low is None:
        c = 0
    elif a.low is None:
        return -1
    elif b.low is None:
        return 1
    else:
        c = cmp_value(spec, a.low, b.low)
    if c:
        return c
    if a.low_inc != b.low_inc:
        return 1 if a.low_inc else -1
    return 0


def normalize_ranges(ranges, spec):
    ranges = sorted(ranges, key=functools.cmp_to_key(
        lambda a, b: _ranges_cmp(spec, a, b)))
    out = []
    for r in ranges:
        if out and _ranges_touch(spec, out[-1], r):
            out[-1] = _merge_ranges(out[-1], r, spec)
        else:
            out.append(r)
    return out


def _ranges_touch(spec, a, b):
    if a.high is None or b.low is None:
        return True
    c = cmp_value(spec, a.high, b.low)
    if c < 0:
        return False
    if c == 0 and not (a.high_inc and b.low_inc):
        return False
    return True


def _merge_ranges(a, b, spec):
    if a.high is None:
        high, high_inc = a.high, a.high_inc
    elif b.high is None:
        high, high_inc = b.high, b.high_inc
    else:
        c = cmp_value(spec, a.high, b.high)
        if c > 0:
            high, high_inc = a.high, a.high_inc
        elif c < 0:
            high, high_inc = b.high, b.high_inc
        else:
            high, high_inc = a.high, a.high_inc or b.high_inc
    return ColRange(a.low, a.low_inc, high, high_inc)


def _term_column(term):
    if isinstance(term, (P.Cmp, P.In, P.NotIn, P.IsNull, P.NotNull)):
        return term.col
    if isinstance(term, (P.And, P.Or)):
        cols = {_term_column(c) for c in term.children}
        cols.discard(None)
        if len(cols) == 1:
            return next(iter(cols))
    return None


def _pad(prefix, token, ncols):
    return prefix + (token,) * (ncols - len(prefix))


def _build_index_intervals(specs, constraints):
    """Returns (intervals, used_cols): first ``used_cols`` columns encoded."""
    ncols = len(specs)
    prefixes = [()]
    for i, spec in enumerate(specs):
        ranges = constraints.get(spec.name)
        if ranges is None:
            return ([Interval(_pad(p, MIN, ncols), True,
                              _pad(p, MAX, ncols), True) for p in prefixes],
                    i)
        if not ranges:
            return [], i + 1
        if all(r.is_point() for r in ranges):
            prefixes = [p + (r.low,) for p in prefixes for r in ranges]
            continue
        out = []
        for p in prefixes:
            for r in ranges:
                if r.low is None:
                    low = _pad(p, MIN, ncols)
                    low_inc = True
                elif r.low_inc:
                    low = _pad(p + (r.low,), MIN, ncols)
                    low_inc = True
                elif i < ncols - 1:
                    low = _pad(p + (r.low,), MAX, ncols)
                    low_inc = True
                else:
                    low = _pad(p + (r.low,), MIN, ncols)
                    low_inc = False
                if r.high is None:
                    high = _pad(p, MAX, ncols)
                    high_inc = True
                elif r.high_inc:
                    high = _pad(p + (r.high,), MAX, ncols)
                    high_inc = True
                else:
                    high = _pad(p + (r.high,), MIN, ncols)
                    high_inc = False
                out.append(Interval(low, low_inc, high, high_inc))
        return out, i + 1
    return [Interval(_pad(p, MIN, ncols), True, _pad(p, MAX, ncols), True)
            for p in prefixes], ncols


def _flatten_and(node):
    out = []
    for c in node.children:
        if isinstance(c, P.And):
            out.extend(_flatten_and(c))
        else:
            out.append(c)
    return out


def _compile_and(terms, index, source):
    specs = index.specs
    position = {s.name: i for i, s in enumerate(specs)}
    constraints = {}
    terms_by_col = {}
    residual = []
    flat = []
    for term in terms:
        if isinstance(term, P.And):
            flat.extend(_flatten_and(term))
        else:
            flat.append(term)
    for term in flat:
        col = _term_column(term)
        if col is None or col not in position:
            residual.append(term)
            continue
        ranges = extract(term, col, specs[position[col]])
        if ranges is None:
            residual.append(term)
            continue
        terms_by_col.setdefault(col, []).append(term)
        if col in constraints:
            constraints[col] = intersect_ranges(
                constraints[col], ranges, specs[position[col]])
        else:
            constraints[col] = ranges
    raw, used = _build_index_intervals(specs, constraints)
    if raw:
        for col, col_terms in terms_by_col.items():
            if position[col] >= used:
                residual.extend(col_terms)
    guard = P.And(tuple(residual)) if residual else None
    if guard is not None and len(guard.children) == 1:
        guard = guard.children[0]
    return [replace(iv, guard=guard, source=source) for iv in raw]


def compile_node(node, index):
    if isinstance(node, P.Or):
        out = []
        for ch in node.children:
            out.extend(compile_node(ch, index))
        return out
    terms = _flatten_and(node) if isinstance(node, P.And) else [node]
    return _compile_and(terms, index, source=node)


def _interval_cmp(specs, a, b):
    c = compare_keys(specs, a.low, b.low)
    if c:
        return c
    if a.low_inc != b.low_inc:
        return -1 if a.low_inc else 1
    c = compare_keys(specs, a.high, b.high)
    if c:
        return c
    if a.high_inc != b.high_inc:
        return 1 if a.high_inc else -1
    return 0


def _interval_before(specs, a, b):
    c = compare_keys(specs, a.high, b.low)
    if c < 0:
        return True
    if c == 0 and not (a.high_inc and b.low_inc):
        return True
    return False


def _or_sources(a, b):
    terms = []
    for s in (a, b):
        if isinstance(s, P.Or):
            terms.extend(s.children)
        elif s is not None:
            terms.append(s)
    dedup = []
    seen = set()
    for t in terms:
        key = json.dumps(P.to_dict(t), sort_keys=True)
        if key not in seen:
            seen.add(key)
            dedup.append(t)
    if len(dedup) == 1:
        return dedup[0]
    return P.Or(tuple(dedup))


def _merge_intervals(a, b, specs):
    c = compare_keys(specs, a.low, b.low)
    if c < 0:
        low, low_inc = a.low, a.low_inc
    elif c > 0:
        low, low_inc = b.low, b.low_inc
    else:
        low = a.low
        low_inc = a.low_inc or b.low_inc
    c = compare_keys(specs, a.high, b.high)
    if c > 0:
        high, high_inc = a.high, a.high_inc
    elif c < 0:
        high, high_inc = b.high, b.high_inc
    else:
        high = a.high
        high_inc = a.high_inc or b.high_inc
    source = _or_sources(a.source, b.source)
    guard = None if (a.guard is None and b.guard is None) else source
    return Interval(low, low_inc, high, high_inc, guard=guard, source=source)


def normalize_intervals(intervals, specs):
    ordered = sorted(intervals,
                     key=functools.cmp_to_key(
                         lambda a, b: _interval_cmp(specs, a, b)))
    out = []
    for iv in ordered:
        if out and not _interval_before(specs, out[-1], iv):
            out[-1] = _merge_intervals(out[-1], iv, specs)
        else:
            out.append(iv)
    return out


def apply_budget(intervals, specs, budget):
    if len(intervals) <= budget:
        return intervals, False
    low = min(intervals, key=functools.cmp_to_key(
        lambda a, b: compare_keys(specs, a.low, b.low)))
    high = max(intervals, key=functools.cmp_to_key(
        lambda a, b: compare_keys(specs, a.high, b.high)))
    source = None
    for iv in intervals:
        source = iv.source if source is None else _or_sources(source, iv.source)
    return [Interval(low.low, low.low_inc, high.high, high.high_inc,
                     guard=source, source=source)], True


@dataclass(frozen=True)
class IndexArm:
    index_name: str
    intervals: tuple


@dataclass(frozen=True)
class ScanArm:
    guard: object


def _ser_endpoint_value(v):
    if v is NULL:
        return {"$": "null"}
    if v is MIN:
        return {"$": "min"}
    if v is MAX:
        return {"$": "max"}
    return v


def _ser_interval(iv):
    return {
        "low": [_ser_endpoint_value(v) for v in iv.low],
        "low_inc": iv.low_inc,
        "high": [_ser_endpoint_value(v) for v in iv.high],
        "high_inc": iv.high_inc,
        "guard": P.to_dict(iv.guard) if iv.guard is not None else None,
    }


def _ser_arm(arm):
    if isinstance(arm, ScanArm):
        return {"scan": True, "guard": P.to_dict(arm.guard)}
    return {"index": arm.index_name,
            "intervals": [_ser_interval(iv) for iv in arm.intervals]}


@dataclass
class Plan:
    kind: str               # "index" | "merge" | "scan"
    arms: tuple
    estimate: dict
    pred: object
    degraded: bool = False

    @property
    def fingerprint(self):
        payload = {
            "kind": self.kind,
            "pred": P.to_dict(self.pred),
            "arms": [_ser_arm(a) for a in self.arms],
        }
        raw = json.dumps(payload, sort_keys=True).encode()
        return hashlib.sha256(raw).hexdigest()[:24]

    def report(self):
        return {
            "kind": self.kind,
            "fingerprint": self.fingerprint,
            "degraded": self.degraded,
            "estimate": dict(self.estimate),
            "arms": [_ser_arm(a) for a in self.arms],
        }


def _estimate_index_arm(db, arm, version, costs):
    index = db.indexes[arm.index_name]
    keys = 0
    pages = 0
    for iv in arm.intervals:
        n = index.count_in_interval(iv, version)
        keys += n
        if n:
            pages += max(1, math.ceil(n / costs.keys_per_page))
    has_guard = any(iv.guard is not None for iv in arm.intervals)
    compares = keys + (keys if has_guard else 0)
    return {
        "pages": pages,
        "lookups": keys,
        "compares": compares,
        "total": (pages * costs.page_read + keys * costs.lookup
                  + compares * costs.compare),
    }


def _estimate_scan(db, version, costs):
    n = db.table.count(version)
    pages = max(1, math.ceil(n / costs.rows_per_page)) if n else 0
    return {
        "pages": pages,
        "lookups": 0,
        "compares": n,
        "total": pages * costs.page_read + n * costs.compare,
    }


def _sum_estimates(parts):
    out = {"pages": 0, "lookups": 0, "compares": 0, "total": 0.0}
    for e in parts:
        for k in out:
            out[k] += e[k]
    return out


_KIND_RANK = {"index": 0, "merge": 1, "scan": 2}


def compile_plan(db, pred, budget, costs):
    """Compile ``pred`` against every access path and pick the cheapest.

    Returns (chosen_plan, all_candidate_plans).  Deterministic: ties break
    on (kind rank, fingerprint).
    """
    pred = P.nnf(pred)
    version = db.mvcc.version
    candidates = []

    for name in sorted(db.indexes):
        index = db.indexes[name]
        ivs = normalize_intervals(compile_node(pred, index), index.specs)
        ivs, degraded = apply_budget(ivs, index.specs, budget)
        arm = IndexArm(name, tuple(ivs))
        est = _estimate_index_arm(db, arm, version, costs)
        candidates.append(Plan("index", (arm,), est, pred, degraded))

    if isinstance(pred, P.Or) and len(db.indexes) > 1:
        per_branch = []
        for ch in pred.children:
            best = None
            for name in sorted(db.indexes):
                index = db.indexes[name]
                ivs = normalize_intervals(compile_node(ch, index), index.specs)
                ivs, degraded = apply_budget(ivs, index.specs, budget)
                arm = IndexArm(name, tuple(ivs))
                est = _estimate_index_arm(db, arm, version, costs)
                if best is None or (est["total"], name) < (best[1]["total"],
                                                           best[0].index_name):
                    best = (arm, est, degraded)
            per_branch.append(best)
        by_index = {}
        degraded = False
        for arm, est, deg in per_branch:
            by_index.setdefault(arm.index_name, []).extend(arm.intervals)
            degraded = degraded or deg
        arms = tuple(
            IndexArm(name, tuple(normalize_intervals(
                ivs, db.indexes[name].specs)))
            for name, ivs in sorted(by_index.items()))
        est = _sum_estimates([_estimate_index_arm(db, a, version, costs)
                              for a in arms])
        est["compares"] += est["lookups"]   # row-id dedup across arms
        est["total"] += est["lookups"] * costs.compare
        candidates.append(Plan("merge", arms, est, pred, degraded))

    scan_est = _estimate_scan(db, version, costs)
    candidates.append(Plan("scan", (ScanArm(pred),), scan_est, pred))

    chosen = min(candidates,
                 key=lambda p: (p.estimate["total"], _KIND_RANK[p.kind],
                                p.fingerprint))
    return chosen, candidates
