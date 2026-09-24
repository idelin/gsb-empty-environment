import unittest

from joinplan.costmodel import CostParams, estimate_plan
from joinplan.executor import Executor, Status
from joinplan.planner import (heuristic_order, is_legal_order, legal_orders,
                              make_plan, ranked_orders)
from joinplan.query import INNER, LEFT, JoinEdge, Query
from joinplan.relations import (Relation, hash_join, named_multiset,
                                partitioned_join)
from joinplan.samples import _partition_setup, _replan_setup, run_all
from joinplan.stats import Catalog, ColumnStat, Interval, RelationStat


def stat(rows, cols):
    return RelationStat(Interval.exact(rows), {
        c: ColumnStat(Interval.exact(ndv)) for c, ndv in cols.items()})


def chain_query_and_catalog():
    """A-B (sel 1/100) and B-C (sel 1/10), 1000 rows each."""
    cat = Catalog()
    cat.set("A", stat(1000, {"b_key": 100}))
    cat.set("B", stat(1000, {"a_key": 100, "c_key": 10}))
    cat.set("C", stat(1000, {"b_key": 10}))
    query = Query(("A", "B", "C"), (
        JoinEdge("A", "B", "b_key", "a_key"),
        JoinEdge("B", "C", "c_key", "b_key"),
    ))
    return query, cat


class TestNullAndMultisetSemantics(unittest.TestCase):
    def setUp(self):
        self.r1 = Relation("R", ("k", "v"),
                           [(1, "a"), (1, "b"), (None, "n"), (2, "c")])
        self.r2 = Relation("S", ("k", "w"),
                           [(1, "x"), (1, "y"), (1, "z"), (None, "m"),
                            (3, "q")])

    def test_null_never_matches(self):
        out = hash_join(self.r1.rows, 0, self.r2.rows, 0, 2, INNER)
        self.assertTrue(all(row[0] is not None and row[2] is not None
                            for row in out))
        self.assertFalse(any(row[0] is None or row[2] is None for row in out))

    def test_multiset_duplicate_keys_multiply(self):
        out = hash_join(self.r1.rows, 0, self.r2.rows, 0, 2, INNER)
        # key 1: 2 left rows x 3 right rows = 6; nothing else matches
        self.assertEqual(len(out), 6)
        self.assertEqual(out.count((1, "a", 1, "x")), 1)
        self.assertEqual(out.count((1, "b", 1, "z")), 1)

    def test_left_join_pads_unmatched(self):
        out = hash_join(self.r1.rows, 0, self.r2.rows, 0, 2, LEFT)
        # 6 matched + (None,'n') padded + (2,'c') padded
        self.assertEqual(len(out), 8)
        self.assertIn((None, "n", None, None), out)
        self.assertIn((2, "c", None, None), out)
        # NULL keys on either side never match, even under LEFT
        self.assertFalse(any(row[2] is None and row[0] is not None
                             and row[0] == 3 for row in out))

    def test_partitioned_join_matches_hash_join(self):
        left = [(i % 7, f"l{i}") for i in range(500)] + [(None, "ln")]
        right = [(i % 5, f"r{i}") for i in range(300)] + [(None, "rn")]
        for kind in (INNER, LEFT):
            expected = hash_join(left, 0, right, 0, 2, kind)
            got, spilled = partitioned_join(left, 0, right, 0, 2, kind,
                                            num_partitions=4, row_width=32)
            self.assertEqual(sorted(map(repr, expected)),
                             sorted(map(repr, got)))
            self.assertGreater(spilled, 0)


class TestLegalityConstraints(unittest.TestCase):
    def test_inner_joins_commute(self):
        query, cat = chain_query_and_catalog()
        orders = set(legal_orders(query))
        self.assertEqual(orders, {("A", "B", "C"), ("B", "A", "C"),
                                  ("B", "C", "A"), ("C", "B", "A")})

    def test_outer_join_not_commutative(self):
        cat = Catalog()
        cat.set("A", stat(100, {"id": 100}))
        cat.set("B", stat(10000, {"a_id": 100, "c": 10000}))
        cat.set("C", stat(1000, {"c": 1000}))
        query = Query(("A", "B", "C"), (
            JoinEdge("A", "B", "id", "a_id", LEFT),
            JoinEdge("B", "C", "c", "c"),
        ))
        # B-C is the cheaper pair, but B may not precede A: the only legal
        # order keeps the preserved side first.
        orders = list(legal_orders(query))
        self.assertEqual(orders, [("A", "B", "C")])
        stats = {n: cat.get(n) for n in query.relations}
        order = heuristic_order(query, stats, CostParams())
        self.assertEqual(order, ("A", "B", "C"))
        self.assertTrue(is_legal_order(order, query))

    def test_dependency_restricts_order(self):
        query = Query(("A", "B", "C"), (
            JoinEdge("A", "B", "x", "x"),
            JoinEdge("B", "C", "y", "y"),
        ), dependencies=(("A", "C"),))
        orders = list(legal_orders(query))
        self.assertEqual(set(orders), {("A", "B", "C"), ("B", "A", "C")})
        for order in orders:
            self.assertLess(order.index("A"), order.index("C"))

    def test_no_cross_products(self):
        query = Query(("A", "B"), ())
        self.assertEqual(list(legal_orders(query)), [])

    def test_outer_join_result_semantics(self):
        rels = {
            "A": Relation("A", ("id", "v"),
                          [(1, "a1"), (2, "a2"), (3, "a3")]),
            "B": Relation("B", ("b_id", "a_id"),
                          [(10, 2), (20, 3), (30, 4)]),
        }
        cat = Catalog()
        cat.set("A", stat(3, {"id": 3}))
        cat.set("B", stat(3, {"a_id": 3}))
        query = Query(("A", "B"), (JoinEdge("A", "B", "id", "a_id", LEFT),))
        plan = make_plan(query, cat, CostParams())
        result = Executor(rels, cat, CostParams()).execute(query, plan)
        self.assertEqual(result.status, Status.COMPLETED)
        self.assertEqual(sorted(result.rows),
                         [(1, "a1", None, None), (2, "a2", 10, 2),
                          (3, "a3", 20, 3)])


