"""Predicate -> access-path compiler.

Compiles a typed predicate tree into, per candidate index, a set of
non-overlapping scan intervals in that index's encoded key space, plus a
residual predicate.  Also builds index-union plans for top-level OR and a
full-scan plan, estimates all of them with a configurable cost model, and
picks the cheapest (deterministically).

Correctness contract: the compiled interval set always covers every row for
which the predicate is *not false* (true or UNKNOWN).  The residual predicate
(the full original predicate) is re-evaluated on every candidate row, so
results are exact.  NOT is compiled as the complement of its child's set,
which is sound precisely because child sets include UNKNOWN-producing rows
(e.g. NULL points are included for comparisons).
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, field

from .domains import Rev, encode_value, high_key, low_key
from .intervals import Interval, complement, intersect, normalize, shrink_to_budget
from .predicates import (And, Cmp, In, IsNotNull, IsNull, Not, NotIn, Or,
                         atom_count, negate, resolve)
from .storage import span_of


@dataclass(frozen=True)
class CostModel:
    page_cost: float = 10.0      # cost of reading one index/heap page
    fetch_cost: float = 4.0      # cost of one row fetch by rowid
    compare_cost: float = 1.0    # cost of evaluating one atomic predicate
    page_size: int = 50          # index/heap entries per page
    interval_budget: int = 16    # max scan intervals before degradation


# ---------------------------------------------------------------- intervals

class RangeCompiler:
    """Compiles predicates into intervals of one index's encoded key space."""

    def __init__(self, index, params=None):
        self.index = index
        self.params = params or {}
        self.n = len(index.spec)
        self.LOK = low_key(self.n)
        self.HIK = high_key(self.n)
        self.FULL = Interval(self.LOK, True, self.HIK, True)
        self.pos_of = {s.col: i for i, s in enumerate(index.spec)}

    def compile(self, pred):
        if isinstance(pred, Cmp):
            return self._cmp(pred)
        if isinstance(pred, In):
            return self._in(pred)
        if isinstance(pred, IsNull):
            return self._isnull(pred)
        if isinstance(pred, And):
            acc = self.compile(pred.children[0]) if pred.children else [self.FULL]
            for child in pred.children[1:]:
                other = self.compile(child)
                acc = normalize(
                    iv
                    for a in acc
                    for b in other
                    if not (iv := intersect(a, b)).is_empty()
                )
            return acc
        if isinstance(pred, Or):
            out = []
            for child in pred.children:
                out.extend(self.compile(child))
            return normalize(out)
        if isinstance(pred, Not):
            # push the negation to the atoms; complementing a widened
            # multi-column interval set would under-cover and drop rows
            return self.compile(negate(pred.child))
        if isinstance(pred, NotIn):
            return self._not_in(pred)
        if isinstance(pred, IsNotNull):
            return self._is_not_null(pred)
        raise TypeError(f"unknown predicate node: {pred!r}")

    # -- atoms ------------------------------------------------------------
    def _point(self, pos, enc):
        """Hull interval of 'column pos == encoded value' (wide if pos > 0)."""
        lo = self.LOK[:pos] + (enc,) + self.LOK[pos + 1:]
        hi = self.HIK[:pos] + (enc,) + self.HIK[pos + 1:]
        return Interval(lo, True, hi, True)

    def _null_point(self, pos):
        return self._point(pos, encode_value(None, self.index.spec[pos]))

    def _cmp(self, pred):
        pos = self.pos_of.get(pred.col)
        if pos is None:
            return [self.FULL]
        val = resolve(pred.value, self.params)
        if val is None:
            # any comparison against a NULL literal is UNKNOWN for every row
            return []
        spec = self.index.spec[pos]
        enc = encode_value(val, spec)
        L, H = self.LOK, self.HIK
        op = pred.op
        if spec.desc:
            # DESC encoding reverses the value order inside the component
            op = {"lt": "gt", "le": "ge", "gt": "lt", "ge": "le"}.get(op, op)
        if op == "eq":
            return normalize([self._point(pos, enc), self._null_point(pos)])
        if op == "ne":
            if pos == 0:
                # exact point set on the prefix column: complement is exact
                # and covers NULL rows automatically
                return complement([self._point(pos, enc)], L, H)
            # complementing a widened suffix hull would under-cover; the full
            # range is the only sound interval cover (residual filters)
            return [self.FULL]
        if op == "lt":
            hi = H[:pos] + (enc,) + L[pos + 1:]
            ivs = [Interval(L, True, hi, False)]
        elif op == "le":
            hi = H[:pos] + (enc,) + H[pos + 1:]
            ivs = [Interval(L, True, hi, True)]
        elif op == "gt":
            lo = L[:pos] + (enc,) + H[pos + 1:]
            ivs = [Interval(lo, True, H, True)]
        elif op == "ge":
            lo = L[:pos] + (enc,) + L[pos + 1:]
            ivs = [Interval(lo, True, H, True)]
        else:
            raise ValueError(op)
        # rows where the column is NULL evaluate UNKNOWN (not false): cover them
        ivs.append(self._null_point(pos))
        return normalize(ivs)

    def _in(self, pred):
        pos = self.pos_of.get(pred.col)
        if pos is None:
            return [self.FULL]
        vals = [resolve(x, self.params) for x in pred.values]
        if not vals:
            return []  # IN () is false everywhere
        if any(v is None for v in vals):
            # NULL in the list makes every non-matching row UNKNOWN: cover all
            return [self.FULL]
        spec = self.index.spec[pos]
        ivs = [self._point(pos, encode_value(v, spec)) for v in set(vals)]
        ivs.append(self._null_point(pos))
        return normalize(ivs)

    def _isnull(self, pred):
        pos = self.pos_of.get(pred.col)
        if pos is None:
            return [self.FULL]
        return [self._null_point(pos)]

    def _is_not_null(self, pred):
        pos = self.pos_of.get(pred.col)
        if pos is None or pos > 0:
            return [self.FULL]
        return complement([self._null_point(pos)], self.LOK, self.HIK)

    def _not_in(self, pred):
        pos = self.pos_of.get(pred.col)
        if pos is None:
            return [self.FULL]
        vals = [resolve(x, self.params) for x in pred.values]
        if not vals:
            return [self.FULL]  # NOT IN () is true everywhere
        if any(v is None for v in vals):
            return []  # NULL in the list: IN is never false, NOT IN never true
        if pos > 0:
            return [self.FULL]
        spec = self.index.spec[pos]
        # everything except the listed points; NULL rows stay covered because
        # NOT IN is UNKNOWN (not false) for them
        points = [self._point(pos, encode_value(v, spec)) for v in set(vals)]
        return complement(points, self.LOK, self.HIK)

    # -- budget ------------------------------------------------------------
    def compile_plan(self, pred, budget):
        ivs = normalize(self.compile(pred))
        degraded = False
        if len(ivs) > budget:
            ivs = shrink_to_budget(ivs, budget)
            degraded = True
        return ivs, degraded


