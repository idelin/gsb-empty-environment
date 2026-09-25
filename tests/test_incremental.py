import unittest

from evidence import (
    FEASIBLE,
    FactVersion,
    IncrementalEngine,
    MutexRule,
    Query,
    segment_signature,
    solve,
)

RULES = [MutexRule("mutex-vendors", 1, frozenset({"vendorA", "vendorB"}))]
QUERY = Query("q-inc", 9, ("price", "rating"))


def base_facts():
    return [
        FactVersion("price-vendorA", 1, "price", "vendorA", 0, 10, recorded_at=1),
        FactVersion("price-vendorB", 1, "price", "vendorB", 4, 8, recorded_at=1),
        FactVersion("rating-manual", 1, "rating", "manual", 0, 6, recorded_at=2),
        FactVersion("rating-manual", 2, "rating", "manual", 0, 9, recorded_at=7),
    ]


class IncrementalTest(unittest.TestCase):
    def setUp(self):
        self.engine = IncrementalEngine(RULES, QUERY)
        self.facts = base_facts()
        self.engine.load(self.facts)

    def test_update_undoes_only_affected_segments(self):
        before_ids = [s.segment_id for s in self.engine.result.segments]
        update = FactVersion("price-vendorA", 2, "price", "vendorA", 3, 10, recorded_at=8)
        report = self.engine.apply_update(update)
        self.assertTrue(report.changed)
        self.assertEqual((0, 10), report.affected_range)
        self.assertEqual(tuple(before_ids), report.undone_segment_ids)
        self.assertTrue(report.new_segment_ids)

    def test_incremental_result_equals_full_recompute(self):
        update = FactVersion("price-vendorA", 2, "price", "vendorA", 3, 10, recorded_at=8)
        self.engine.apply_update(update)
        full = solve(self.facts + [update], RULES, QUERY)
        self.assertEqual(
            segment_signature(full.segments),
            segment_signature(self.engine.result.segments),
        )
        feasible = self.engine.result.feasible_segments
        self.assertEqual([(3, 9)], [(s.start, s.end) for s in feasible])

    def test_partial_range_update_keeps_unaffected_segments(self):
        update = FactVersion("rating-manual", 3, "rating", "manual", 0, 4, recorded_at=8)
        report = self.engine.apply_update(update)
        self.assertEqual((0, 9), report.affected_range)
        full = solve(self.facts + [update], RULES, QUERY)
        self.assertEqual(
            segment_signature(full.segments),
            segment_signature(self.engine.result.segments),
        )

    def test_invisible_update_is_noop(self):
        before = segment_signature(self.engine.result.segments)
        late = FactVersion("price-vendorA", 2, "price", "vendorA", 0, 1, recorded_at=50)
        report = self.engine.apply_update(late)
        self.assertFalse(report.changed)
        self.assertEqual((), report.undone_segment_ids)
        self.assertEqual(before, segment_signature(self.engine.result.segments))

    def test_superseded_by_older_recorded_version_is_noop(self):
        stale = FactVersion("rating-manual", 3, "rating", "manual", 0, 1, recorded_at=3)
        report = self.engine.apply_update(stale)
        self.assertFalse(report.changed)

    def test_sequential_updates_match_full_recompute(self):
        updates = [
            FactVersion("price-vendorA", 2, "price", "vendorA", 2, 10, recorded_at=8),
            FactVersion("price-vendorB", 2, "price", "vendorB", 6, 12, recorded_at=8),
            FactVersion("rating-manual", 3, "rating", "manual", 1, 9, recorded_at=8),
        ]
        for update in updates:
            self.engine.apply_update(update)
            self.facts.append(update)
        full = solve(self.facts, RULES, QUERY)
        self.assertEqual(
            segment_signature(full.segments),
            segment_signature(self.engine.result.segments),
        )
        self.assertTrue(self.engine.replay())

    def test_new_fact_id_extends_coverage(self):
        update = FactVersion("price-vendorC", 1, "price", "vendorC", 8, 14, recorded_at=8)
        report = self.engine.apply_update(update)
        self.assertTrue(report.changed)
        full = solve(self.facts + [update], RULES, QUERY)
        self.assertEqual(
            segment_signature(full.segments),
            segment_signature(self.engine.result.segments),
        )
        feasible = self.engine.result.feasible_segments
        self.assertIn((0, 9), [(s.start, s.end) for s in feasible])


if __name__ == "__main__":
    unittest.main()
