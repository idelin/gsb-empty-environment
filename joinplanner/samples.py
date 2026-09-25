"""Replayable planning/execution samples on deterministic synthetic data.

Every sample is pure (seeded RNG or literal data, no wall clock) and returns
plain-Python decision records, so running it twice gives identical output.
"""
from __future__ import annotations

import random

from .cost import CostModel, estimate_join
from .executor import Executor
from .planner import JoinStep, Query, enumerate_legal_orders
from .planner import plan as planner_plan
from .planner import resolve_edge
from .relations import INNER, LEFT_OUTER, Relation, exact_ndv, execute_join
from .stats import Interval, RelStats, StatsProvider


def _rel(name, columns, rows):
    return Relation(name, tuple(columns), list(rows))


def _point_stats(rel: Relation, cols) -> RelStats:
    return RelStats(rows=Interval.point(rel.nrows),
                    ndv={c: Interval.point(exact_ndv(rel, c)) for c in cols})


def _exact_plan(query, relations, order, model):
    """Execute an order on real data and sum its true cost."""
    current = relations[query.base]
    placed = {query.base}
    total = 0.0
    for step in order:
        src, lk, rk = resolve_edge(step, placed)
        right = relations[step.rel]
        result = execute_join(current, right, lk, rk, step.join_type)
        est = estimate_join(model, current.nrows, right.nrows,
                            exact_ndv(current, lk), exact_ndv(right, rk),
                            step.join_type)
        total += est.total
        current = result
        placed.add(step.rel)
    return total, current


def _exact_ranking(query, relations, model):
    ranked = []
    for order in enumerate_legal_orders(list(query.steps), {query.base}):
        total, _ = _exact_plan(query, relations, order, model)
        ranked.append(([s.rel for s in order], total))
    ranked.sort(key=lambda t: t[1])
    return ranked


def _greedy_low_selectivity(query, provider, model):
    """Heuristic: repeatedly pick the join with the smallest immediate output.

    This is the 'low selectivity first' rule; it ignores later-stage and
    memory/spill effects, so it is exactly the heuristic the DP planner is
    checked against.
    """
    stats = {n: provider.get(n) for n in query.relations()}
    placed = {query.base}
    rows = stats[query.base].rows.lo
    ndv = {c: iv.lo for c, iv in stats[query.base].ndv.items()}
    remaining = list(query.steps)
    order = []
    while remaining:
        best = None
        for step in remaining:
            if not set(step.requires) <= placed:
                continue
            if step.join_type != INNER and set(step.prefix) != placed:
                continue
            edge = resolve_edge(step, placed)
            if edge is None:
                continue
            src, lk, rk = edge
            rs = stats[step.rel]
            out = rows * rs.rows.lo / max(ndv.get(lk, 1.0),
                                          rs.ndv_for(rk).lo, 1.0)
            if best is None or out < best[0]:
                best = (out, step, lk, rk)
        out, step, lk, rk = best
        order.append(step)
        placed.add(step.rel)
        remaining.remove(step)
        ndv[lk] = min(ndv.get(lk, 1.0), stats[step.rel].ndv_for(rk).lo)
        rows = out
    return tuple(order)


def sample_low_selectivity_exception():
    """Three-table triangle: lowest first-stage selectivity is a trap.

    A-B is the smallest first join, but it leaves A-B joined to C on a
    low-NDV key and explodes.  Joining A-C first is slightly larger up front
    but makes the final join small.  Exhaustive execution of every legal
    order on the real data verifies the DP planner is optimal and quantifies
    the heuristic gap.
    """
    a = _rel("A", ["k1", "k3", "pa"], [(i, i % 450, f"a{i}") for i in range(900)])
    b = _rel("B", ["k1", "k2", "pb"], [(i % 900, i % 10, f"b{i}") for i in range(1000)])
    c = _rel("C", ["k2", "k3", "pc"], [(i % 10, i % 450, f"c{i}") for i in range(1000)])
    rels = {"A": a, "B": b, "C": c}
    query = Query(base="A", steps=(
        JoinStep(rel="B", join_type=INNER, edges=(("A", "k1", "k1"),)),
        JoinStep(rel="C", join_type=INNER,
                 edges=(("B", "k2", "k2"), ("A", "k3", "k3"))),
    ))
    stats = {"A": _point_stats(a, ["k1", "k3"]),
             "B": _point_stats(b, ["k1", "k2"]),
             "C": _point_stats(c, ["k2", "k3"])}
    provider = StatsProvider(stats)
    model = CostModel()
    choice = planner_plan(query, provider, model)
    ranking = _exact_ranking(query, rels, model)
    planner_cost, _ = _exact_plan(query, rels, choice.primary.order, model)
    greedy = _greedy_low_selectivity(query, provider, model)
    greedy_cost, _ = _exact_plan(query, rels, greedy, model)
    best_cost = ranking[0][1]
    return {
        "planner_order": [s.rel for s in choice.primary.order],
        "greedy_order": [s.rel for s in greedy],
        "legal_orders": [o for o, _ in ranking],
        "planner_exact_cost": planner_cost,
        "greedy_exact_cost": greedy_cost,
        "planner_optimal": abs(planner_cost - best_cost) < 1e-9,
        "planner_gap": planner_cost / best_cost,
        "greedy_gap": greedy_cost / best_cost,
    }