# ---------------------------------------------------------------- plans

@dataclass
class Estimate:
    pages: int
    fetches: int
    compares: int
    cost: float

    def as_dict(self):
        return {"pages": self.pages, "fetches": self.fetches,
                "compares": self.compares, "cost": self.cost}


def _ser_payload(p):
    return {"$rev": p.v} if isinstance(p, Rev) else p


def _ser_key(key):
    return [[tag, _ser_payload(p)] for tag, p in key]


def _ser_interval(iv):
    return [_ser_key(iv.lo), iv.lo_inc, _ser_key(iv.hi), iv.hi_inc]


@dataclass
class RangeScanPlan:
    index: object
    intervals: list
    residual: object
    degraded: bool
    est: Estimate
    kind: str = field(default="range", init=False)

    def to_obj(self):
        return {"kind": "range", "index": self.index.name,
                "intervals": [_ser_interval(iv) for iv in self.intervals],
                "residual": repr(self.residual), "degraded": self.degraded}

    def desc(self):
        return f"range:{self.index.name}"


@dataclass
class UnionPlan:
    children: list  # list[RangeScanPlan], one per OR branch
    est: Estimate
    kind: str = field(default="union", init=False)

    def to_obj(self):
        return {"kind": "union", "children": [c.to_obj() for c in self.children]}

    def desc(self):
        return "union(" + ",".join(c.index.name for c in self.children) + ")"


