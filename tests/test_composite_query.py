import datetime as dt
import unittest

from recur import (
    DailyRule,
    Duration,
    Instant,
    MonthlyRule,
    STD,
    Schedule,
    StaleCursorError,
    WallTime,
    WeeklyRule,
    Window,
    difference,
    intersection,
    make_modified_table,
    make_table,
    nth_event,
    query_page,
    union,
)
from tests._oracle import brute_force_days


def win(y0, m0, d0, y1, m1, d1):
    return Window(
        Instant(WallTime.from_parts(y0, m0, d0).epoch_us - STD),
        Instant(WallTime.from_parts(y1, m1, d1).epoch_us - STD),
    )


class CompositeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_table()
        cls.daily = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9), name="daily"), cls.table
        )
        cls.monday = Schedule(
            WeeklyRule(
                WallTime.from_parts(2024, 1, 1, 9), by_weekday=(0,), name="monday"
            ),
            cls.table,
        )
        cls.window = win(2024, 1, 1, 2024, 2, 15)

    def test_union_count_and_order(self):
        events = union([self.daily, self.monday]).expand(self.window).events
        self.assertEqual(len(events), 45)  # Mondays overlap -> deduped
        epochs = [e.instant.epoch_us for e in events]
        self.assertEqual(epochs, sorted(epochs))

    def test_intersection_is_mondays(self):
        events = intersection([self.daily, self.monday]).expand(self.window).events
        self.assertEqual(
            [e.wall.date for e in events],
            [dt.date(2024, 1, 1), dt.date(2024, 1, 8), dt.date(2024, 1, 15),
             dt.date(2024, 1, 22), dt.date(2024, 1, 29), dt.date(2024, 2, 5),
             dt.date(2024, 2, 12)],
        )

    def test_difference_removes_mondays(self):
        remaining = difference(self.daily, self.monday).expand(self.window).events
        union_events = union([self.daily, self.monday]).expand(self.window).events
        common = intersection([self.daily, self.monday]).expand(self.window).events
        self.assertEqual(len(remaining), len(union_events) - len(common))
        self.assertTrue(all(e.wall.date.weekday() != 0 for e in remaining))

    def test_composite_requires_shared_table(self):
        other = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9)), make_modified_table()
        )
        with self.assertRaises(ValueError):
            union([self.daily, other])

    def test_composite_matches_oracle_counts(self):
        # Difference with an excluded day still affects the combined count.
        blocked = Schedule(
            MonthlyRule(WallTime.from_parts(2024, 1, 15, 9), by_monthday=(15,)),
            self.table,
        )
        node = difference(self.daily, blocked)
        events = node.expand(win(2024, 1, 1, 2024, 2, 1)).events
        self.assertEqual(len(events), 30)  # 31 January days minus Jan 15


class NthEventTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_table()

    def test_nth_daily(self):
        sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9), interval=3), self.table
        )
        self.assertEqual(nth_event(sched, 1).wall.date, dt.date(2024, 1, 1))
        self.assertEqual(nth_event(sched, 5).wall.date, dt.date(2024, 1, 13))

    def test_nth_monthly_31_skips_months(self):
        sched = Schedule(
            MonthlyRule(WallTime.from_parts(2024, 1, 31, 9), by_monthday=(31,)),
            self.table,
        )
        self.assertEqual(
            (nth_event(sched, 2).wall.year, nth_event(sched, 2).wall.month),
            (2024, 3),
        )

    def test_nth_respects_exclusions(self):
        sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9)),
            self.table,
            exdates={dt.date(2024, 1, 1), dt.date(2024, 1, 2)},
        )
        self.assertEqual(nth_event(sched, 1).wall.date, dt.date(2024, 1, 3))

    def test_nth_invalid(self):
        sched = Schedule(DailyRule(WallTime.from_parts(2024, 1, 1, 9)), self.table)
        with self.assertRaises(ValueError):
            nth_event(sched, 0)


class PaginationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_table()
        cls.sched = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9), name="daily"), cls.table
        )
        cls.window = win(2024, 1, 1, 2024, 2, 15)

    def test_pages_cover_window_without_gaps_or_dupes(self):
        seen = []
        cursor = None
        pages = 0
        while True:
            page = query_page(self.sched, self.window, limit=7, cursor=cursor)
            seen.extend(e.instant.epoch_us for e in page.events)
            pages += 1
            cursor = page.next_cursor
            if cursor is None:
                break
        expected = [e.instant.epoch_us for e in self.sched.expand(self.window).events]
        self.assertEqual(seen, expected)
        self.assertEqual(len(seen), len(set(seen)))
        self.assertGreater(pages, 1)

    def test_cursor_stale_on_table_version(self):
        other_table = make_modified_table()
        other = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 9), name="daily"), other_table
        )
        page = query_page(self.sched, self.window, limit=5)
        with self.assertRaises(StaleCursorError):
            query_page(other, self.window, limit=5, cursor=page.next_cursor)

    def test_cursor_stale_on_window(self):
        page = query_page(self.sched, self.window, limit=5)
        with self.assertRaises(StaleCursorError):
            query_page(self.sched, win(2024, 1, 1, 2024, 2, 16), limit=5,
                       cursor=page.next_cursor)

    def test_cursor_stale_on_rule_change(self):
        changed = Schedule(
            DailyRule(WallTime.from_parts(2024, 1, 1, 10), name="daily"), self.table
        )
        page = query_page(self.sched, self.window, limit=5)
        with self.assertRaises(StaleCursorError):
            query_page(changed, self.window, limit=5, cursor=page.next_cursor)


class DurationSemanticsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_table()

    def _instant(self, y, m, d, hh, mm=0, **policy):
        return self.table.resolve(WallTime.from_parts(y, m, d, hh, mm), **policy).instant

    def test_nominal_vs_elapsed_across_spring_forward(self):
        a = WallTime.from_parts(2024, 3, 9, 12)
        b = WallTime.from_parts(2024, 3, 11, 12)
        nominal = b - a
        ia = self._instant(2024, 3, 9, 12)
        ib = self._instant(2024, 3, 11, 12)
        elapsed = ib - ia
        self.assertEqual(nominal, Duration.of(days=2))
        self.assertEqual(elapsed, Duration.of(days=2) - Duration.of(hours=1))

    def test_nominal_vs_elapsed_across_fall_back(self):
        ia = self._instant(2024, 11, 2, 12)
        ib = self._instant(2024, 11, 4, 12)
        elapsed = ib - ia
        self.assertEqual(elapsed, Duration.of(days=2) + Duration.of(hours=1))

    def test_microsecond_exactness_survives_transition(self):
        w = WallTime.from_parts(2024, 7, 1, 12, 0, 0, 999_999)
        instant = self.table.resolve(w).instant
        back, _ = self.table.wall_at(instant)
        self.assertEqual(back, w)
        self.assertEqual(instant.epoch_us % 1_000_000, 999_999)


if __name__ == "__main__":
    unittest.main()
