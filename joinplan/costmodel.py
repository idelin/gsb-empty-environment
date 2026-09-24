"""Cost model: per-step intermediate rows, memory footprint and spill.

The cost of a plan is the sum over join steps of

    row_weight * intermediate_rows + 2 * spill_weight * spilled_bytes

where spilled_bytes is the part of the intermediate exceeding the memory
budget (written and re-read once). Costs are intervals when statistics are
intervals, so this is deliberately not a single selectivity product: each
step's intermediate cardinality, its memory footprint and the resulting
spill are accounted for individually.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .query import LEFT
from .stats import ColumnStat, Interval


@dataclass(frozen=True)
class CostParams:
    mem_budget_bytes: int = 1 << 20
    row_weight: float = 1.0
    spill_weight: float = 0.005
    row_width: int = 32


@dataclass
class StepEstimate:
    step: int
    relation: str
    rows: Interval
    spilled_bytes: Interval
    cost: Interval
    edges: tuple


@dataclass
class PlanEstimate:
    order: tuple
    steps: list
    total: Interval


_UNKNOWN_COL = ColumnStat(Interval.unknown(), Interval(0.0, 1.0))


def _col_stat(stats, rel, col):
    rs = stats.get(rel)
    if rs is None:
        return _UNKNOWN_COL
    return rs.columns.get(col, _UNKNOWN_COL)


def estimate_join_rows(prefix_rows, placed, rel, edges, stats):
    """Interval estimate of the intermediate size after joining `rel`."""
    rows = prefix_rows * stats[rel].rows
    preserved = False
    for e in edges:
        if e.left == rel:
            r_col, p_col, p_rel = e.left_col, e.right_col, e.right
        else:
            r_col, p_col, p_rel = e.right_col, e.left_col, e.left
        r_stat = _col_stat(stats, rel, r_col)
        p_stat = _col_stat(stats, p_rel, p_col)
        # ndv cannot exceed the number of rows on each side
        ndv_p = p_stat.ndv.imin(prefix_rows)
        ndv_r = r_stat.ndv.imin(stats[rel].rows)
        sel = Interval(
            1.0 / max(ndv_r.hi, ndv_p.hi, 1.0),
            1.0 / max(ndv_r.lo, ndv_p.lo, 1.0),
        )
        match = Interval(
            (1.0 - r_stat.null_frac.hi) * (1.0 - p_stat.null_frac.hi),
            (1.0 - r_stat.null_frac.lo) * (1.0 - p_stat.null_frac.lo),
        )
        rows = rows * sel * match
        if e.kind == LEFT:
            preserved = True
    if preserved:
        # Outer join: every preserved-side row appears at least once.
        rows = rows.imax(prefix_rows)
    return rows


def estimate_plan(order, query, stats, params):
    steps = []
    placed = []
    prefix_rows = None
    total_lo = total_hi = 0.0
    for i, rel in enumerate(order):
        if i == 0:
            prefix_rows = stats[rel].rows
            placed.append(rel)
            continue
        edges = query.edges_between(rel, placed)
        rows = estimate_join_rows(prefix_rows, placed, rel, edges, stats)
        spilled = Interval(
            max(0.0, rows.lo * params.row_width - params.mem_budget_bytes),
            max(0.0, rows.hi * params.row_width - params.mem_budget_bytes),
        )
        cost = Interval(
            params.row_weight * rows.lo + 2 * params.spill_weight * spilled.lo,
            params.row_weight * rows.hi + 2 * params.spill_weight * spilled.hi,
        )
        steps.append(StepEstimate(i, rel, rows, spilled, cost, tuple(edges)))
        total_lo += cost.lo
        total_hi += cost.hi
        prefix_rows = rows
        placed.append(rel)
    return PlanEstimate(tuple(order), steps, Interval(total_lo, total_hi))
