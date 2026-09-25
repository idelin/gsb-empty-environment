import unittest

from evidence import (
    EMPTY_INTERSECTION,
    FEASIBLE,
    INFEASIBLE,
    MUTEX_CONFLICT,
    NOT_VISIBLE,
    NO_COVERAGE,
    SEARCH_INCOMPLETE,
    UNKNOWN,
    FactVersion,
    MutexRule,
    Query,
    common_interval,
    is_consistent,
    solve,
)

RULES = [MutexRule("mutex-vendors", 1, frozenset({"vendorA", "vendorB"}))]


def base_facts():
    return [
        FactVersion("price-vendorA", 1, "price", "vendorA", 0, 10, recorded_at=1),
        FactVersion("price-vendorB", 1, "price", "vendorB", 4, 8, recorded_at=1),
        FactVersion("rating-manual", 1, "rating", "manual", 0, 6, recorded_at=2),
        FactVersion("rating-manual", 2, "rating", "manual", 0, 9, recorded_at=7),
        FactVersion("price-archive", 1, "price", "archive", 0, 9, recorded_at=100),
    ]


class VisibilityTest(unittest.TestCase):
    def test_late_evidence_never_participates(self):
        result = solve(base_facts(), RULES, Query("q", 5, ("price", "rating")))
        refs = {ref for seg in result.segments for sel in seg.witnesses for ref in sel.refs}
        self.assertNotIn("price-archive@v1", refs)
        self.assertNotIn("rating-manual@v2", refs)
        self.assertNotIn("rating-manual@v2", refs)
        self.assertIn("rating-manual@v1", refs)

    def test_newer_version_visible_when_recorded_in_time(self):
        result = solve(base_facts(), RULES, Query("q", 9, ("price", "rating")))
        refs = {ref for seg in result.segments for sel in seg.witnesses for ref in sel.refs}
        self.assertIn("rating-manual@v2", refs)
        self.assertNotIn("rating-manual@v1", refs)

    def test_not_visible_reason_when_only_late_evidence_exists(self):
        result = solve(base_facts(), RULES, Query("q", 1.5, ("price", "rating")))
        self.assertTrue(result.segments)
        self.assertEqual([], result.feasible_segments)
        self.assertTrue(all(seg.status == INFEASIBLE for seg in result.segments))
        codes = {r.code for seg in result.segments for r in seg.reasons}
        self.assertIn(NOT_VISIBLE, codes)
        late_refs = {
            ref
            for seg in result.segments
            for r in seg.reasons
            if r.code == NOT_VISIBLE
            for ref in r.fact_refs
        }
        self.assertIn("rating-manual@v1", late_refs)


class IntervalSemanticsTest(unittest.TestCase):
    def test_touching_intervals_have_no_common_interval(self):
        left = FactVersion("a", 1, "price", "vendorA", 0, 5, recorded_at=1)
        right = FactVersion("b", 1, "rating", "manual", 5, 9, recorded_at=1)
        self.assertIsNone(common_interval((left, right)))

    def test_touching_pair_yields_no_feasible_segment(self):
        facts = [
            FactVersion("a", 1, "price", "vendorA", 0, 5, recorded_at=1),
            FactVersion("b", 1, "rating", "manual", 5, 9, recorded_at=1),
        ]
        result = solve(facts, [], Query("q", 10, ("price", "rating")))
        self.assertEqual([], result.feasible_segments)
        codes = {r.code for seg in result.segments for r in seg.reasons}
        self.assertEqual({NO_COVERAGE}, codes)

    def test_overlapping_pair_reports_maximal_common_interval(self):
        facts = [
            FactVersion("a", 1, "price", "vendorA", 0, 5, recorded_at=1),
            FactVersion("b", 1, "rating", "manual", 3, 9, recorded_at=1),
        ]
        result = solve(facts, [], Query("q", 10, ("price", "rating")))
        feasible = result.feasible_segments
        self.assertEqual(1, len(feasible))
        self.assertEqual((3, 5), (feasible[0].start, feasible[0].end))


class MutexTest(unittest.TestCase):
    def test_mutex_conflict_reason(self):
        facts = [
            FactVersion("a", 1, "price", "vendorA", 0, 10, recorded_at=1),
            FactVersion("b", 1, "rating", "vendorB", 0, 10, recorded_at=1),
        ]
        result = solve(facts, RULES, Query("q", 10, ("price", "rating")))
        self.assertEqual([], result.feasible_segments)
        codes = {r.code for seg in result.segments for r in seg.reasons}
        self.assertEqual({MUTEX_CONFLICT}, codes)
        detail = result.segments[0].reasons[0].detail
        self.assertIn("vendorA", detail)
        self.assertIn("vendorB", detail)
        self.assertIn("mutex-vendors@v1", detail)

    def test_alternatives_make_both_sides_feasible(self):
        facts = [
            FactVersion("a", 1, "price", "vendorA", 0, 5, recorded_at=1),
            FactVersion("b", 1, "price", "vendorB", 5, 9, recorded_at=1),
            FactVersion("m", 1, "rating", "manual", 0, 9, recorded_at=1),
        ]
        result = solve(facts, RULES, Query("q", 10, ("price", "rating")))
        feasible = result.feasible_segments
        self.assertEqual(1, len(feasible))
        self.assertEqual((0, 9), (feasible[0].start, feasible[0].end))
        self.assertFalse(feasible[0].covers_entire_segment)
        self.assertGreaterEqual(len(feasible[0].witnesses), 1)
        for sel in feasible[0].witnesses:
            self.assertTrue(is_consistent(sel.facts, RULES))

    def test_not_just_intersection_of_all_candidates(self):
        facts = [
            FactVersion("a", 1, "price", "vendorA", 0, 5, recorded_at=1),
            FactVersion("b", 1, "price", "vendorB", 5, 9, recorded_at=1),
            FactVersion("m", 1, "rating", "manual", 0, 9, recorded_at=1),
        ]
        naive = common_interval(facts)
        self.assertIsNone(naive)
        result = solve(facts, RULES, Query("q", 10, ("price", "rating")))
        self.assertEqual([(0, 9)], [(s.start, s.end) for s in result.feasible_segments])


