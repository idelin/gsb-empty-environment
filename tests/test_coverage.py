"""unittest suite for the evidence coverage engine. Python 3.11, stdlib only."""

from __future__ import annotations

import unittest

from evcov.model import CategoryRequirement, EvidenceItem, Rule
from evcov.pipeline import replay, run_pipeline
from evcov.subset import minimal_subset
from evcov.sweep import Interval, compute_coverage


def make_item(item_id, source_id, categories, start, end, **kw):
    return EvidenceItem(
        id=item_id,
        source_id=source_id,
        categories=frozenset(categories),
        valid_from=start,
        valid_until=end,
        **kw,
    )


def make_rule(reqs, start=0, end=100):
    return Rule(
        id="r1",
        version=1,
        requirements=tuple(CategoryRequirement(c, k) for c, k in reqs),
        horizon_start=start,
        horizon_end=end,
    )


class SameSourceDedupTest(unittest.TestCase):
    """两条同源证据只算一个独立来源。"""

    def test_two_items_one_source_count_as_one(self):
        rule = make_rule([("telemetry", 2)])
        items = [
            make_item("e1", "s1", {"telemetry"}, 0, 100),
            make_item("e2", "s1", {"telemetry"}, 0, 100),
        ]
        self.assertEqual(compute_coverage(rule, items).satisfied, ())

    def test_second_independent_source_satisfies(self):
        rule = make_rule([("telemetry", 2)])
        items = [
            make_item("e1", "s1", {"telemetry"}, 0, 100),
            make_item("e2", "s1", {"telemetry"}, 0, 100),
            make_item("e3", "s2", {"telemetry"}, 0, 100),
        ]
        cov = compute_coverage(rule, items)
        self.assertEqual(cov.satisfied, (Interval(0, 100),))


class SweepTest(unittest.TestCase):
    """区间并集、恰接边界、过期证据。"""

    def test_touching_boundaries_merge_without_gap(self):
        rule = make_rule([("a", 1)])
        items = [
            make_item("e1", "s1", {"a"}, 0, 50),
            make_item("e2", "s2", {"a"}, 50, 100),
        ]
        cov = compute_coverage(rule, items)
        self.assertEqual(cov.satisfied, (Interval(0, 100),))
        self.assertEqual(cov.gaps, ())
        self.assertEqual(cov.longest, Interval(0, 100))

    def test_expired_evidence_does_not_count(self):
        rule = make_rule([("a", 1)])
        items = [make_item("e1", "s1", {"a"}, 0, 30)]
        cov = compute_coverage(rule, items)
        self.assertEqual(cov.satisfied, (Interval(0, 30),))
        self.assertEqual(cov.gaps, (Interval(30, 100),))

    def test_longest_of_several_satisfied_intervals(self):
        rule = make_rule([("a", 1)], start=0, end=100)
        items = [
            make_item("e1", "s1", {"a"}, 0, 20),
            make_item("e2", "s2", {"a"}, 50, 95),
        ]
        cov = compute_coverage(rule, items)
        self.assertEqual(cov.satisfied, (Interval(0, 20), Interval(50, 95)))
        self.assertEqual(cov.gaps, (Interval(20, 50), Interval(95, 100)))
        self.assertEqual(cov.longest, Interval(50, 95))


class CrossCategoryExclusivityTest(unittest.TestCase):
    """跨类别来源互斥:一个来源不能同时充数两个类别。"""

    def test_single_source_cannot_serve_two_categories(self):
        rule = make_rule([("a", 1), ("b", 1)])
        items = [make_item("e1", "s1", {"a", "b"}, 0, 100)]
        self.assertEqual(compute_coverage(rule, items).satisfied, ())

    def test_distinct_sources_satisfy_jointly(self):
        rule = make_rule([("a", 1), ("b", 1)])
        items = [
            make_item("e1", "s1", {"a", "b"}, 0, 100),
            make_item("e2", "s2", {"b"}, 0, 100),
        ]
        cov = compute_coverage(rule, items)
        self.assertEqual(cov.satisfied, (Interval(0, 100),))


