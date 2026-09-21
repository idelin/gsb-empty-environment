import unittest

from occupancy_peak import OccupancyTracker, Status


def seg_tuples(result):
    return [(s.start, s.end, s.ids) for s in result.segments]


class PairingTest(unittest.TestCase):
    def test_end_before_start_pairs_later(self):
        t = OccupancyTracker()
        o1 = t.record_end("B", 9)
        self.assertEqual(o1.status, Status.RECORDED)
        self.assertEqual(t.orphans(), {"B": {"start": None, "end": 9}})
        self.assertEqual(t.intervals(), {})
        o2 = t.record_start("B", 5)
        self.assertEqual(o2.status, Status.PAIRED)
        self.assertEqual(t.interval("B"), (5, 9))
        self.assertEqual(t.orphans(), {})

    def test_duplicate_end_is_idempotent(self):
        t = OccupancyTracker()
        t.record_start("C", 4)
        self.assertEqual(t.record_end("C", 6).status, Status.PAIRED)
        o = t.record_end("C", 6)
        self.assertEqual(o.status, Status.DUPLICATE)
        self.assertEqual(t.interval("C"), (4, 6))
        self.assertEqual(t.level_at(5), 1)

    def test_orphan_never_creates_fake_occupancy(self):
        t = OccupancyTracker()
        t.record_end("E", 9)   # 只有 end：不得补零成 [0, 9)
        t.record_start("F", 3)  # 只有 start：不得补零成 [3, 0) 负占用
        r = t.peak(0, 100)
        self.assertEqual(r.peak, 0)
        self.assertEqual(r.segments, ())
        self.assertEqual(set(t.orphans()), {"E", "F"})

    def test_zero_length_rejected(self):
        t = OccupancyTracker()
        t.record_start("Z", 3)
        o = t.record_end("Z", 3)
        self.assertEqual(o.status, Status.REJECTED_INVALID)
        self.assertIsNone(t.interval("Z"))


class PeakTest(unittest.TestCase):
    def test_half_open_boundary_no_overlap(self):
        t = OccupancyTracker()
        t.record_start("A", 0)
        t.record_end("A", 5)
        t.record_start("B", 5)
        t.record_end("B", 9)
        r = t.peak(0, 10)
        self.assertEqual(r.peak, 1)
        self.assertEqual(seg_tuples(r), [(0, 9, frozenset({"A", "B"}))])
        self.assertEqual(t.level_at(5), 1)  # 交界处不重叠

    def test_overlap_then_retract_restores(self):
        t = OccupancyTracker()
        for oid, s, e in [("A", 0, 5), ("B", 5, 9)]:
            t.record_start(oid, s)
            t.record_end(oid, e)
        t.record_start("C", 4)
        t.record_end("C", 6)
        r = t.peak(0, 10)
        self.assertEqual(r.peak, 2)
        self.assertEqual(seg_tuples(r), [(4, 6, frozenset({"A", "B", "C"}))])
        self.assertEqual(t.retract("C").status, Status.RETRACTED)
        r = t.peak(0, 10)
        self.assertEqual(r.peak, 1)
        self.assertEqual(seg_tuples(r), [(0, 9, frozenset({"A", "B"}))])

    def test_multiple_peak_plateaus(self):
        t = OccupancyTracker()
        for oid, s, e in [("A", 0, 4), ("B", 1, 3), ("C", 6, 10), ("D", 7, 9)]:
            t.record_start(oid, s)
            t.record_end(oid, e)
        r = t.peak(0, 10)
        self.assertEqual(r.peak, 2)
        self.assertEqual(
            seg_tuples(r),
            [(1, 3, frozenset({"A", "B"})), (7, 9, frozenset({"C", "D"}))],
        )

    def test_window_clips_segments(self):
        t = OccupancyTracker()
        t.record_start("A", 0)
        t.record_end("A", 10)
        r = t.peak(3, 7)
        self.assertEqual(r.peak, 1)
        self.assertEqual(seg_tuples(r), [(3, 7, frozenset({"A"}))])

    def test_empty_window_peak_zero(self):
        t = OccupancyTracker()
        r = t.peak(0, 10)
        self.assertEqual(r.peak, 0)
        self.assertEqual(r.segments, ())

    def test_invalid_window_raises(self):
        t = OccupancyTracker()
        with self.assertRaises(ValueError):
            t.peak(5, 5)


class CorrectionAndRetractTest(unittest.TestCase):
    def _ab(self):
        t = OccupancyTracker()
        for oid, s, e in [("A", 0, 5), ("B", 5, 9)]:
            t.record_start(oid, s)
            t.record_end(oid, e)
        return t

    def test_correction_moves_interval_out_of_range(self):
        t = self._ab()
        self.assertEqual(t.record_end("B", 15).status, Status.CORRECTED)
        self.assertEqual(t.record_start("B", 12).status, Status.CORRECTED)
        self.assertEqual(t.interval("B"), (12, 15))
        r = t.peak(0, 10)
        self.assertEqual(r.peak, 1)
        # 旧贡献全部撤销：窗口内只剩 A 的 [0,5)
        self.assertEqual(seg_tuples(r), [(0, 5, frozenset({"A"}))])
        self.assertEqual(t.level_at(7), 0)
        r2 = t.peak(10, 20)
        self.assertEqual(seg_tuples(r2), [(12, 15, frozenset({"B"}))])

    def test_invalid_end_preserves_state(self):
        t = self._ab()
        before = t.peak(0, 10)
        o = t.record_end("B", 2)  # end 早于 start=5
        self.assertEqual(o.status, Status.REJECTED_INVALID)
        self.assertEqual(t.interval("B"), (5, 9))
        self.assertEqual(t.peak(0, 10), before)

    def test_invalid_start_correction_rejected(self):
        t = self._ab()
        o = t.record_start("B", 20)  # start 晚于 end=9
        self.assertEqual(o.status, Status.REJECTED_INVALID)
        self.assertEqual(t.interval("B"), (5, 9))

    def test_valid_endpoint_correction_updates_index(self):
        t = self._ab()
        o = t.record_end("A", 8)  # [0,5) -> [0,8)，与 B 重叠
        self.assertEqual(o.status, Status.CORRECTED)
        r = t.peak(0, 10)
        self.assertEqual(r.peak, 2)
        self.assertEqual(seg_tuples(r), [(5, 8, frozenset({"A", "B"}))])

    def test_retract_unknown_is_not_found(self):
        t = OccupancyTracker()
        self.assertEqual(t.retract("ghost").status, Status.NOT_FOUND)

    def test_retract_orphan(self):
        t = OccupancyTracker()
        t.record_end("E", 9)
        self.assertEqual(t.retract("E").status, Status.RETRACTED)
        self.assertEqual(t.orphans(), {})


if __name__ == "__main__":
    unittest.main()