def sample_outer_join_barrier():
    """Outer joins do not commute/reassociate: only one order is legal.

    (R LEFT JOIN S) INNER JOIN T  = empty
    R LEFT JOIN (S INNER JOIN T)  = one NULL-padded row
    so reordering across the outer join would change the multiset.  The
    planner enumerates a single legal order.
    """
    r = _rel("R", ["k", "pr"], [(1, "r1")])
    s = _rel("S", ["k", "ps"], [(1, "s1"), (2, "s2")])
    t = _rel("T", ["k", "pt"], [(2, "t2")])
    rels = {"R": r, "S": s, "T": t}
    query = Query(base="R", steps=(
        JoinStep(rel="S", join_type=LEFT_OUTER, edges=(("R", "k", "k"),)),
        JoinStep(rel="T", join_type=INNER, edges=(("R", "k", "k"),)),
    ))
    orders = enumerate_legal_orders(list(query.steps), {"R"})
    legal, legal_result = _exact_plan(query, rels, orders[0], CostModel())
    st = execute_join(s, t, "k", "k", INNER)
    illegal = execute_join(r, st, "k", "k", LEFT_OUTER)
    return {
        "legal_orders": [[s.rel for s in o] for o in orders],
        "legal_result_rows": legal_result.nrows,
        "illegal_reassociated_rows": illegal.nrows,
        "multiset_differs": legal_result.multiset() != illegal.multiset(),
    }


def sample_duplicate_key_spill():
    """Heavy duplicate keys blow past memory: switch to the partitioned path.

    The partitioned path must yield the identical multiset as the in-memory
    hash join; every emitted row matches with the same multiplicity.
    """
    rng = random.Random(7)
    keys = list(range(4))
    a = _rel("A", ["k", "pa"], [(rng.choice(keys), f"a{i}") for i in range(1000)])
    b = _rel("B", ["k", "pb"], [(rng.choice(keys), f"b{i}") for i in range(1000)])
    rels = {"A": a, "B": b}
    query = Query(base="A", steps=(
        JoinStep(rel="B", join_type=INNER, edges=(("A", "k", "k"),)),
    ))
    provider = StatsProvider({
        "A": _point_stats(a, ["k"]),
        "B": _point_stats(b, ["k"]),
    })
    model = CostModel(memory_bytes=50_000, row_width=64)  # 1000*64 > 50k
    report = Executor(rels, provider, model, partitions=16).run(query)
    reference = execute_join(a, b, "k", "k", INNER, partitioned=False)
    return {
        "status": report.status,
        "states": report.states,
        "partitioned_stages": sum(1 for d in report.decisions
                                  if d.get("kind") == "partitioned_join"),
        "result_rows": report.final.nrows if report.final else 0,
        "reference_rows": reference.nrows,
        "multiset_matches_reference":
            report.final is not None
            and report.final.canonical_multiset() == reference.canonical_multiset(),
        "emitted_matches_result":
            _emitted_matches(report),
    }


def _emitted_matches(report):
    from collections import Counter
    return Counter(report.emitted) == (report.final.multiset() if report.final else Counter())


