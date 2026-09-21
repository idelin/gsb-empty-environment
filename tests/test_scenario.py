import unittest
from fractions import Fraction

from coverage_ledger import Ledger, query, InvalidWindow, revision_impact


class SpecScenarioTests(unittest.TestCase):
    def setUp(self):
        self.led = Ledger()
        self.led.add_membership("A", 0, None, rule_id="mem-A-1")
        self.led.add_membership("B", 0, None, rule_id="mem-B-1")
        self.led.add_observation("A", 0, 6, observation_id="obs-A")
        self.led.add_observation("B", 4, 8, observation_id="obs-B")

    def test_headline_rates(self):
        view = self.led.snapshot()
        union = query(view, (0, 10), "union")
        at_least_two = query(view, (0, 10), "at_least_k", k=2)
        intersection = query(view, (0, 10), "intersection")
        self.assertEqual(union.known_coverage_length, 8)
        self.assertEqual(union.window_length, 10)
        self.assertEqual(union.coverage_rate, Fraction(4, 5))
        self.assertEqual(at_least_two.known_coverage_length, 2)
        self.assertEqual(at_least_two.coverage_rate, Fraction(1, 5))
        self.assertEqual(intersection.known_coverage_length, 2)
        self.assertEqual(set(union.evidence_ids()), {"obs-A", "obs-B"})

    def test_segments_cut_on_leave_and_correction(self):
        self.led.publish_report("R", (0, 10), "union")
        published_seq = self.led.head
        self.led.end_membership("B", 5, rule_id="mem-B-2")
        self.assertEqual(query(self.led.snapshot(), (0, 10), "union").known_coverage_length, 6)
        self.led.retract_observation("obs-B", "B")
        self.led.add_observation("B", 3, 8, observation_id="obs-B-v2")

        # New state: A [0,6), B counts only as member [0,5) online [3,5)
        current = query(self.led.snapshot(), (0, 10), "union")
        self.assertEqual(current.known_coverage_length, 6)
        boundaries = [(s.start, s.end) for s in current.segments if s.length]
        self.assertEqual(boundaries, [(0, 3), (3, 5), (5, 6), (6, 8), (8, 10)])

        # Old snapshot: old records + old membership rules.
        old = self.led.snapshot(published_seq)
        self.assertEqual(query(old, (0, 10), "union").known_coverage_length, 8)
        old_boundaries = [(s.start, s.end) for s in query(old, (0, 10), "union").segments]
        self.assertEqual(old_boundaries, [(0, 4), (4, 6), (6, 8), (8, 10)])

    def test_zero_length_window_is_parameter_error(self):
        with self.assertRaises(InvalidWindow):
            query(self.led.snapshot(), (5, 5), "union")
        with self.assertRaises(InvalidWindow):
            query(self.led.snapshot(), (9, 3), "union")

    def test_reported_revision_range(self):
        self.led.end_membership("B", 5, rule_id="mem-B-2")
        leave_seq = self.led.head
        retract = self.led.retract_observation("obs-B", "B")
        impact = revision_impact(self.led, retract.seq, (0, 10))
        self.assertEqual(impact["raw_changed_ranges"], [(4, 8)])
        self.assertEqual(impact["effective_changed_ranges"], [(4, 5)])
        member_impact = revision_impact(self.led, leave_seq, (0, 10))
        self.assertEqual(member_impact["devices"], ["B"])


if __name__ == "__main__":
    unittest.main()