class RetractionTest(unittest.TestCase):
    """撤回最后支持者,满足区间准确收缩。"""

    def test_retraction_shrinks_interval(self):
        rule = make_rule([("a", 2)])
        e1 = make_item("e1", "s1", {"a"}, 0, 100)
        e2 = make_item("e2", "s2", {"a"}, 0, 60)
        cov = compute_coverage(rule, [e1, e2])
        self.assertEqual(cov.satisfied, (Interval(0, 60),))
        self.assertEqual(cov.gaps, (Interval(60, 100),))

        retracted = [e1, e2.retract()]
        self.assertEqual(retracted[1].version, e2.version + 1)
        cov2 = compute_coverage(rule, retracted)
        self.assertEqual(cov2.satisfied, ())
        self.assertEqual(cov2.gaps, (Interval(0, 100),))
        self.assertIsNone(cov2.longest)


class MinimalSubsetTest(unittest.TestCase):
    """小集合穷举证明最小性;同源冗余不进入最小子集。"""

    def build(self):
        rule = make_rule([("telemetry", 2), ("audit", 1)])
        items = [
            make_item("t1", "s1", {"telemetry"}, 0, 100),
            make_item("t2", "s1", {"telemetry"}, 0, 100),  # 同源冗余
            make_item("t3", "s2", {"telemetry"}, 0, 100),
            make_item("a1", "s3", {"audit"}, 0, 50),
            make_item("a2", "s4", {"audit"}, 50, 100),
        ]
        return rule, items

    def test_exhaustive_proves_minimality(self):
        rule, items = self.build()
        res = minimal_subset(rule, items, budget=4096)
        self.assertTrue(res.feasible)
        self.assertTrue(res.optimality_proven)
        self.assertEqual(res.witness_size, 4)
        self.assertEqual(res.witness, ("a1", "a2", "t1", "t3"))

    def test_infeasible_when_sources_missing(self):
        rule = make_rule([("a", 2)])
        items = [make_item("e1", "s1", {"a"}, 0, 100)]
        res = minimal_subset(rule, items)
        self.assertFalse(res.feasible)
        self.assertIsNone(res.witness)
        self.assertTrue(res.optimality_proven)


class BudgetTest(unittest.TestCase):
    """预算不足:可行见证与未证最优分开报告。"""

    def test_budget_insufficient_reports_witness_without_optimality(self):
        rule = make_rule([("a", 1)], start=0, end=10)
        items = [make_item(f"e{i}", f"s{i}", {"a"}, 0, 10) for i in range(20)]
        res = minimal_subset(rule, items, budget=100)
        self.assertTrue(res.feasible)
        self.assertFalse(res.optimality_proven)
        self.assertEqual(res.witness_size, 1)
        self.assertEqual(len(res.witness), 1)


class ReplayTest(unittest.TestCase):
    """事实、规则、步骤带稳定 ID 与版本,计算可重放。"""

    def test_replay_is_deterministic(self):
        rule = make_rule([("a", 1)])
        items = [make_item("e1", "s1", {"a"}, 0, 100)]
        rep1 = run_pipeline(rule, items)
        rep2 = run_pipeline(rule, items)
        self.assertEqual(rep1.steps, rep2.steps)
        self.assertTrue(replay(rep1, rule, items))
        kinds = [s.kind for s in rep1.steps]
        self.assertEqual(kinds, ["coverage", "subset"])
        self.assertTrue(all(s.step_id and s.version >= 1 for s in rep1.steps))

    def test_replay_detects_tampered_facts(self):
        rule = make_rule([("a", 1)])
        items = [make_item("e1", "s1", {"a"}, 0, 100)]
        report = run_pipeline(rule, items)
        tampered = [make_item("e1", "s1", {"a"}, 0, 60)]
        self.assertFalse(replay(report, rule, tampered))


if __name__ == "__main__":
    unittest.main()
