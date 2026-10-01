import unittest

from calpart import (
    CalendarSpec,
    DayGrid,
    MonthGrid,
    OffsetTable,
    WeekGrid,
    common_refinement,
    verify_segments_cover,
)
from tests.helpers import U, europe_like, wild_jump_table


class TestCommonRefinement(unittest.TestCase):
    def setUp(self):
        self.calendars = {
            "berlin": CalendarSpec(europe_like(), DayGrid()),
            "kolkata": CalendarSpec(OffsetTable.fixed(19800), DayGrid()),
        }

    def test_atomic_segments_carry_every_calendar_label(self):
        window = (U(2024, 10, 26, 12), U(2024, 10, 28, 12))
        ref = common_refinement(window, self.calendars)
        for seg in ref.segments:
            self.assertEqual(set(seg.labels), {"berlin", "kolkata"})
        verify_segments_cover(ref.segments, window)

    def test_cut_points_come_from_all_calendars(self):
        window = (U(2024, 10, 26, 12), U(2024, 10, 28, 12))
        ref = common_refinement(window, self.calendars)
        cuts = {s.start for s in ref.segments} | {s.end for s in ref.segments}
        self.assertIn(U(2024, 10, 26, 22), cuts)      # berlin midnight (CEST)
        self.assertIn(U(2024, 10, 26, 18, 30), cuts)  # kolkata midnight (+5:30)
        self.assertIn(U(2024, 10, 27, 23), cuts)      # berlin midnight after 25h day

    def test_labels_track_each_calendar(self):
        window = (U(2024, 10, 26, 12), U(2024, 10, 28, 12))
        ref = common_refinement(window, self.calendars)
        probe = next(s for s in ref.segments if s.start == U(2024, 10, 26, 22))
        self.assertEqual(probe.labels["berlin"], "2024-10-27")
        self.assertEqual(probe.labels["kolkata"], "2024-10-27")
        probe = next(s for s in ref.segments if s.start == U(2024, 10, 27, 18, 30))
        self.assertEqual(probe.labels["kolkata"], "2024-10-28")

    def test_preimages_index_reports_true_durations(self):
        window = (U(2024, 10, 26, 12), U(2024, 10, 28, 12))
        ref = common_refinement(window, self.calendars)
        pre = ref.preimage_of("berlin", "2024-10-27")
        self.assertEqual(pre, [(U(2024, 10, 26, 22), U(2024, 10, 27, 23))])
        self.assertEqual(sum(b - a for a, b in pre), 25 * 3600)

    def test_label_with_disjoint_preimages(self):
        window = (U(2024, 1, 1), U(2024, 1, 5))
        calendars = {
            "wild": CalendarSpec(wild_jump_table(), DayGrid()),
            "plain": CalendarSpec(OffsetTable.fixed(0), DayGrid()),
        }
        ref = common_refinement(window, calendars)
        pre = ref.preimage_of("wild", "2024-01-02")
        self.assertEqual(
            pre,
            [(U(2024, 1, 2), U(2024, 1, 2, 12)), (U(2024, 1, 3, 6), U(2024, 1, 4))],
        )
        jan2 = [s for s in ref.segments if s.labels["wild"] == "2024-01-02"]
        self.assertEqual(len(jan2), 2)
        self.assertLess(jan2[0].end, jan2[1].start)
        verify_segments_cover(ref.segments, window)

    def test_three_calendars_day_week_month(self):
        window = (U(2024, 12, 29), U(2025, 1, 2))
        calendars = {
            "day": CalendarSpec(OffsetTable.fixed(0), DayGrid()),
            "week": CalendarSpec(OffsetTable.fixed(0), WeekGrid(0)),
            "month": CalendarSpec(OffsetTable.fixed(0), MonthGrid()),
        }
        ref = common_refinement(window, calendars)
        verify_segments_cover(ref.segments, window)
        labels = [s.labels for s in ref.segments]
        self.assertEqual(labels[0]["week"], "week[Mon]@2024-12-23")
        self.assertEqual(labels[-1]["week"], "week[Mon]@2024-12-30")
        self.assertEqual(labels[-1]["month"], "2025-01")
        self.assertEqual(labels[0]["month"], "2024-12")
        month_cuts = [s for s in ref.segments if s.start == U(2025, 1, 1)]
        self.assertEqual(len(month_cuts), 1)


if __name__ == "__main__":
    unittest.main()
