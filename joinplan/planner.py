"""Join-order planning.

Inner joins commute freely; outer joins and correlated dependencies impose
precedence constraints, and cross products are disallowed. The heuristic is
greedy low-selectivity-first, with the exception that it may never emit an
illegal order (outer-join commutation, dependency violation, cross
product): an illegal greedy result is repaired to the cheapest legal order.
For small queries every legal plan is enumerated so the heuristic is
validated as cost-optimal or its gap is reported.
"""
from __future__ import annotations

from dataclasses import dataclass
from itertools import permutations

from .costmodel import estimate_join_rows, estimate_plan
from .stats import Interval


def _predecessors(query):
    preds = {r: set() for r in query.relations}
    for a, b in query.precedence_pairs():
        preds[b].add(a)
    return preds


def is_legal_order(order, query):
    pos = {r: i for i, r in enumerate(order)}
    for a, b in query.precedence_pairs():
        if pos[a] >= pos[b]:
            return False
    for i in range(1, len(order)):
        if not query.edges_between(order[i], order[:i]):
            return False  # no cross products
    return True


def legal_orders(query, seed_prefix=()):
    prefix = tuple(seed_prefix)
    rest = [r for r in query.relations if r not in prefix]
    for perm in permutations(rest):
        order = prefix + perm
        if is_legal_order(order, query):
            yield order


def _step_rows_hi(placed, rel, query, stats, params):
    est = estimate_plan(tuple(placed) + (rel,), query, stats, params)
    return est.steps[-1].rows.hi


def _first_relation(cands, query, stats):
    """Pick the starting relation of the cheapest available join pair."""
    def key(r):
        best = None
        for e in query.edges:
            other = None
            if e.left == r and e.right in cands:
                other = e.right
            elif e.right == r and e.left in cands:
                other = e.left
            if other is None:
                continue
            rows = estimate_join_rows(stats[r].rows, [r], other, [e], stats)
            best = rows.hi if best is None else min(best, rows.hi)
        return (best if best is not None else float("inf"),
                stats[r].rows.hi, r)
    return min(cands, key=key)


def _cheapest_legal(query, stats, params, seed_prefix):
    ranked = ranked_orders(query, stats, params, seed_prefix)
    if not ranked:
        raise ValueError("no legal plan exists for this query")
    return ranked[0][0]


def heuristic_order(query, stats, params, seed_prefix=()):
    placed = list(seed_prefix)
    remaining = [r for r in query.relations if r not in placed]
    preds = _predecessors(query)
    while remaining:
        cands = [r for r in remaining if preds[r] <= set(placed)]
        if placed:
            conn = [r for r in cands if query.edges_between(r, placed)]
            if not conn:
                # Greedy cannot be extended legally (e.g. an outer join or a
                # dependency blocks every connected relation): repair to the
                # cheapest fully legal order.
                return _cheapest_legal(query, stats, params, seed_prefix)
            cands = conn
        if placed:
            chosen = min(cands, key=lambda r: (
                _step_rows_hi(placed, r, query, stats, params), r))
        else:
            chosen = _first_relation(cands, query, stats)
        placed.append(chosen)
        remaining.remove(chosen)
    order = tuple(placed)
    if not is_legal_order(order, query):
        return _cheapest_legal(query, stats, params, seed_prefix)
    return order


def ranked_orders(query, stats, params, seed_prefix=()):
    """All legal orders ranked by worst-case (hi) cost."""
    ranked = [(order, estimate_plan(order, query, stats, params))
              for order in legal_orders(query, seed_prefix)]
    ranked.sort(key=lambda t: (t[1].total.hi, t[0]))
    return ranked


@dataclass
class PlanResult:
    primary: tuple
    estimate: object
    alternatives: list          # list of (order, total Interval)
    conservative: bool          # statistics unknown / interval-valued
    catalog_version: int
    optimal: object             # None if not exhaustively checked
    gap: object                 # heuristic hi-cost minus best legal hi-cost

    def describe(self):
        lines = [
            f"primary: {' -> '.join(self.primary)} "
            f"cost=[{self.estimate.total.lo:.4g}, {self.estimate.total.hi:.4g}]"
            + (" (conservative: statistics unknown/interval-valued)"
               if self.conservative else "")
        ]
        for order, total in self.alternatives:
            lines.append(
                f"alternative: {' -> '.join(order)} "
                f"cost=[{total.lo:.4g}, {total.hi:.4g}]")
        if self.optimal is not None:
            lines.append("exhaustive check: " + (
                "heuristic is cost-optimal" if self.optimal
                else f"heuristic gap={self.gap:.4g}"))
        return lines


def make_plan_from_stats(query, stats, params, *, catalog_version=0,
                         num_alternatives=2, seed_prefix=(),
                         exhaustive_limit=6):
    primary = heuristic_order(query, stats, params, seed_prefix)
    est = estimate_plan(primary, query, stats, params)
    conservative = any(s.is_wide() for s in stats.values())
    ranked = ranked_orders(query, stats, params, seed_prefix)
    alternatives = [(o, e.total) for o, e in ranked
                    if o != primary][:num_alternatives]
    optimal = gap = None
    if ranked and len(query.relations) <= exhaustive_limit:
        _, best_est = ranked[0]
        gap = est.total.hi - best_est.total.hi
        optimal = gap <= 1e-9
    return PlanResult(primary, est, alternatives, conservative,
                      catalog_version, optimal, gap)


def make_plan(query, catalog, params, **kw):
    stats = {name: catalog.get(name) for name in query.relations}
    return make_plan_from_stats(query, stats, params,
                                catalog_version=catalog.version, **kw)