@dataclass
class FullScanPlan:
    residual: object
    est: Estimate
    kind: str = field(default="full", init=False)

    def to_obj(self):
        return {"kind": "full", "residual": repr(self.residual)}

    def desc(self):
        return "full"


def _ser_params(params):
    def conv(v):
        if isinstance(v, Rev):
            return {"$rev": v.v}
        return v
    return {k: conv(v) for k, v in sorted((params or {}).items())}


def plan_fingerprint(plan, params=None):
    """Stable fingerprint binding a plan and its parameters; used by tokens."""
    obj = {"plan": plan.to_obj(), "params": _ser_params(params)}
    s = json.dumps(obj, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(s.encode()).hexdigest()


# ---------------------------------------------------------------- selection

def _ceil_div(a, b):
    return -(-a // b)


def _range_plan(index, pred, params, cm, version):
    rc = RangeCompiler(index, params)
    ivs, degraded = rc.compile_plan(pred, cm.interval_budget)
    view = index.view(version)
    entries = sum(e - s for s, e in (span_of(view, iv) for iv in ivs))
    atoms = atom_count(pred)
    pages = _ceil_div(entries, cm.page_size)
    est = Estimate(
        pages=pages,
        fetches=entries,
        compares=entries * atoms,
        cost=pages * cm.page_cost + entries * cm.fetch_cost
        + entries * atoms * cm.compare_cost,
    )
    return RangeScanPlan(index, ivs, pred, degraded, est)


def compile_query(db, version, pred, params=None, cm=CostModel(), index_names=None):
    """Compile `pred` against `db` at `version`.

    Returns (plan, report).  `report` lists every candidate with its cost
    breakdown and which one was chosen, so the choice is auditable and
    reproducible.
    """
    params = params or {}
    names = sorted(db.indexes) if index_names is None else sorted(index_names)
    atoms = atom_count(pred)
    candidates = []

    rows = len(db.visible_rowids(version))
    pages = _ceil_div(rows, cm.page_size)
    full_est = Estimate(
        pages=pages,
        fetches=rows,
        compares=rows * atoms,
        cost=pages * cm.page_cost + rows * cm.fetch_cost
        + rows * atoms * cm.compare_cost,
    )
    candidates.append((FullScanPlan(pred, full_est), full_est))

    for name in names:
        plan = _range_plan(db.indexes[name], pred, params, cm, version)
        candidates.append((plan, plan.est))

    if isinstance(pred, Or) and len(pred.children) >= 2 and names:
        child_plans = []
        for branch in pred.children:
            best = min(
                (_range_plan(db.indexes[n], branch, params, cm, version)
                 for n in names),
                key=lambda p: (p.est.cost, p.index.name),
            )
            child_plans.append(best)
        # fetches/compares are upper bounds: rowids shared between branches
        # are fetched and emitted only once at execution time.
        est = Estimate(
            pages=sum(c.est.pages for c in child_plans),
            fetches=sum(c.est.fetches for c in child_plans),
            compares=sum(c.est.compares for c in child_plans),
            cost=sum(c.est.cost for c in child_plans),
        )
        candidates.append((UnionPlan(child_plans, est), est))

    rank = {"union": 0, "range": 1, "full": 2}
    chosen, _ = min(
        candidates,
        key=lambda t: (t[1].cost, rank[t[0].kind], t[0].desc()),
    )
    report = {
        "candidates": [
            {"desc": p.desc(), "kind": p.kind, **e.as_dict()}
            for p, e in candidates
        ],
        "chosen": chosen.desc(),
        "cost_model": cm.__dict__,
    }
    return chosen, report
