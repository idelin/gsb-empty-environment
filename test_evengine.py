"""Unit tests for the evidence coverage engine (synthetic data only)."""
import unittest

from evengine import (
    CategoryRequirement,
    Evidence,
    Rule,
    digest,
    evaluate,
    minimal_subset,
    replay,
    satisfied_at,
    sources_by_category,
)
from sample_data import STEPS


def ev(eid, src, cat, start, end, expires=None):
    return Evidence(eid, 1, src, cat, start, end, expires)


LAB2_FIELD1 = Rule(
    "RULE-T", 1,
    (CategoryRequirement("lab", 2), CategoryRequirement("field", 1)),
    exclusive_sources=True,
)


class SourceCountingTest(unittest.TestCase):
    def test_same_source_counts_once(self):
        # Two evidence items from one source = one independent source.
        items = [ev("E1", "S-A", "lab", 0, 10), ev("E2", "S-A", "lab", 0, 10)]
        self.assertEqual(sources_by_category(items, 5), {"lab": {"S-A"}})

    def test_two_same_source_items_do_not_meet_k2(self):
        rule = Rule("R", 1, (CategoryRequirement("lab", 2),))
        items = [ev("E1", "S-A", "lab", 0, 10), ev("E2", "S-A", "lab", 0, 10)]
        self.assertFalse(satisfied_at(rule, items, 5))
        items.append(ev("E3", "S-B", "lab", 0, 10))
        self.assertTrue(satisfied_at(rule, items, 5))

    def test_expired_evidence_not_counted(self):
        rule = Rule("R", 1, (CategoryRequirement("lab", 1),))
        items = [ev("E1", "S-A", "lab", 0, 30, expires=15)]
        self.assertTrue(satisfied_at(rule, items, 14))
        self.assertFalse(satisfied_at(rule, items, 15))
        result = evaluate(rule, items, (0, 30))
        self.assertEqual(result.satisfied, ((0, 15),))
        self.assertEqual(result.gaps, ((15, 30),))


class IntervalUnionTest(unittest.TestCase):
    def test_touching_boundaries_leave_no_gap(self):
        rule = Rule("R", 1, (CategoryRequirement("lab", 1),))
        items = [ev("E1", "S-A", "lab", 0, 5), ev("E2", "S-A", "lab", 5, 10)]
        result = evaluate(rule, items, (0, 10))
        self.assertEqual(result.satisfied, ((0, 10),))
        self.assertEqual(result.gaps, ())

    def test_union_before_coverage(self):
        # One source's touching items union to [0, 30); a second source
        # covers only [10, 20); k=2 holds exactly on the overlap.
        rule = Rule("R", 1, (CategoryRequirement("lab", 2),))
        items = [
            ev("E1", "S-A", "lab", 0, 20), ev("E2", "S-A", "lab", 20, 30),
            ev("E3", "S-B", "lab", 10, 20),
        ]
        result = evaluate(rule, items, (0, 30))
        self.assertEqual(result.satisfied, ((10, 20),))
        self.assertEqual(result.gaps, ((0, 10), (20, 30)))

    def test_real_gap_reported(self):
        rule = Rule("R", 1, (CategoryRequirement("lab", 1),))
        items = [ev("E1", "S-A", "lab", 0, 4), ev("E2", "S-A", "lab", 7, 10)]
        result = evaluate(rule, items, (0, 10))
        self.assertEqual(result.gaps, ((4, 7),))


class ExclusivityTest(unittest.TestCase):
    def test_source_cannot_serve_two_categories(self):
        # S-A has both lab and field evidence, but exclusivity plus the
        # joint constraint means lab still needs a second source.
        items = [
            ev("E1", "S-A", "lab", 0, 10),
            ev("E2", "S-A", "field", 0, 10),
        ]
        self.assertFalse(satisfied_at(LAB2_FIELD1, items, 5))
        # Adding a second lab source is still not enough: S-A is spent on
        # lab, so field has no exclusive source left (joint needs 3 units).
        items.append(ev("E3", "S-B", "lab", 0, 10))
        self.assertFalse(satisfied_at(LAB2_FIELD1, items, 5))
        # A dedicated field source completes the joint assignment.
        items.append(ev("E4", "S-C", "field", 0, 10))
        self.assertTrue(satisfied_at(LAB2_FIELD1, items, 5))

    def test_joint_constraint_not_per_category_count(self):
        # Per-category counts look fine (lab: {S-A, S-B}, field: {S-B}),
        # but S-B cannot count twice, so the joint constraint fails.
        items = [
            ev("E1", "S-A", "lab", 0, 10),
            ev("E2", "S-B", "lab", 0, 10),
            ev("E3", "S-B", "field", 0, 10),
        ]
        self.assertFalse(satisfied_at(LAB2_FIELD1, items, 5))
        relaxed = Rule("R2", 1, LAB2_FIELD1.requirements, exclusive_sources=False)
        self.assertTrue(satisfied_at(relaxed, items, 5))


