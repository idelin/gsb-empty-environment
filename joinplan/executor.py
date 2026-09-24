"""Execution engine with runtime observation and safe re-planning.

Joins run left-deep and every intermediate is fully materialized; those
materialization points are the only safe re-planning boundaries. When
observed cardinalities deviate from the estimate intervals (or the catalog
version changes, i.e. statistics are stale), only the *remaining* joins are
re-planned while the materialized intermediate is kept. Already produced
rows can therefore never be repeated, and the final multiset is identical
to an execution without re-planning. When an intermediate exceeds the
memory budget, subsequent joins switch to a partitioned (grace-hash) path.
"""
from __future__ import annotations

from collections import Counter
from dataclasses import dataclass
from enum import Enum

from .planner import make_plan_from_stats
from .query import INNER, LEFT, JoinEdge, Query
from .relations import hash_join, partitioned_join
from .stats import ColumnStat, Interval, RelationStat


class Status(Enum):
    COMPLETED = "completed"
    BUDGET_EXHAUSTED = "budget_exhausted"
    CANCELLED = "cancelled"


@dataclass
class Decision:
    seq: int
    step: int
    kind: str
    detail: dict

    def render(self):
        parts = ", ".join(f"{k}={v}" for k, v in self.detail.items())
        return f"#{self.seq} step={self.step} {self.kind}: {parts}"


@dataclass
class ExecutionResult:
    status: Status
    rows: object
    columns: object
    decisions: list
    spilled_bytes: int
    stale_stats: bool
    final_order: tuple

    def multiset(self):
        return Counter(self.rows or [])

    def decision_log(self):
        return [d.render() for d in self.decisions]


def derive_stat(columns, rows):
    """Exact statistics derived from a materialized intermediate."""
    cols = {}
    for idx, c in enumerate(columns):
        vals = [r[idx] for r in rows]
        nonnull = [v for v in vals if v is not None]
        ndv = len(set(nonnull)) or 1
        null_frac = 1.0 - len(nonnull) / max(len(vals), 1)
        cols[c] = ColumnStat(Interval.exact(ndv), Interval.exact(null_frac))
    return RelationStat(Interval.exact(len(rows)), cols)


