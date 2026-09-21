import unittest

from coverage_ledger import Ledger, DuplicateReport, UnknownRecord, query
from coverage_ledger.store import Timeline


class TouchingEndpointTests(unittest.TestCase):
    def setUp(self):
        self.led = Ledger()
        self.led.add_membership("A", 0, None, rule_id="m")

    def test_back_to_back_reports_cover_continuously(self):
        self.led.add_observation("A", 0, 4, observation_id="p1")
        self.led.add_observation("A", 4, 8, observation_id="p2")
        result = query(self.led.snapshot(), (0, 10), "union")
        self.assertEqual(result.known_coverage_length, 8)
        # elementary segments keep the touching breakpoint, but merged
        # covered length is continuous [0,8)
        from coverage_ledger import intervals as iv
        self.assertEqual(
            iv.merge([(s.start, s.end) for s in result.covered_segments]),
            [(0, 8)],
        )

    def test_overlapping_repeat_upload_not_double_counted(self):
        self.led.add_observation("A", 0, 6, observation_id="q1")
        # same interval, new id: union stays [0,6), no extra length
        self.led.add_observation("A", 0, 6, observation_id="q2")
        result = query(self.led.snapshot(), (0, 10), "union")
        self.assertEqual(result.known_coverage_length, 6)

    def test_exact_same_id_is_rejected(self):
        self.led.add_observation("A", 0, 6, observation_id="same")
        with self.assertRaises(DuplicateReport):
            self.led.add_observation("A", 1, 3, observation_id="same")
        with self.assertRaises(UnknownRecord):
            self.led.retract_observation("missing", "A")

    def test_point_adjacency_not_overlap(self):
        timeline = Timeline()
        timeline.add("x", "A", 0, 4)
        self.assertEqual(timeline.covering("A", (4, 8)), [])
        self.assertEqual([r[0] for r in timeline.covering("A", (0, 4))], ["x"])


class LocalityTests(unittest.TestCase):
    def test_correction_only_touches_one_devices_indexes(self):
        led = Ledger()
        led.add_membership("A", 0, None, rule_id="ma")
        led.add_membership("Z", 0, None, rule_id="mz")
        for i in range(50):
            led.add_observation("Z", 100 + i, 101 + i, observation_id=f"z{i}")
        led.add_observation("A", 0, 5, observation_id="a")

        z_index = led.observations._devices["Z"]
        z_before = (list(z_index.starts), list(z_index.ends), dict(z_index.records))
        led.retract_observation("a", "A")
        led.add_observation("A", 0, 6, observation_id="a2")

        z_after = (z_index.starts, z_index.ends, z_index.records)
        self.assertEqual(z_before[0], z_after[0])
        self.assertEqual(z_before[1], z_after[1])
        self.assertEqual(z_before[2], z_after[2])

    def test_window_query_skips_disjoint_history(self):
        led = Ledger()
        led.add_membership("A", 0, None, rule_id="m")
        led.add_observation("A", 100, 200, observation_id="far")
        result = query(led.snapshot(), (0, 10), "union")
        self.assertEqual(result.known_coverage_length, 0)
        self.assertFalse(any(s.covered for s in result.segments))


if __name__ == "__main__":
    unittest.main()
