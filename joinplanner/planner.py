"""Join order planner.

Legality rules
--------------
- INNER join steps reorder freely, subject to dependency constraints.
- A non-INNER (LEFT OUTER) step is a barrier: it may only be placed when
  exactly its original prefix of relations has already been placed.  Outer
  joins do not commute, so the planner never reorders across them.
- `requires` models correlated dependencies: a step may only be placed
  after every relation it depends on.
- Each step carries candidate join `edges` (left_source, left_key,
  right_key); the first edge whose source relation is already placed is
  used, which is what makes inner-join reordering well-defined.

Costing
-------
Every candidate order is costed under optimistic and pessimistic readings
of the interval statistics.  The primary plan minimizes worst-case
(pessimistic) cost; conservative alternatives are returned alongside.
"""
from __future__ import annotations

from dataclasses import dataclass, replace

from .cost import estimate_join
from .relations import INNER
from .stats import Interval


@dataclass(frozen=True)
class JoinStep:
    rel: str
    join_type: str
    edges: tuple                     # tuple[(left_source, left_key, right_key)]
    requires: frozenset = frozenset()
    prefix: frozenset = frozenset()  # filled by Query: relations before this step


@dataclass(frozen=True)
class Query:
    base: str
    steps: tuple

    def __post_init__(self):
        seen = {self.base}
        new_steps = []
        for s in self.steps:
            new_steps.append(replace(s, prefix=frozenset(seen)))
            seen.add(s.rel)
        object.__setattr__(self, "steps", tuple(new_steps))

    def relations(self) -> list:
        return [self.base] + [s.rel for s in self.steps]


def resolve_edge(step: JoinStep, placed: set):
    for src, lk, rk in step.edges:
        if src in placed:
            return src, lk, rk
    return None


def enumerate_legal_orders(steps: list, placed: set) -> list:
    """All orderings of `steps` that respect barrier/dependency constraints."""
    steps = list(steps)
    out = []

    def rec(remaining, placed_set, acc):
        if not remaining:
            out.append(tuple(acc))
            return
        for i, step in enumerate(remaining):
            if step.rel in placed_set:
                continue
            if not set(step.requires) <= placed_set:
                continue
            if resolve_edge(step, placed_set) is None:
                continue
            if step.join_type != INNER and set(step.prefix) != placed_set:
                continue
            rec(remaining[:i] + remaining[i + 1:],
                placed_set | {step.rel}, acc + [step])

    rec(steps, set(placed), [])
    return out


def _chain(base_stats, order, stats, model, mode, placed0):
    """Cost a full order under one reading ('opt'/'pess'/'mid') of the stats."""
    rows_iv = base_stats.rows
    ndv = dict(base_stats.ndv)
    placed = set(placed0)
    total = 0.0
    stages = []
    for step in order:
        src, lk, rk = resolve_edge(step, placed)
        rs = stats[step.rel]
        l_ndv = ndv.get(lk, Interval.unknown()).pick_ndv(mode)
        r_ndv = rs.ndv_for(rk).pick_ndv(mode)
        est = estimate_join(model, rows_iv.pick_rows(mode), rs.rows.pick_rows(mode),
                            l_ndv, r_ndv, step.join_type)
        total += est.total
        stages.append(est.out_rows)
        rows_iv = Interval.point(est.out_rows)
        ndv[lk] = Interval.point(min(l_ndv, r_ndv))
        placed.add(step.rel)
    return total, tuple(stages)


@dataclass(frozen=True)
class Plan:
    order: tuple
    optimistic: float
    pessimistic: float
    stage_estimates: tuple   # expected intermediate rows after each step (mid)


@dataclass(frozen=True)
class PlanChoice:
    primary: Plan
    alternatives: tuple      # conservative fallbacks
    ranked: tuple            # all legal plans, best worst-case first


def choose_order(base_stats, placed, steps, stats, model) -> PlanChoice:
    orders = enumerate_legal_orders(list(steps), set(placed))
    if not orders:
        raise ValueError("no legal join order")
    plans = []
    for order in orders:
        opt, _ = _chain(base_stats, order, stats, model, "opt", placed)
        pess, _ = _chain(base_stats, order, stats, model, "pess", placed)
        _, stages = _chain(base_stats, order, stats, model, "mid", placed)
        plans.append(Plan(order, opt, pess, stages))
    # Stable sort: ties keep enumeration (query) order, so choices are
    # deterministic and replayable.
    plans.sort(key=lambda p: (p.pessimistic, p.optimistic))
    primary = plans[0]
    rest = [p for p in plans if p is not primary]
    alternatives = []
    if rest:
        alternatives.append(sorted(rest, key=lambda p: (p.optimistic, p.pessimistic))[0])
        if rest[0] not in alternatives:
            alternatives.append(rest[0])
    return PlanChoice(primary, tuple(alternatives), tuple(plans))


def plan(query: Query, provider, model) -> PlanChoice:
    stats = {name: provider.get(name) for name in query.relations()}
    return choose_order(stats[query.base], {query.base}, list(query.steps),
                        stats, model)