class TestCostModel(unittest.TestCase):
    def test_intermediate_rows_drive_cost(self):
        query, cat = chain_query_and_catalog()
        stats = {n: cat.get(n) for n in query.relations}
        params = CostParams(mem_budget_bytes=1 << 40)  # no spill
        cheap = estimate_plan(("A", "B", "C"), query, stats, params)
        pricey = estimate_plan(("B", "C", "A"), query, stats, params)
        # Same final join size, different intermediates -> different cost.
        self.assertEqual(cheap.steps[-1].rows, pricey.steps[-1].rows)
        self.assertLess(cheap.total.hi, pricey.total.hi)
        self.assertEqual(cheap.steps[0].rows.hi, 10000)   # low sel first
        self.assertEqual(pricey.steps[0].rows.hi, 100000)

    def test_memory_and_spill_accounted(self):
        query, cat = chain_query_and_catalog()
        stats = {n: cat.get(n) for n in query.relations}
        params = CostParams(mem_budget_bytes=1 << 20, row_width=32)
        est = estimate_plan(("A", "B", "C"), query, stats, params)
        # second step: 1e6 rows * 32B = 32MB > 1MB budget -> spill charged
        self.assertGreater(est.steps[1].spilled_bytes.hi, 0)
        no_spill = estimate_plan(("A", "B", "C"), query, stats,
                                 CostParams(mem_budget_bytes=1 << 40))
        self.assertGreater(est.total.hi, no_spill.total.hi)
        self.assertEqual(no_spill.steps[1].spilled_bytes.hi, 0)

    def test_outer_join_estimate_preserves_rows(self):
        cat = Catalog()
        cat.set("A", stat(1000, {"id": 1000}))
        cat.set("B", stat(10, {"a_id": 5}))
        stats = {n: cat.get(n) for n in ("A", "B")}
        query = Query(("A", "B"), (JoinEdge("A", "B", "id", "a_id", LEFT),))
        est = estimate_plan(("A", "B"), query, stats, CostParams())
        # preserved side rows always appear at least once
        self.assertGreaterEqual(est.steps[0].rows.lo, 1000)


class TestPlanner(unittest.TestCase):
    def test_low_selectivity_first_and_exhaustive_optimality(self):
        query, cat = chain_query_and_catalog()
        plan = make_plan(query, cat, CostParams())
        self.assertEqual(plan.primary, ("A", "B", "C"))
        self.assertTrue(plan.optimal)
        self.assertEqual(plan.gap, 0)
        self.assertFalse(plan.conservative)

    def test_heuristic_never_worse_than_exhaustive(self):
        query, cat = chain_query_and_catalog()
        stats = {n: cat.get(n) for n in query.relations}
        params = CostParams()
        ranked = ranked_orders(query, stats, params)
        heur = heuristic_order(query, stats, params)
        heur_cost = estimate_plan(heur, query, stats, params).total.hi
        self.assertGreaterEqual(heur_cost, ranked[0][1].total.hi)

    def test_unknown_stats_conservative_with_alternatives(self):
        cat = Catalog()
        cat.set("A", stat(1000, {"a_key": 100}))
        cat.set("B", stat(1000, {"a_key": 100, "c_key": 100}))
        cat.set("C", RelationStat.unknown(("c_key",)))
        query = Query(("A", "B", "C"), (
            JoinEdge("A", "B", "a_key", "a_key"),
            JoinEdge("B", "C", "c_key", "c_key"),
        ))
        plan = make_plan(query, cat, CostParams())
        self.assertTrue(plan.conservative)
        self.assertGreaterEqual(len(plan.alternatives), 1)
        # worst-case ranking keeps the unknown relation last
        self.assertEqual(plan.primary, ("A", "B", "C"))
        self.assertEqual(plan.primary[-1], "C")

    def test_interval_statistics_used_in_estimates(self):
        cat = Catalog()
        cat.set("A", RelationStat(Interval(100, 200),
                                  {"k": ColumnStat(Interval(10, 50))}))
        cat.set("B", stat(100, {"k": 20}))
        query = Query(("A", "B"), (JoinEdge("A", "B", "k", "k"),))
        plan = make_plan(query, cat, CostParams())
        rows = plan.estimate.steps[0].rows
        self.assertLess(rows.lo, rows.hi)  # interval propagates


