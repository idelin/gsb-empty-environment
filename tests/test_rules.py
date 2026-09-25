import datetime as dt
import unittest

from recur import (
    Ambiguous,
    DailyRule,
    Gap,
    Instant,
    MonthlyRule,
    Schedule,
    STD,
    WallTime,
    WeeklyRule,
    Window,
    nth_event,
    make_table,
)
from tests._oracle import brute_force_days


def wall_instant(y, m, d, hh=0, mm=0):
    return Instant(WallTime.from_parts(y, m, d, hh, mm).epoch_us - STD)


class RuleExpansionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_table()

    def _assert_matches_oracle(self, schedule, window):
        expansion = schedule.expand(window)
        reference, scanned = brute_force_days(schedule, window)
        got = [(e.instant.epoch_us, e.wall, e.kind) for e in expansion.events]
        ref = [(e.instant.epoch_us, e.wall, e.kind) for e in reference]
        self.assertEqual(got, ref)
        return expansion, scanned

    def test_daily_matches_day_by_day_oracle(self):
        sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 8, 30), interval=3), self.table
        )
        window = Window(wall_instant(2024, 1, 1), wall_instant(2024, 3, 1))
        expansion, scanned = self._assert_matches_oracle(sched, window)
        self.assertEqual(len(expansion.events), 20)
        self.assertLess(expansion.jumps, scanned)

    def test_weekly_matches_oracle(self):
        sched = Schedule(
            WeeklyRule(
                WallTime.from_parts(2024, 1, 1, 9),
                by_weekday=(0, 2, 4),  # Mon/Wed/Fri
                interval=2,
            ),
            self.table,
        )
        window = Window(wall_instant(2024, 1, 1), wall_instant(2024, 4, 1))
        expansion, scanned = self._assert_matches_oracle(sched, window)
        self.assertLess(expansion.jumps, scanned // 5)

    def test_monthly_day_31_skips_short_months(self):
        sched = Schedule(
            MonthlyRule(
                WallTime.from_parts(2024, 1, 31, 10), by_monthday=(31,)
            ),
            self.table,
        )
        window = Window(wall_instant(2024, 1, 1), wall_instant(2025, 1, 1))
        expansion, _ = self._assert_matches_oracle(sched, window)
        months = {(e.wall.year, e.wall.month) for e in expansion.events}
        self.assertEqual(
            months,
            {(2024, 1), (2024, 3), (2024, 5), (2024, 7), (2024, 8), (2024, 10), (2024, 12)},
        )
        self.assertNotIn((2024, 2), months)
        # 13 month-period jumps, not 366 day scans.
        self.assertEqual(expansion.jumps, 13)

    def test_monthly_interval_advances_by_months_not_seconds(self):
        sched = Schedule(
            MonthlyRule(
                WallTime.from_parts(2024, 1, 15, 10), by_monthday=(15,), interval=2
            ),
            self.table,
        )
        window = Window(wall_instant(2024, 1, 1), wall_instant(2025, 1, 1))
        expansion, _ = self._assert_matches_oracle(sched, window)
        self.assertEqual(
            [(e.wall.year, e.wall.month) for e in expansion.events],
            [(2024, 1), (2024, 3), (2024, 5), (2024, 7), (2024, 9), (2024, 11)],
        )

    def test_exclusion_changes_count_and_ranking(self):
        sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9)),
            self.table,
            exdates={dt.date(2024, 1, 2), dt.date(2024, 1, 3)},
        )
        window = Window(wall_instant(2024, 1, 1), wall_instant(2024, 1, 10))
        events = sched.expand(window).events
        self.assertEqual(len(events), 7)  # 9 days minus 2 exclusions
        # The 2nd occurrence in rank is now Jan 4, not Jan 2.
        self.assertEqual(nth_event(sched, 2).wall.date, dt.date(2024, 1, 4))

    def test_exclusion_beats_same_day_extra(self):
        sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9)),
            self.table,
            exdates={dt.date(2024, 1, 2)},
            rdates=[WallTime.from_parts(2024, 1, 2, 15)],
        )
        window = Window(wall_instant(2024, 1, 1), wall_instant(2024, 1, 5))
        self._assert_matches_oracle(sched, window)
        days = {e.wall.date for e in sched.expand(window).events}
        self.assertNotIn(dt.date(2024, 1, 2), days)

    def test_extra_date_adds_occurrence(self):
        sched = Schedule(
            WeeklyRule(WallTime.from_parts(2024, 1, 1, 9), by_weekday=(0,)),
            self.table,
            rdates=[WallTime.from_parts(2024, 1, 17, 12)],  # a Wednesday
        )
        window = Window(wall_instant(2024, 1, 1), wall_instant(2024, 1, 22))
        expansion, _ = self._assert_matches_oracle(sched, window)
        self.assertTrue(any(e.kind == "extra" for e in expansion.events))

    def test_window_is_half_open(self):
        sched = Schedule(DailyRule(WallTime.from_parts(2024, 1, 1, 9)), self.table)
        start = wall_instant(2024, 1, 1, 9)  # included
        end = wall_instant(2024, 1, 3, 9)    # excluded
        events = sched.expand(Window(start, end)).events
        self.assertEqual([e.wall.date for e in events], [dt.date(2024, 1, 1), dt.date(2024, 1, 2)])

    def test_duplicate_instants_deduplicated(self):
        # Two rules coinciding on the time line collapse to one event.
        a = Schedule(DailyRule(WallTime.from_parts(2024, 1, 1, 9), name="a"), self.table)
        b = Schedule(DailyRule(WallTime.from_parts(2024, 1, 1, 9), name="b"), self.table)
        from recur import union

        events = union([a, b]).expand(
            Window(wall_instant(2024, 1, 1), wall_instant(2024, 1, 8))
        ).events
        self.assertEqual(len(events), 7)

    def test_gap_day_uses_forward_policy_in_expansion(self):
        sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 3, 8, 2, 30)),
            self.table,
            gap=Gap.FORWARD,
        )
        window = Window(wall_instant(2024, 3, 9), wall_instant(2024, 3, 11))
        events = sched.expand(window).events
        mapped = {e.wall.date: e for e in events}
        # Mar 10 02:30 is skipped; it maps forward to 03:00 the same day.
        self.assertIn(dt.date(2024, 3, 10), mapped)
        event = mapped[dt.date(2024, 3, 10)]
        self.assertEqual(event.wall.hour, 2)       # nominal scheduled time
        self.assertEqual(event.mapped_wall.hour, 3)  # after forward jump
        self.assertEqual(event.instant.isoformat_utc(), "2024-03-09T18:00:00Z")

    def test_fold_day_can_emit_two_occurrences(self):
        sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 11, 1, 1, 30)),
            self.table,
            ambiguous=Ambiguous.BOTH,
        )
        window = Window(wall_instant(2024, 11, 2), wall_instant(2024, 11, 4))
        events = sched.expand(window).events
        nov3 = [e for e in events if e.wall.date == dt.date(2024, 11, 3)]
        self.assertEqual(len(nov3), 2)


class LargeSpanJumpTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_table()

    def test_decade_query_does_not_day_scan(self):
        # (rule, upper bound on jumps).  Daily interval=1 still jumps day by
        # day by construction (bounded by span length); coarser rules must
        # jump far fewer times than the number of days.
        window = Window(wall_instant(2024, 1, 1), wall_instant(2034, 1, 1))
        days = (dt.date(2034, 1, 1) - dt.date(2024, 1, 1)).days
        cases = [
            (DailyRule(WallTime.from_parts(2024, 1, 1, 9), interval=1), days + 4),
            (DailyRule(WallTime.from_parts(2024, 1, 1, 9), interval=7), days // 5 + 4),
            (
                WeeklyRule(
                    WallTime.from_parts(2024, 1, 1, 9), by_weekday=(0, 2, 4), interval=3
                ),
                days // 5 + 4,
            ),
            (
                MonthlyRule(WallTime.from_parts(2024, 1, 31, 9), by_monthday=(31,)),
                12 * 10 + 4,
            ),
        ]
        for rule, bound in cases:
            sched = Schedule(rule, self.table)
            expansion = sched.expand(window)
            with self.subTest(rule=rule.frequency):
                self.assertLessEqual(expansion.jumps, bound)
                self.assertGreater(len(expansion.events), 0)
                # Cross-check the first month against the day-by-day oracle.
                head = sched.expand(
                    Window(wall_instant(2024, 1, 1), wall_instant(2024, 2, 1))
                )
                reference, _ = brute_force_days(
                    sched,
                    Window(wall_instant(2024, 1, 1), wall_instant(2024, 2, 1)),
                )
                self.assertEqual(
                    [e.instant.epoch_us for e in head.events],
                    [e.instant.epoch_us for e in reference],
                )


if __name__ == "__main__":
    unittest.main()
