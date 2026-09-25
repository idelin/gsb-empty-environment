import unittest

from timemap import Kind, PiecewiseTimeMap


class TestTimeMap(unittest.TestCase):
    def test_forward_slope(self):
        tm = PiecewiseTimeMap([(0.0, 0.0), (2.0, 4.0)])
        self.assertEqual(tm.source_at(1.0), 2.0)
        self.assertEqual(tm.kind_at(1.0), Kind.FORWARD)
        self.assertEqual(tm.segments[0].slope, 2.0)

    def test_speed_2_halves_duration(self):
        # Source spans 0..100 (in source-time units); slope 2 means the
        # output only needs half the time to cover the same source span.
        fast = PiecewiseTimeMap([(0.0, 0.0), (50.0, 100.0)])
        normal = PiecewiseTimeMap([(0.0, 0.0), (100.0, 100.0)])
        self.assertEqual(fast.out_end, normal.out_end / 2.0)
        self.assertEqual(fast.source_at(fast.out_end), 100.0)

    def test_reverse_segment(self):
        tm = PiecewiseTimeMap([(0.0, 10.0), (1.0, 0.0)])
        self.assertEqual(tm.kind_at(0.5), Kind.REVERSE)
        self.assertLess(tm.source_at(0.75), tm.source_at(0.25))

    def test_freeze_segment_holds_position(self):
        tm = PiecewiseTimeMap([(0.0, 0.0), (1.0, 5.0), (2.0, 5.0), (3.0, 9.0)])
        self.assertEqual(tm.kind_at(1.5), Kind.FREEZE)
        self.assertEqual(tm.source_at(1.0), 5.0)
        self.assertEqual(tm.source_at(1.999999), 5.0)

    def test_boundary_belongs_to_next_segment(self):
        # Forward then reverse meeting at the turning point src=100 at t=1.
        tm = PiecewiseTimeMap([(0.0, 0.0), (1.0, 100.0), (2.0, 0.0)])
        self.assertEqual(tm.segment_index(1.0), 1)  # owned by reverse leg
        self.assertEqual(tm.kind_at(1.0), Kind.REVERSE)
        self.assertEqual(tm.source_at(1.0), 100.0)

    def test_turning_point_not_replayed_twice(self):
        # Sample the map on an output grid aligned with the turning point:
        # source position 100 must occur exactly once.
        tm = PiecewiseTimeMap([(0.0, 0.0), (1.0, 100.0), (2.0, 0.0)])
        sr = 100
        positions = [tm.source_at(i / sr) for i in range(2 * sr + 1)]
        hits = sum(1 for p in positions if round(p) == 100)
        self.assertEqual(hits, 1)

    def test_segment_repeat_keeps_source_identity(self):
        # The same source region 0..10 is played twice; both output copies
        # map back to the same source positions.
        tm = PiecewiseTimeMap([(0.0, 0.0), (1.0, 10.0), (2.0, 0.0), (3.0, 10.0)])
        self.assertEqual(tm.source_at(0.5), tm.source_at(2.5))

    def test_invalid_control_points_rejected(self):
        with self.assertRaises(ValueError):
            PiecewiseTimeMap([(0.0, 0.0)])
        with self.assertRaises(ValueError):
            PiecewiseTimeMap([(0.0, 0.0), (0.0, 1.0)])  # not increasing
        with self.assertRaises(ValueError):
            PiecewiseTimeMap([(1.0, 0.0), (0.5, 1.0)])


if __name__ == "__main__":
    unittest.main()