def sample_adaptive_replan():
    """Stale/interval stats vs observation: replan only at a safe boundary.

    Catalog says A has ~100 rows but it has 40k; D's statistics are stale
    (200k claimed, 20k actual) and are refreshed at the first boundary.  The
    executor observes a ~500x deviation after A-B, replans the remaining
    joins (switching to the cheaper D-before-C tail; the C build overflows
    memory and is partitioned), and emits a multiset identical to the
    reference evaluation.  Replaying the decision log reproduces the choice.
    """
    a = _rel("A", ["k", "pa"], [(k, f"a{k}") for k in range(40_000)])
    b = _rel("B", ["k", "pb"], [(k, f"b{k}") for k in range(40_000)])
    d = _rel("D", ["k", "pd"], [(k, f"d{k}") for k in range(20_000)])
    c = _rel("C", ["k", "pc"], [(k, f"c{k}") for k in range(200_000)])
    rels = {"A": a, "B": b, "C": c, "D": d}
    query = Query(base="A", steps=(
        JoinStep(rel="B", join_type=INNER, edges=(("A", "k", "k"),)),
        JoinStep(rel="C", join_type=INNER, edges=(("A", "k", "k"),)),
        JoinStep(rel="D", join_type=INNER, edges=(("A", "k", "k"),)),
    ))
    provider = StatsProvider({
        "A": RelStats(rows=Interval(50, 150),
                      ndv={"k": Interval(50, 150)}),
        "B": RelStats(Interval.point(40_000), {"k": Interval.point(40_000)}),
        "C": RelStats(Interval.point(200_000), {"k": Interval.point(200_000)}),
        # Stale: catalog claims D has 200k rows.
        "D": RelStats(Interval.point(200_000), {"k": Interval.point(200_000)}),
    })
    fired = {"v": False}

    def hook():
        if not fired["v"]:
            fired["v"] = True
            provider.update("D", RelStats(Interval.point(20_000),
                                          {"k": Interval.point(20_000)}))
            provider.bump_epoch()

    model = CostModel(memory_bytes=4 * 1024 * 1024, row_width=64)
    report = Executor(rels, provider, model, partitions=32,
                      deviation_threshold=2.0, boundary_hook=hook).run(query)
    ref = execute_join(a, b, "k", "k", INNER)
    ref = execute_join(ref, c, "k", "k", INNER)
    ref = execute_join(ref, d, "k", "k", INNER)
    stage_order = [d["step"] for d in report.decisions if d.get("kind") == "stage"]
    replans = [d for d in report.decisions if d.get("kind") == "replan"]
    from collections import Counter
    return {
        "status": report.status,
        "states": report.states,
        "initial_order": report.decisions[0]["order"],
        "executed_order": stage_order,
        "replan_count": len(replans),
        "replan_reasons": [
            "estimate_deviation" if d["reason"].startswith("estimate") else d["reason"]
            for d in replans],
        "partitioned_steps": [d["step"] for d in report.decisions
                              if d.get("kind") == "partitioned_join"],
        "result_rows": report.final.nrows if report.final else 0,
        "reference_rows": ref.nrows,
        "multiset_matches_reference":
            report.final is not None
            and report.final.canonical_multiset() == ref.canonical_multiset(),
        "emitted_matches_result":
            Counter(report.emitted) == (report.final.multiset() if report.final else Counter()),
    }


