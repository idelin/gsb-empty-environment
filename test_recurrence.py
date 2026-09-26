"""Unittest suite. Counts/kth from arithmetic jumps are cross-checked
against a brute-force per-day reference over small windows."""

import unittest
from datetime import datetime, timedelta

from fixtures import make_test_table
from recurrence import (
    Cursor,
    DailyRule,
    FoldPolicy,
    GapPolicy,
    Instant,
    MonthlyRule,
    NonexistentWallTime,
    RuleSet,
    WallClock,
    WeeklyRule,
)


def W(y, m, d, hh=0, mm=0, ss=0):
    return WallClock(datetime(y, m, d, hh, mm, ss))


def brute_force(rule, start, end):
    """Per-day reference: probe every day at the rule's time of day."""
    out = []
    tod = rule.anchor.dt.time()
    day = start.dt.date()
    while datetime.combine(day, tod) < end.dt:
        w = WallClock(datetime.combine(day, tod))
        if start <= w < end and rule.contains(w):
            out.append(w)
        day += timedelta(days=1)
    return out


class DailyRuleTest(unittest.TestCase):
    def test_count_matches_brute_force(self):
        rule = DailyRule(W(2026, 1, 5, 9, 30), interval=3)
        start, end = W(2026, 2, 1), W(2027, 3, 1)
        expected = brute_force(rule, start, end)
        self.assertEqual(rule.count_between(start, end), len(expected))
        self.assertEqual(list(rule.iter_wall(start, end)), expected)

    def test_large_span_jumps_without_scanning(self):
        rule = DailyRule(W(2000, 1, 1, 8, 0), interval=1)
        count = rule.count_between(W(2000, 1, 1), W(4000, 1, 1))
        self.assertEqual(count, 2000 * 365 + 484 + 1)  # 484 leap days in 2000..3999
        self.assertEqual(rule.last_meta["periods_scanned"], 0)
        self.assertEqual(rule.last_meta["periods_jumped"], count)

    def test_kth(self):
        rule = DailyRule(W(2026, 1, 1, 6, 0), interval=10)
        self.assertEqual(rule.kth(0), W(2026, 1, 1, 6, 0))
        self.assertEqual(rule.kth(3), W(2026, 1, 31, 6, 0))


class WeeklyRuleTest(unittest.TestCase):
    def test_count_matches_brute_force(self):
        rule = WeeklyRule(W(2026, 1, 7, 17, 0), weekdays={0, 2, 4}, interval=2)
        start, end = W(2026, 1, 1), W(2028, 6, 30)
        expected = brute_force(rule, start, end)
        self.assertEqual(rule.count_between(start, end), len(expected))
        self.assertEqual(list(rule.iter_wall(start, end)), expected)
        # O(1): only boundary weeks scanned regardless of span
        self.assertLessEqual(rule.last_meta["periods_scanned"], 2)

    def test_anchor_midweek_drops_earlier_weekdays(self):
        rule = WeeklyRule(W(2026, 1, 7, 9, 0), weekdays={0, 2})  # anchor is Wed
        got = list(rule.iter_wall(W(2026, 1, 1), W(2026, 1, 20)))
        self.assertEqual(got, [W(2026, 1, 7, 9, 0), W(2026, 1, 12, 9, 0),
                               W(2026, 1, 14, 9, 0), W(2026, 1, 19, 9, 0)])

    def test_kth_matches_brute_force(self):
        rule = WeeklyRule(W(2026, 3, 4, 12, 0), weekdays={1, 3}, interval=1)
        expected = brute_force(rule, W(2026, 1, 1), W(2028, 1, 1))
        for k in (0, 1, 5, 50, len(expected) - 1):
            self.assertEqual(rule.kth(k), expected[k])


class MonthlyRuleTest(unittest.TestCase):
    def test_31st_skips_short_months(self):
        rule = MonthlyRule(W(2026, 1, 31, 8, 0), monthdays={31})
        got = list(rule.iter_wall(W(2026, 1, 1), W(2026, 8, 1)))
        self.assertEqual(
            got,
            [W(2026, 1, 31, 8, 0), W(2026, 3, 31, 8, 0),
             W(2026, 5, 31, 8, 0), W(2026, 7, 31, 8, 0)],
        )

    def test_count_matches_brute_force(self):
        rule = MonthlyRule(W(2026, 2, 10, 7, 15), monthdays={5, 10, 29, 31}, interval=3)
        start, end = W(2026, 1, 1), W(2031, 1, 1)
        expected = brute_force(rule, start, end)
        self.assertEqual(rule.count_between(start, end), len(expected))
        self.assertEqual(list(rule.iter_wall(start, end)), expected)
        # per-month scan, never per-day
        self.assertLess(rule.last_meta["periods_scanned"], 5 * 12)

    def test_kth_matches_brute_force(self):
        rule = MonthlyRule(W(2026, 1, 31, 0, 0), monthdays={15, 31})
        expected = brute_force(rule, W(2026, 1, 1), W(2030, 1, 1))
        for k in (0, 1, 7, 40, len(expected) - 1):
            self.assertEqual(rule.kth(k), expected[k])

    def test_monthly_rule_does_not_advance_by_fixed_seconds(self):
        table = make_test_table()
        rule = MonthlyRule(W(2026, 1, 31, 12, 0), monthdays={31})
        rs = RuleSet(table).add_rule(rule)
        pairs = list(rs.iter_instants(W(2026, 1, 1), W(2027, 1, 1)))
        gaps = {b[1].epoch_seconds - a[1].epoch_seconds for a, b in zip(pairs, pairs[1:])}
        self.assertGreater(len(gaps), 1)  # month lengths and offset shifts vary


