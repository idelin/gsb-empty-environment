"""Replayable end-to-end samples (deterministic, no randomness).

Run with:  python -m joinplan.samples
"""
from __future__ import annotations

from .costmodel import CostParams
from .executor import Executor, Status
from .planner import legal_orders, make_plan
from .query import INNER, LEFT, JoinEdge, Query
from .relations import Relation, named_multiset
from .stats import Catalog, ColumnStat, Interval, RelationStat


def _stat(rows, cols):
    return RelationStat(Interval.exact(rows), {
        c: ColumnStat(Interval.exact(ndv)) for c, ndv in cols.items()})


def scenario_inner_reorder():
    """Three-table inner join: low-selectivity edge first, exhaustive check."""
    rels = {
        "A": Relation("A", ("id", "b_key"),
                      [(i, i % 100) for i in range(1000)]),
        "B": Relation("B", ("id", "a_key", "c_key"),
                      [(i, i % 100, i % 10) for i in range(1000)]),
        "C": Relation("C", ("id", "b_key"),
                      [(i, i % 10) for i in range(1000)]),
    }
    cat = Catalog()
    cat.set("A", _stat(1000, {"b_key": 100}))
    cat.set("B", _stat(1000, {"a_key": 100, "c_key": 10}))
    cat.set("C", _stat(1000, {"b_key": 10}))
    query = Query(("A", "B", "C"), (
        JoinEdge("A", "B", "b_key", "a_key"),
        JoinEdge("B", "C", "c_key", "b_key"),
    ))
    plan = make_plan(query, cat, CostParams())
    lines = ["chain A-B (sel 1/100) and B-C (sel 1/10); "
             "low-selectivity-first must win"]
    lines += plan.describe()
    return lines


def scenario_outer_join():
    """Outer join is not commutative: illegal orders are rejected."""
    rels = {
        "A": Relation("A", ("id", "v"),
                      [(1, "a1"), (2, "a2"), (3, "a3")]),
        "B": Relation("B", ("b_id", "a_id", "c"),
                      [(10, 2, "x"), (20, 3, "y"), (30, 4, "z")]),
        "C": Relation("C", ("c",), [("x",), ("y",)]),
    }
    cat = Catalog()
    cat.set("A", _stat(3, {"id": 3}))
    cat.set("B", _stat(3, {"a_id": 3, "c": 3}))
    cat.set("C", _stat(2, {"c": 2}))
    query = Query(("A", "B", "C"), (
        JoinEdge("A", "B", "id", "a_id", LEFT),
        JoinEdge("B", "C", "c", "c"),
    ))
    legal = list(legal_orders(query))
    plan = make_plan(query, cat, CostParams())
    result = Executor(rels, cat, CostParams()).execute(query, plan)
    lines = ["A LEFT JOIN B INNER JOIN C; preserved side A must precede B",
             f"legal orders: {['->'.join(o) for o in legal]}",
             f"planned: {' -> '.join(plan.primary)}"]
    lines += [f"result row: {row}" for row in sorted(
        result.rows, key=lambda r: (r[0], str(r[1:])))]
    lines.append("note: A.id=1 is padded with NULLs, then filtered by the "
                 "inner join with C (NULL never matches); B.a_id=4 is dropped")
    return lines


def scenario_unknown_stats():
    """Unknown statistics: conservative plan plus alternatives."""
    cat = Catalog()
    cat.set("A", _stat(1000, {"a_key": 100}))
    cat.set("B", _stat(1000, {"a_key": 100, "c_key": 100}))
    cat.set("C", RelationStat.unknown(("c_key",)))
    query = Query(("A", "B", "C"), (
        JoinEdge("A", "B", "a_key", "a_key"),
        JoinEdge("B", "C", "c_key", "c_key"),
    ))
    plan = make_plan(query, cat, CostParams())
    lines = ["C has unknown statistics (wide intervals); "
             "plans are ranked by worst-case (hi) cost"]
    lines += plan.describe()
    return lines


def _partition_setup():
    rels = {
        "A": Relation("A", ("id", "k"),
                      [(i, i % 10) for i in range(200)]),
        "B": Relation("B", ("id", "k", "c_key"),
                      [(i, i % 10, i % 5) for i in range(2000)]),
        "C": Relation("C", ("c_key",), [(i,) for i in range(5)]),
    }
    cat = Catalog()
    cat.set("A", _stat(200, {"k": 10}))
    cat.set("B", _stat(2000, {"k": 10, "c_key": 5}))
    cat.set("C", _stat(5, {"c_key": 5}))
    query = Query(("A", "B", "C"), (
        JoinEdge("A", "B", "k", "k"),
        JoinEdge("B", "C", "c_key", "c_key"),
    ))
    return rels, cat, query