def sample_estimate_deviation():
    """Pure estimate-deviation replan (no stale stats involved).

    B is a correlated dependency of C, so the legal order is A-B-... and
    only the tail (C, D) can reorder.  The catalog underestimates A by ~300x;
    after materializing A-B the observed intermediate is huge.  Under the
    estimated (small) intermediate the [C, D] tail is a (deterministic)
    best-by-tie; with the observed high-NDV intermediate joining D first
    filters it before the memory-overflowing C build, so the executor flips
    the tail to [D, C] at the safe materialization boundary.
    """
    a = _rel("A", ["k", "pa"], [(i, f"a{i}") for i in range(100_000)])
    b = _rel("B", ["k", "pb"], [(i % 100_000, f"b{i}") for i in range(100_000)])
    d = _rel("D", ["k", "pd"], [(k, f"d{k}") for k in range(20_000)])
    c = _rel("C", ["k", "pc"], [(k, f"c{k}") for k in range(200_000)])
    rels = {"A": a, "B": b, "C": c, "D": d}
    query = Query(base="A", steps=(
        JoinStep(rel="B", join_type=INNER, edges=(("A", "k", "k"),)),
        # C may only be introduced after B (correlated dependency).
        JoinStep(rel="C", join_type=INNER, edges=(("A", "k", "k"),),
                 requires=frozenset({"B"})),
        JoinStep(rel="D", join_type=INNER, edges=(("A", "k", "k"),)),
    ))
    provider = StatsProvider({
        "A": RelStats(Interval(250, 350), {"k": Interval(250, 350)}),
        "B": RelStats(Interval.point(100_000), {"k": Interval.point(100_000)}),
        "C": RelStats(Interval.point(200_000), {"k": Interval.point(200_000)}),
        "D": RelStats(Interval.point(20_000), {"k": Interval.point(20_000)}),
    })
    model = CostModel(memory_bytes=2 * 1024 * 1024, row_width=64)
    report = Executor(rels, provider, model, partitions=32,
                      deviation_threshold=2.0).run(query)
    ref = execute_join(a, b, "k", "k", INNER)
    ref = execute_join(ref, c, "k", "k", INNER)
    ref = execute_join(ref, d, "k", "k", INNER)
    stage_order = [x["step"] for x in report.decisions if x.get("kind") == "stage"]
    replans = [x for x in report.decisions if x.get("kind") == "replan"]
    from collections import Counter
    return {
        "status": report.status,
        "states": report.states,
        "initial_order": report.decisions[0]["order"],
        "executed_order": stage_order,
        "tail_flipped": stage_order == ["B", "D", "C"],
        "replan_reasons": [
            "estimate_deviation" if x["reason"].startswith("estimate") else x["reason"]
            for x in replans],
        "partitioned_steps": [x["step"] for x in report.decisions
                              if x.get("kind") == "partitioned_join"],
        "result_rows": report.final.nrows if report.final else 0,
        "reference_rows": ref.nrows,
        "multiset_matches_reference":
            report.final is not None
            and report.final.canonical_multiset() == ref.canonical_multiset(),
        "emitted_matches_result":
            Counter(report.emitted) == (report.final.multiset() if report.final else Counter()),
    }


def _state_scenario_relations():
    a = _rel("A", ["k", "pa"], [(i % 200, f"a{i}") for i in range(1000)])
    b = _rel("B", ["k", "pb"], [(i % 200, f"b{i}") for i in range(1000)])
    rels = {"A": a, "B": b}
    query = Query(base="A", steps=(
        JoinStep(rel="B", join_type=INNER, edges=(("A", "k", "k"),)),
    ))
    provider = StatsProvider({
        "A": _point_stats(a, ["k"]),
        "B": _point_stats(b, ["k"]),
    })
    return rels, query, provider


def sample_terminal_states():
    """Explicit handling of stale stats, budget exhaustion and cancellation."""
    model = CostModel()

    rels, query, provider = _state_scenario_relations()
    def hook():
        provider.bump_epoch()
    stale = Executor(rels, provider, model, boundary_hook=hook).run(query)

    rels, query, provider = _state_scenario_relations()
    broke = Executor(rels, provider, model, budget=10.0).run(query)

    rels, query, provider = _state_scenario_relations()
    calls = {"n": 0}
    def cancel():
        calls["n"] += 1
        return calls["n"] > 2
    cancelled = Executor(rels, provider, model, chunk_size=100,
                         cancel_check=cancel).run(query)

    rels, query, provider = _state_scenario_relations()
    ref = execute_join(rels["A"], rels["B"], "k", "k", INNER)
    from collections import Counter
    partial = Counter(cancelled.emitted)
    emitted_is_prefix = all(partial[row] <= count
                            for row, count in ref.multiset().items())
    return {
        "stale_stats_status": stale.status,
        "stale_stats_flag": "STALE_STATS" in stale.states,
        "budget_status": broke.status,
        "budget_cost_spent": broke.cost_spent,
        "cancel_status": cancelled.status,
        "cancelled_emitted_rows": len(cancelled.emitted),
        "emitted_submultiset_of_result": emitted_is_prefix,
    }


SAMPLES = {
    "low_selectivity_exception": sample_low_selectivity_exception,
    "outer_join_barrier": sample_outer_join_barrier,
    "duplicate_key_spill": sample_duplicate_key_spill,
    "adaptive_replan": sample_adaptive_replan,
    "estimate_deviation": sample_estimate_deviation,
    "terminal_states": sample_terminal_states,
}


def run_all():
    return {name: fn() for name, fn in SAMPLES.items()}
