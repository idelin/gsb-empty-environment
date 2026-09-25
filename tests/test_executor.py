import unittest
from collections import Counter

from joinplanner.cost import CostModel
from joinplanner.executor import (BUDGET_EXHAUSTED, CANCELLED, COMPLETED,
                                  REPLANNED, SPILLED, STALE_STATS, Executor)
from joinplanner.planner import JoinStep, Query
from joinplanner.relations import INNER, Relation, execute_join
from joinplanner.stats import Interval, RelStats, StatsProvider


def _chain_query():
    return Query(base="A", steps=(
        JoinStep(rel="B", join_type=INNER, edges=(("A", "k", "k"),)),
        JoinStep(rel="C", join_type=INNER, edges=(("A", "k", "k"),)),
    ))


def _chain_rels():
    return {
        "A": Relation("A", ("k", "x"), [(i, f"a{i}") for i in range(500)]),
        "B": Relation("B", ("k", "y"), [(i % 500, f"b{i}") for i in range(500)]),
        "C": Relation("C", ("k", "z"), [(i % 500, f"c{i}") for i in range(500)]),
    }


def _chain_provider(rels):
    def ps(name, cols=("k",)):
        r = rels[name]
        from joinplanner.relations import exact_ndv
        return RelStats(Interval.point(r.nrows),
                        {c: Interval.point(exact_ndv(r, c)) for c in cols})
    return StatsProvider({"A": ps("A"), "B": ps("B"), "C": ps("C")})


class ExecutionBasicsTest(unittest.TestCase):
    def test_completes_with_correct_multiset(self):
        rels = _chain_rels()
        report = Executor(rels, _chain_provider(rels), CostModel()).run(_chain_query())
        self.assertEqual(report.status, COMPLETED)
        ref = execute_join(execute_join(rels["A"], rels["B"], "k", "k"),
                           rels["C"], "k", "k")
        self.assertEqual(report.final.canonical_multiset(),
                         ref.canonical_multiset())
        self.assertEqual(Counter(report.emitted), ref.multiset())


class SpillTest(unittest.TestCase):
    def test_duplicate_keys_take_partitioned_path(self):
        from joinplanner import samples
        out = samples.sample_duplicate_key_spill()
        self.assertEqual(out["status"], COMPLETED)
        self.assertIn("SPILLED", out["states"])
        self.assertEqual(out["partitioned_stages"], 1)
        self.assertTrue(out["multiset_matches_reference"])
        self.assertTrue(out["emitted_matches_result"])


class ReplanTest(unittest.TestCase):
    def test_stale_stats_and_adaptive_flip(self):
        from joinplanner import samples
        out = samples.sample_adaptive_replan()
        self.assertEqual(out["status"], COMPLETED)
        self.assertIn(STALE_STATS, out["states"])
        self.assertIn(REPLANNED, out["states"])
        self.assertEqual(out["initial_order"], ["A", "B", "C", "D"])
        self.assertEqual(out["executed_order"], ["D", "B", "C"])
        self.assertTrue(out["multiset_matches_reference"])
        self.assertTrue(out["emitted_matches_result"])

    def test_estimate_deviation_flips_tail_only_at_boundary(self):
        from joinplanner import samples
        out = samples.sample_estimate_deviation()
        self.assertEqual(out["status"], COMPLETED)
        self.assertIn(REPLANNED, out["states"])
        self.assertNotIn(STALE_STATS, out["states"])
        self.assertTrue(out["tail_flipped"])
        self.assertEqual(out["replan_reasons"], ["estimate_deviation"])
        self.assertTrue(out["multiset_matches_reference"])

    def test_decision_log_is_recomputable(self):
        from joinplanner import samples
        out = samples.sample_estimate_deviation()
        again = samples.sample_estimate_deviation()
        self.assertEqual(out["executed_order"], again["executed_order"])
        self.assertEqual(out["replan_reasons"], again["replan_reasons"])


class TerminalStateTest(unittest.TestCase):
    def test_budget_exhausted_status(self):
        rels = _chain_rels()
        ex = Executor(rels, _chain_provider(rels), CostModel(), budget=10.0)
        report = ex.run(_chain_query())
        self.assertEqual(report.status, BUDGET_EXHAUSTED)
        self.assertIsNone(report.final)

    def test_cancelled_before_first_stage(self):
        rels = _chain_rels()
        ex = Executor(rels, _chain_provider(rels), CostModel(),
                      cancel_check=lambda: True)
        report = ex.run(_chain_query())
        self.assertEqual(report.status, CANCELLED)
        self.assertEqual(report.emitted, [])

    def test_cancelled_during_emit_emits_no_duplicate(self):
        rels = _chain_rels()
        calls = {"n": 0}

        def cancel():
            calls["n"] += 1
            # Two stage boundaries first, then allow one emitted chunk
            # before cancelling mid-emit.
            return calls["n"] > 3

        ex = Executor(rels, _chain_provider(rels), CostModel(),
                      chunk_size=100, cancel_check=cancel)
        report = ex.run(_chain_query())
        self.assertEqual(report.status, CANCELLED)
        ref = execute_join(execute_join(rels["A"], rels["B"], "k", "k"),
                           rels["C"], "k", "k")
        emitted = Counter(report.emitted)
        self.assertTrue(emitted)
        for row, count in emitted.items():
            self.assertLessEqual(count, ref.multiset()[row])

    def test_stale_stats_state(self):
        rels = _chain_rels()
        provider = _chain_provider(rels)
        ex = Executor(rels, provider, CostModel(),
                      boundary_hook=provider.bump_epoch)
        report = ex.run(_chain_query())
        self.assertEqual(report.status, COMPLETED)
        self.assertIn(STALE_STATS, report.states)

    def test_spill_state_flag(self):
        from joinplanner import samples
        out = samples.sample_duplicate_key_spill()
        self.assertIn(SPILLED, out["states"])


if __name__ == "__main__":
    unittest.main()