class Executor:
    def __init__(self, relations, catalog, params, *, cost_budget=None,
                 cancel_check=None, replan_tolerance=2.0, enable_replan=True):
        self.relations = relations
        self.catalog = catalog
        self.params = params
        self.cost_budget = cost_budget
        self.cancel_check = cancel_check
        self.replan_tolerance = replan_tolerance
        self.enable_replan = enable_replan

    def execute(self, query, plan):
        decisions = []
        seq = [0]

        def log(step, kind, **detail):
            decisions.append(Decision(seq[0], step, kind, detail))
            seq[0] += 1

        p = self.params
        order = list(plan.primary)
        stats = {n: self.catalog.get(n) for n in query.relations}
        est_rows = [stats[order[0]].rows] + [s.rows for s in plan.estimate.steps]
        version = plan.catalog_version
        stale = False
        spilled_total = 0
        cost_acc = 0.0
        partition_mode = False

        current_rows = None
        current_cols = None
        i = 0
        while i < len(order):
            if self.cancel_check and self.cancel_check():
                log(i, "cancel", note="cancelled by caller")
                return ExecutionResult(Status.CANCELLED, None, None, decisions,
                                       spilled_total, stale, tuple(order))
            if self.catalog.version != version:
                stale = True
                version = self.catalog.version
                stats = {n: self.catalog.get(n) for n in query.relations}
                log(i, "stale_stats",
                    note="catalog version changed; refreshed statistics")
                if self.enable_replan:
                    if current_rows is None:
                        fresh = make_plan_from_stats(
                            query, stats, p, catalog_version=version)
                        order = list(fresh.primary)
                        est_rows = [stats[order[0]].rows] + [
                            s.rows for s in fresh.estimate.steps]
                        log(i, "replan", reason="stale_stats",
                            new="->".join(order))
                    elif i < len(order) - 1:
                        order, est_rows = self._replan(
                            query, order, est_rows, i, current_rows,
                            current_cols, stats, log, reason="stale_stats")
            rel = order[i]
            step_spill = 0
            if current_rows is None:
                current_rows = list(self.relations[rel].rows)
                current_cols = tuple(
                    f"{rel}.{c}" for c in self.relations[rel].columns)
            else:
                right = self.relations[rel]
                for e in query.edges_between(rel, order[:i]):
                    left_name = f"{e.left}.{e.left_col}"
                    right_name = f"{e.right}.{e.right_col}"
                    if left_name in current_cols and right_name in current_cols:
                        # Both sides already materialized: apply as a filter.
                        li = current_cols.index(left_name)
                        ri = current_cols.index(right_name)
                        current_rows = [
                            row for row in current_rows
                            if row[li] is not None and row[ri] is not None
                            and row[li] == row[ri]]
                        continue
                    if e.left == rel:
                        assert e.kind == INNER, (
                            "outer join precedence violated by plan")
                        r_col = e.left_col
                        p_prefixed = right_name
                    else:
                        r_col = e.right_col
                        p_prefixed = left_name
                    li = current_cols.index(p_prefixed)
                    ri = right.col_index(r_col)
                    right_rows = right.rows
                    input_bytes = ((len(current_rows) + len(right_rows))
                                   * p.row_width)
                    if partition_mode or input_bytes > p.mem_budget_bytes:
                        nparts = max(2, input_bytes // p.mem_budget_bytes + 1)
                        if not partition_mode:
                            log(i, "partition",
                                note="inputs exceed memory budget; "
                                     "switching to partitioned path",
                                partitions=nparts)
                        partition_mode = True
                        current_rows, step_spill = partitioned_join(
                            current_rows, li, right_rows, ri,
                            len(right.columns), e.kind, nparts, p.row_width)
                        spilled_total += step_spill
                    else:
                        current_rows = hash_join(
                            current_rows, li, right_rows, ri,
                            len(right.columns), e.kind)
                    current_cols = current_cols + tuple(
                        f"{rel}.{c}" for c in right.columns)
            observed = len(current_rows)
            cost_acc += (p.row_weight * observed
                         + 2 * p.spill_weight * step_spill)
            if self.cost_budget is not None and cost_acc > self.cost_budget:
                log(i, "budget",
                    note=f"cost budget exhausted "
                         f"({cost_acc:.4g} > {self.cost_budget})")
                return ExecutionResult(Status.BUDGET_EXHAUSTED, None, None,
                                       decisions, spilled_total, stale,
                                       tuple(order))
            if (self.enable_replan and i < len(order) - 1
                    and not est_rows[i].contains(observed,
                                                 self.replan_tolerance)):
                order, est_rows = self._replan(
                    query, order, est_rows, i, current_rows, current_cols,
                    stats, log,
                    reason=f"observed {observed} rows outside estimate "
                           f"[{est_rows[i].lo:.4g}, {est_rows[i].hi:.4g}]")
            if (not partition_mode
                    and observed * p.row_width > p.mem_budget_bytes):
                partition_mode = True
                log(i, "partition",
                    note="intermediate exceeds memory budget; subsequent "
                         "joins use partitioned path",
                    observed_rows=observed)
            i += 1
        return ExecutionResult(Status.COMPLETED, current_rows, current_cols,
                               decisions, spilled_total, stale, tuple(order))

    def _replan(self, query, order, est_rows, i, current_rows, current_cols,
                stats, log, reason):
        """Re-plan only the remaining joins at a materialization boundary."""
        placed = order[:i + 1]
        remaining = order[i + 1:]
        placed_set = set(placed)
        mid_stat = derive_stat(current_cols, current_rows)
        sub_stats = {"_mid": mid_stat}
        sub_stats.update({n: stats[n] for n in remaining})
        sub_edges = []
        for e in query.edges:
            l_in = e.left in placed_set
            r_in = e.right in placed_set
            if l_in and r_in:
                continue
            if not l_in and not r_in:
                sub_edges.append(e)
            elif l_in:
                sub_edges.append(JoinEdge(
                    "_mid", e.right, f"{e.left}.{e.left_col}",
                    e.right_col, e.kind))
            else:
                # A LEFT edge's preserved side is always placed first, so
                # reaching this branch with kind=LEFT would be illegal.
                assert e.kind == INNER, "outer join precedence violated"
                sub_edges.append(JoinEdge(
                    "_mid", e.left, f"{e.right}.{e.right_col}",
                    e.left_col, e.kind))
        sub_deps = [(a, b) for a, b in query.dependencies
                    if a in remaining and b in remaining]
        sub_query = Query(("_mid",) + tuple(remaining),
                          tuple(sub_edges), tuple(sub_deps))
        sub_plan = make_plan_from_stats(sub_query, sub_stats, self.params,
                                        seed_prefix=("_mid",))
        new_suffix = list(sub_plan.primary[1:])
        log(i, "replan", reason=reason,
            old="->".join(remaining) or "-",
            new="->".join(new_suffix) or "-")
        new_order = placed + new_suffix
        new_est = est_rows[:i + 1] + [s.rows for s in sub_plan.estimate.steps]
        return new_order, new_est