class LongestAndRetractionTest(unittest.TestCase):
    def test_longest_interval(self):
        rule = Rule("R", 1, (CategoryRequirement("lab", 1),))
        items = [
            ev("E1", "S-A", "lab", 0, 3),
            ev("E2", "S-B", "lab", 5, 20),
        ]
        result = evaluate(rule, items, (0, 25))
        self.assertEqual(result.longest, (5, 20))

    def test_retracting_last_supporter_shrinks_interval(self):
        rule = Rule("R", 1, (CategoryRequirement("lab", 2),))
        items = [
            ev("E1", "S-A", "lab", 0, 30),
            ev("E2", "S-B", "lab", 5, 25),
            ev("E3", "S-C", "lab", 10, 30, expires=15),
        ]
        before = evaluate(rule, items, (0, 30))
        self.assertEqual(before.satisfied, ((5, 25),))
        # Retract S-B: only S-C's live window [10, 15) keeps k=2.
        after = evaluate(rule, [items[0], items[2]], (0, 30))
        self.assertEqual(after.satisfied, ((10, 15),))
        self.assertEqual(after.gaps, ((0, 10), (15, 30)))


class MinimalSubsetTest(unittest.TestCase):
    def setUp(self):
        self.items = [
            ev("EV-01", "S-A", "lab", 0, 20), ev("EV-02", "S-A", "lab", 20, 30),
            ev("EV-03", "S-B", "lab", 5, 25), ev("EV-04", "S-B", "lab", 25, 30),
            ev("EV-05", "S-C", "lab", 10, 30, expires=15),
            ev("EV-06", "S-D", "field", 0, 30),
            ev("EV-07", "S-B", "field", 0, 30),
        ]

    def test_exhaustive_proves_minimum(self):
        result = minimal_subset(LAB2_FIELD1, self.items, (5, 30))
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.optimal_size, 5)
        self.assertEqual(
            set(result.optimal), {"EV-01", "EV-02", "EV-03", "EV-04", "EV-06"}
        )

    def test_infeasible_target(self):
        # k=2 lab cannot hold on [0, 5): only S-A is present.
        result = minimal_subset(LAB2_FIELD1, self.items, (0, 5))
        self.assertFalse(result.feasible)
        self.assertIsNone(result.optimal)

    def test_budget_shortfall_reports_witness_not_optimum(self):
        result = minimal_subset(LAB2_FIELD1, self.items, (5, 30), budget=10)
        self.assertFalse(result.proven_optimal)
        self.assertIsNone(result.optimal)
        self.assertTrue(result.feasible)
        self.assertIsNotNone(result.witness)
        # The witness is feasible: replay it through the engine.
        chosen = [x for x in self.items if x.evidence_id in result.witness]
        evaluation = evaluate(LAB2_FIELD1, chosen, (5, 30))
        self.assertEqual(evaluation.satisfied, ((5, 30),))


class ReplayTest(unittest.TestCase):
    def test_sample_scenario_outcome(self):
        outcome = replay(STEPS)
        first, second = outcome["evaluations"]
        self.assertEqual(first["satisfied"], [[5, 30]])
        self.assertEqual(first["gaps"], [[0, 5]])
        self.assertEqual(first["longest"], [5, 30])
        # After retracting S-B's lab evidence, only S-C's live window works.
        self.assertEqual(second["satisfied"], [[10, 15]])
        report = outcome["subset_reports"][0]
        self.assertTrue(report["proven_optimal"])
        self.assertEqual(report["optimal_size"], 5)

    def test_replay_is_deterministic(self):
        self.assertEqual(digest(replay(STEPS)), digest(replay(STEPS)))

    def test_step_ids_and_versions_propagate(self):
        outcome = replay(STEPS)
        self.assertEqual(outcome["evaluations"][0]["step_id"], "STEP-009")
        self.assertEqual(outcome["evaluations"][0]["step_version"], 1)
        self.assertEqual(outcome["rules"]["RULE-001"]["version"], 1)


if __name__ == "__main__":
    unittest.main()