class TestExecutorReplan(unittest.TestCase):
    def test_replan_at_materialization_boundary(self):
        rels, cat, query = _replan_setup()
        params = CostParams()
        plan = make_plan(query, cat, params)
        self.assertEqual(plan.primary, ("S", "U", "R", "T"))
        result = Executor(rels, cat, params).execute(query, plan)
        self.assertEqual(result.status, Status.COMPLETED)
        replans = [d for d in result.decisions if d.kind == "replan"]
        self.assertTrue(replans)
        first = replans[0]
        # re-planning happens only at a safe materialization boundary
        self.assertEqual(first.step, 0)
        self.assertEqual(first.detail["old"], "U->R->T")
        self.assertEqual(first.detail["new"], "R->T->U")
        self.assertEqual(result.final_order, ("S", "R", "T", "U"))

    def test_replan_preserves_multiset(self):
        rels, cat, query = _replan_setup()
        params = CostParams()
        plan = make_plan(query, cat, params)
        replanned = Executor(rels, cat, params).execute(query, plan)
        baseline = Executor(rels, cat, params,
                            enable_replan=False).execute(query, plan)
        self.assertEqual(named_multiset(replanned.columns, replanned.rows),
                         named_multiset(baseline.columns, baseline.rows))
        # no output row is produced more than its multiplicity allows
        self.assertEqual(sum(replanned.multiset().values()),
                         sum(baseline.multiset().values()))

    def test_partitioned_path_on_duplicate_key_blowup(self):
        rels, cat, query = _partition_setup()
        small = CostParams(mem_budget_bytes=32 << 10)
        plan = make_plan(query, cat, small)
        result = Executor(rels, cat, small).execute(query, plan)
        self.assertEqual(result.status, Status.COMPLETED)
        kinds = [d.kind for d in result.decisions]
        self.assertIn("partition", kinds)
        self.assertGreater(result.spilled_bytes, 0)
        baseline = Executor(rels, cat,
                            CostParams(mem_budget_bytes=1 << 30),
                            enable_replan=False).execute(query, plan)
        self.assertEqual(named_multiset(result.columns, result.rows),
                         named_multiset(baseline.columns, baseline.rows))


class TestExecutorStates(unittest.TestCase):
    def test_stale_stats_detected_and_replanned(self):
        rels, cat, query = _replan_setup()
        params = CostParams()
        plan = make_plan(query, cat, params)
        cat.set("T", stat(120, {"t_key": 50}))  # statistics go stale
        result = Executor(rels, cat, params).execute(query, plan)
        self.assertTrue(result.stale_stats)
        self.assertEqual(result.status, Status.COMPLETED)
        self.assertIn("stale_stats", [d.kind for d in result.decisions])
        baseline = Executor(rels, cat, params,
                            enable_replan=False).execute(
            query, make_plan(query, cat, params))
        self.assertEqual(named_multiset(result.columns, result.rows),
                         named_multiset(baseline.columns, baseline.rows))

    def test_budget_exhausted(self):
        rels, cat, query = _replan_setup()
        params = CostParams()
        plan = make_plan(query, cat, params)
        result = Executor(rels, cat, params,
                          cost_budget=3000).execute(query, plan)
        self.assertEqual(result.status, Status.BUDGET_EXHAUSTED)
        self.assertIsNone(result.rows)
        self.assertIn("budget", [d.kind for d in result.decisions])

    def test_cancelled(self):
        rels, cat, query = _replan_setup()
        params = CostParams()
        plan = make_plan(query, cat, params)
        calls = iter(range(10))
        result = Executor(rels, cat, params,
                          cancel_check=lambda: next(calls) >= 2
                          ).execute(query, plan)
        self.assertEqual(result.status, Status.CANCELLED)
        self.assertIsNone(result.rows)
        self.assertIn("cancel", [d.kind for d in result.decisions])


class TestReplayableSamples(unittest.TestCase):
    def test_samples_are_replayable(self):
        first = run_all()
        second = run_all()
        self.assertEqual(first, second)
        self.assertIn("scenario_replan", first)
        self.assertIn("multiset identical to no-replan run: True", first)

    def test_decision_log_is_deterministic(self):
        rels, cat, query = _replan_setup()
        params = CostParams()
        plan = make_plan(query, cat, params)
        run1 = Executor(rels, cat, params).execute(query, plan)
        run2 = Executor(rels, cat, params).execute(query, plan)
        self.assertEqual(run1.decision_log(), run2.decision_log())


if __name__ == "__main__":
    unittest.main()