class OffsetTableTest(unittest.TestCase):
    def setUp(self):
        self.table = make_test_table()

    def test_gap_shift_forward(self):
        instant = self.table.to_instant(W(2026, 3, 29, 2, 30), GapPolicy.SHIFT_FORWARD)
        self.assertEqual(instant.to_utc(), datetime(2026, 3, 29, 1, 30,
                                                    tzinfo=__import__("datetime").timezone.utc))

    def test_gap_reject(self):
        with self.assertRaises(NonexistentWallTime):
            self.table.to_instant(W(2026, 3, 29, 2, 30), GapPolicy.REJECT)

    def test_fold_policies(self):
        wall = W(2026, 10, 25, 2, 30)
        early = self.table.to_instant(wall, fold_policy=FoldPolicy.EARLIER)
        late = self.table.to_instant(wall, fold_policy=FoldPolicy.LATER)
        self.assertEqual(late.epoch_seconds - early.epoch_seconds, 3600)

    def test_thirty_minute_gap_and_fold(self):
        shifted = self.table.to_instant(W(2027, 1, 15, 1, 10), GapPolicy.SHIFT_FORWARD)
        self.assertEqual(self.table.to_wall(shifted), W(2027, 1, 15, 1, 40))
        early = self.table.to_instant(W(2027, 6, 15, 1, 10), fold_policy=FoldPolicy.EARLIER)
        late = self.table.to_instant(W(2027, 6, 15, 1, 10), fold_policy=FoldPolicy.LATER)
        self.assertEqual(late.epoch_seconds - early.epoch_seconds, 1800)

    def test_roundtrip(self):
        for wall in (W(2026, 5, 1, 10, 0), W(2026, 12, 31, 23, 59)):
            self.assertEqual(self.table.to_wall(self.table.to_instant(wall)), wall)