class WitnessTest(unittest.TestCase):
    def test_every_feasible_segment_has_consistent_witness(self):
        result = solve(base_facts(), RULES, Query("q", 9, ("price", "rating")))
        self.assertTrue(result.feasible_segments)
        for seg in result.feasible_segments:
            self.assertGreaterEqual(len(seg.witnesses), 1)
            for sel in seg.witnesses:
                self.assertTrue(is_consistent(sel.facts, RULES))
                self.assertIsNotNone(sel.common_interval())
            if seg.covers_entire_segment:
                for sel in seg.witnesses:
                    lo, hi = sel.common_interval()
                    self.assertLessEqual(lo, seg.start)
                    self.assertGreaterEqual(hi, seg.end)

    def test_single_selection_covering_whole_segment(self):
        result = solve(base_facts(), RULES, Query("q", 9, ("price", "rating")))
        feasible = result.feasible_segments
        self.assertEqual(1, len(feasible))
        self.assertEqual((0, 9), (feasible[0].start, feasible[0].end))
        self.assertTrue(feasible[0].covers_entire_segment)
        self.assertIn(
            ("price-vendorA@v1", "rating-manual@v2"),
            [sel.refs for sel in feasible[0].witnesses],
        )


class BudgetTest(unittest.TestCase):
    FACTS = [
        FactVersion("a", 1, "price", "vendorA", 0, 5, recorded_at=1),
        FactVersion("b", 1, "price", "vendorB", 5, 9, recorded_at=1),
        FactVersion("m", 1, "rating", "manual", 0, 9, recorded_at=1),
    ]

    def test_atomic_budget_leaves_unknown_tail(self):
        result = solve(self.FACTS, RULES, Query("q", 10, ("price", "rating")),
                       max_atomic_segments=1)
        statuses = [seg.status for seg in result.segments]
        self.assertIn(FEASIBLE, statuses)
        self.assertIn(UNKNOWN, statuses)
        unknown = [s for s in result.segments if s.status == UNKNOWN]
        self.assertEqual((5, 9), (unknown[0].start, unknown[0].end))
        self.assertEqual(SEARCH_INCOMPLETE, unknown[0].reasons[0].code)

    def test_combination_budget_marks_segment_unknown(self):
        facts = [
            FactVersion(f"p{i}", 1, "price", f"sp{i}", 0, 10, recorded_at=1)
            for i in range(4)
        ] + [
            FactVersion(f"r{i}", 1, "rating", f"sr{i}", 0, 10, recorded_at=1)
            for i in range(4)
        ]
        result = solve(facts, [], Query("q", 10, ("price", "rating")), max_combinations=4)
        self.assertEqual(1, len(result.segments))
        self.assertEqual(UNKNOWN, result.segments[0].status)
        self.assertEqual(SEARCH_INCOMPLETE, result.segments[0].reasons[0].code)


class StableIdTest(unittest.TestCase):
    def test_deterministic_ids_and_steps(self):
        first = solve(base_facts(), RULES, Query("q", 9, ("price", "rating")))
        second = solve(base_facts(), RULES, Query("q", 9, ("price", "rating")))
        self.assertEqual(
            [s.segment_id for s in first.segments],
            [s.segment_id for s in second.segments],
        )
        self.assertEqual(
            [(s["step_id"], s["version"]) for s in first.journal.steps],
            [(s["step_id"], s["version"]) for s in second.journal.steps],
        )
        self.assertEqual(first.journal.digest(), second.journal.digest())

    def test_facts_rules_steps_carry_ids_and_versions(self):
        result = solve(base_facts(), RULES, Query("q", 9, ("price", "rating")))
        visibility = result.journal.steps[0]
        self.assertEqual("step-0001", visibility["step_id"])
        self.assertEqual(1, visibility["version"])
        self.assertIn("mutex-vendors@v1", visibility["payload"]["rules"])
        self.assertIn("rating-manual@v2", visibility["payload"]["visible"])
        self.assertIn("price-archive@v1", visibility["payload"]["excluded_late"])
        self.assertIn("rating-manual@v1", visibility["payload"]["superseded"])


if __name__ == "__main__":
    unittest.main()