def scenario_partitioned():
    """Heavy duplicate keys: intermediate exceeds memory, partitioned path."""
    rels, cat, query = _partition_setup()
    small = CostParams(mem_budget_bytes=32 << 10)
    plan = make_plan(query, cat, small)
    result = Executor(rels, cat, small).execute(query, plan)
    baseline = Executor(rels, cat, CostParams(mem_budget_bytes=1 << 30),
                        enable_replan=False).execute(query, plan)
    same = (named_multiset(result.columns, result.rows)
            == named_multiset(baseline.columns, baseline.rows))
    lines = ["duplicate keys: A x B blows the 32KiB memory budget",
             f"planned: {' -> '.join(plan.primary)}",
             f"status: {result.status.value}, "
             f"spilled_bytes: {result.spilled_bytes}, "
             f"rows: {len(result.rows)}",
             f"multiset identical to non-partitioned run: {same}"]
    lines += result.decision_log()
    return lines


def _replan_setup():
    rels = {
        "R": Relation("R", ("id", "t_key"),
                      [(i, i % 50) for i in range(100)]),
        "S": Relation("S", ("id", "r_key", "u_key"),
                      [(i, i % 100, i % 2) for i in range(4000)]),
        "T": Relation("T", ("t_key",), [(i % 50,) for i in range(100)]),
        "U": Relation("U", ("u_key",), [(i % 2,) for i in range(100)]),
    }
    cat = Catalog()
    # Stale/wrong statistics: R and U row counts and S.u_key ndv are far
    # off, so the initial plan joins U too early.
    cat.set("R", _stat(10000, {"id": 100, "t_key": 50}))
    cat.set("S", _stat(1000, {"r_key": 100, "u_key": 1000}))
    cat.set("T", _stat(100, {"t_key": 50}))
    cat.set("U", _stat(2000, {"u_key": 2}))
    query = Query(("R", "S", "T", "U"), (
        JoinEdge("R", "S", "id", "r_key"),
        JoinEdge("R", "T", "t_key", "t_key"),
        JoinEdge("S", "U", "u_key", "u_key"),
    ))
    return rels, cat, query


def scenario_replan():
    """Observed cardinality deviates: re-plan remaining joins at boundary."""
    rels, cat, query = _replan_setup()
    params = CostParams()
    plan = make_plan(query, cat, params)
    result = Executor(rels, cat, params).execute(query, plan)
    baseline = Executor(rels, cat, params,
                        enable_replan=False).execute(query, plan)
    same = (named_multiset(result.columns, result.rows)
            == named_multiset(baseline.columns, baseline.rows))
    lines = ["stale statistics: R claimed 10000 rows (actual 100), "
             "U claimed 2000 (actual 100), S claimed 1000 rows (actual "
             "4000) with u_key ndv 1000 (actual 2)",
             f"initial plan: {' -> '.join(plan.primary)}",
             f"final order:  {' -> '.join(result.final_order)}",
             f"status: {result.status.value}, rows: {len(result.rows)}",
             f"multiset identical to no-replan run: {same}"]
    lines += result.decision_log()
    return lines


def scenario_states():
    """Explicit states: stale statistics, budget exhausted, cancellation."""
    rels, cat, query = _replan_setup()
    params = CostParams()
    lines = []

    plan = make_plan(query, cat, params)
    cat.set("T", _stat(120, {"t_key": 50}))  # bump catalog version
    stale_result = Executor(rels, cat, params).execute(query, plan)
    lines.append(f"stale stats detected: {stale_result.stale_stats}, "
                 f"status: {stale_result.status.value}")
    lines += [d for d in stale_result.decision_log() if "stale" in d]

    plan2 = make_plan(query, cat, params)
    budget = Executor(rels, cat, params, cost_budget=3000)
    budget_result = budget.execute(query, plan2)
    lines.append(f"budget run status: {budget_result.status.value} "
                 f"(rows returned: {budget_result.rows is not None})")

    calls = iter(range(10))
    cancel = Executor(rels, cat, params,
                      cancel_check=lambda: next(calls) >= 2)
    cancel_result = cancel.execute(query, plan2)
    lines.append(f"cancel run status: {cancel_result.status.value} "
                 f"(rows returned: {cancel_result.rows is not None})")
    return lines


SCENARIOS = (
    scenario_inner_reorder,
    scenario_outer_join,
    scenario_unknown_stats,
    scenario_partitioned,
    scenario_replan,
    scenario_states,
)


def run_all():
    out = []
    for fn in SCENARIOS:
        out.append(f"=== {fn.__name__} ===")
        out.extend(fn())
        out.append("")
    return "\n".join(out)


if __name__ == "__main__":
    print(run_all())