class RuleSetTest(unittest.TestCase):
    def setUp(self):
        self.table = make_test_table()

    def test_union_dedupes(self):
        rs = RuleSet(self.table)
        rs.add_rule(DailyRule(W(2026, 1, 1, 9, 0), interval=2))
        rs.add_rule(DailyRule(W(2026, 1, 1, 9, 0), interval=4))
        got = list(rs.iter_wall(W(2026, 1, 1), W(2026, 1, 9)))
        self.assertEqual(got, [W(2026, 1, 1, 9, 0), W(2026, 1, 3, 9, 0),
                               W(2026, 1, 5, 9, 0), W(2026, 1, 7, 9, 0)])

    def test_intersection(self):
        rs = RuleSet(self.table)
        rs.add_rule(DailyRule(W(2026, 1, 1, 9, 0), interval=1))
        rs.add_intersect(WeeklyRule(W(2026, 1, 5, 9, 0), weekdays={0, 6}))
        got = list(rs.iter_wall(W(2026, 1, 1), W(2026, 1, 20)))
        self.assertEqual(got, [W(2026, 1, 5, 9, 0), W(2026, 1, 11, 9, 0),
                               W(2026, 1, 12, 9, 0), W(2026, 1, 18, 9, 0),
                               W(2026, 1, 19, 9, 0)])

    def test_difference(self):
        rs = RuleSet(self.table)
        rs.add_rule(DailyRule(W(2026, 1, 1, 9, 0)))
        rs.add_exclude_rule(WeeklyRule(W(2026, 1, 3, 9, 0), weekdays={5, 6}))
        got = list(rs.iter_wall(W(2026, 1, 1), W(2026, 1, 12)))
        self.assertEqual([w.dt.day for w in got], [1, 2, 5, 6, 7, 8, 9])

    def test_exdate_beats_rdate_same_day(self):
        rs = RuleSet(self.table)
        rs.add_rule(DailyRule(W(2026, 1, 1, 9, 0)))
        rs.add_rdate(W(2026, 1, 2, 9, 0))
        rs.add_exdate(W(2026, 1, 2, 9, 0))
        got = list(rs.iter_wall(W(2026, 1, 1), W(2026, 1, 4)))
        self.assertEqual(got, [W(2026, 1, 1, 9, 0), W(2026, 1, 3, 9, 0)])

    def test_rdate_merges_and_dedupes(self):
        rs = RuleSet(self.table)
        rs.add_rule(DailyRule(W(2026, 1, 1, 9, 0), interval=2))
        rs.add_rdate(W(2026, 1, 2, 9, 0))   # extra
        rs.add_rdate(W(2026, 1, 3, 9, 0))   # duplicate of a rule occurrence
        got = list(rs.iter_wall(W(2026, 1, 1), W(2026, 1, 6)))
        self.assertEqual([w.dt.day for w in got], [1, 2, 3, 5])

    def test_exdate_affects_count_and_rank(self):
        base = RuleSet(self.table).add_rule(DailyRule(W(2026, 1, 1, 9, 0)))
        excl = RuleSet(self.table).add_rule(DailyRule(W(2026, 1, 1, 9, 0)))
        excl.add_exdate(W(2026, 1, 3, 9, 0))
        start, end = W(2026, 1, 1), W(2026, 2, 1)
        self.assertEqual(base.count_between(start, end), 31)
        self.assertEqual(excl.count_between(start, end), 30)
        self.assertEqual(base.kth(2, start), W(2026, 1, 3, 9, 0))
        self.assertEqual(excl.kth(2, start), W(2026, 1, 4, 9, 0))  # rank shifted

    def test_half_open_window(self):
        rs = RuleSet(self.table).add_rule(DailyRule(W(2026, 1, 1, 0, 0)))
        got = list(rs.iter_wall(W(2026, 1, 2), W(2026, 1, 5)))
        self.assertEqual([w.dt.day for w in got], [2, 3, 4])  # end excluded

    def test_kth_of_union(self):
        rs = RuleSet(self.table)
        rs.add_rule(DailyRule(W(2026, 1, 1, 9, 0), interval=2))
        rs.add_rule(DailyRule(W(2026, 1, 2, 9, 0), interval=2))
        self.assertEqual(rs.kth(0, W(2026, 1, 1)), W(2026, 1, 1, 9, 0))
        self.assertEqual(rs.kth(9, W(2026, 1, 1)), W(2026, 1, 10, 9, 0))


class CursorTest(unittest.TestCase):
    def setUp(self):
        self.table = make_test_table()
        self.rs = RuleSet(self.table).add_rule(DailyRule(W(2026, 1, 1, 9, 0)))

    def test_pagination_matches_single_pass(self):
        start, end = W(2026, 1, 1), W(2026, 3, 1)
        single = [i for _, i in self.rs.iter_instants(start, end)]
        paged, cursor = [], None
        while True:
            items, cursor = self.rs.page(start, end, 7, cursor)
            paged.extend(i for _, i in items)
            if cursor is None:
                break
        self.assertEqual(paged, single)

    def test_cursor_roundtrip(self):
        _, cursor = self.rs.page(W(2026, 1, 1), W(2026, 3, 1), 5)
        self.assertEqual(Cursor.decode(cursor.encode()), cursor)

    def test_cursor_rejects_other_ruleset(self):
        _, cursor = self.rs.page(W(2026, 1, 1), W(2026, 3, 1), 5)
        other = RuleSet(self.table).add_rule(DailyRule(W(2026, 1, 1, 10, 0)))
        with self.assertRaises(ValueError):
            other.page(W(2026, 1, 1), W(2026, 3, 1), 5, cursor)

    def test_cursor_rejects_other_table_version(self):
        _, cursor = self.rs.page(W(2026, 1, 1), W(2026, 3, 1), 5)
        other_table = make_test_table(version="testland-2099b")
        rs2 = RuleSet(other_table).add_rule(DailyRule(W(2026, 1, 1, 9, 0)))
        with self.assertRaises(ValueError):
            rs2.page(W(2026, 1, 1), W(2026, 3, 1), 5, cursor)


class TimePrimitiveTest(unittest.TestCase):
    def test_wallclock_rejects_aware_and_subsecond(self):
        from datetime import timezone
        with self.assertRaises(ValueError):
            WallClock(datetime(2026, 1, 1, tzinfo=timezone.utc))
        with self.assertRaises(ValueError):
            WallClock(datetime(2026, 1, 1, 0, 0, 0, 1))

    def test_instant_duration_arithmetic(self):
        from recurrence import Duration
        a = Instant(1_000_000)
        b = a.plus(Duration.hours(2))
        self.assertEqual(b.minus(a), Duration(7200))


if __name__ == "__main__":
    unittest.main(verbosity=2)
