import unittest
from datetime import datetime, time, timedelta

from effective_time import (
    ClosedInterval,
    DailyWindow,
    Duration,
    EffectiveCalendar,
    Instant,
    OffsetTable,
    OffsetTransition,
    WallTime,
)

H8 = timedelta(hours=8)
H9 = timedelta(hours=9)

# 合成偏移转换表:基准 +08:00
# 2024-03-10 02:00 前拨一小时(+8 -> +9),墙钟 02:00 直接跳到 03:00
# 2024-11-03 02:00 回拨一小时(+9 -> +8),墙钟 01:00-02:00 出现两次
SPRING_FORWARD = OffsetTransition(WallTime(datetime(2024, 3, 10, 2, 0)), H8, H9)
FALL_BACK = OffsetTransition(WallTime(datetime(2024, 11, 3, 2, 0)), H9, H8)


def make_table():
    return OffsetTable(H8, [SPRING_FORWARD, FALL_BACK])


def to_instant(table, dt, **kwargs):
    return table.wall_to_instant(WallTime(dt), **kwargs)


def to_wall(table, instant):
    return table.instant_to_wall(instant).value


class TypeDistinctionTest(unittest.TestCase):
    def test_wall_time_rejects_aware_datetime(self):
        with self.assertRaises(ValueError):
            WallTime(datetime(2024, 1, 1, tzinfo=__import__("datetime").timezone.utc))

    def test_instant_arithmetic_types(self):
        instant = Instant(1_000_000)
        duration = Duration.of(hours=1)
        self.assertIsInstance(instant + duration, Instant)
        self.assertIsInstance(instant - duration, Instant)
        self.assertIsInstance(instant - Instant(0), Duration)
        with self.assertRaises(TypeError):
            instant + instant  # 时间点 + 时间点无意义
        with self.assertRaises(TypeError):
            duration + instant  # 时长 + 时间点无意义

    def test_calendar_rejects_wall_time_arguments(self):
        table = make_table()
        calendar = EffectiveCalendar(table, [DailyWindow(time(9), time(17))])
        with self.assertRaises(TypeError):
            calendar.effective_duration(
                WallTime(datetime(2024, 1, 1, 9)), WallTime(datetime(2024, 1, 1, 17))
            )
        with self.assertRaises(TypeError):
            table.wall_to_instant(Instant(0))


class OffsetConversionTest(unittest.TestCase):
    def setUp(self):
        self.table = make_table()

    def test_round_trip(self):
        wall = WallTime(datetime(2024, 3, 9, 12, 0))
        self.assertEqual(self.table.instant_to_wall(self.table.wall_to_instant(wall)), wall)

    def test_nonexistent_wall_time_shifts_forward(self):
        # 前拨空洞中的 02:30 不存在,顺延到空洞结束(即墙钟 03:00)
        gap = to_instant(self.table, datetime(2024, 3, 10, 2, 30))
        after = to_instant(self.table, datetime(2024, 3, 10, 3, 0))
        self.assertEqual(gap, after)

    def test_ambiguous_wall_time_first_and_last(self):
        # 回拨后 01:30 出现两次,两次相差真实一小时
        first = to_instant(self.table, datetime(2024, 11, 3, 1, 30), ambiguous="first")
        last = to_instant(self.table, datetime(2024, 11, 3, 1, 30), ambiguous="last")
        self.assertEqual(last - first, Duration.of(hours=1))

    def test_transition_chain_validation(self):
        with self.assertRaises(ValueError):
            OffsetTable(H8, [OffsetTransition(WallTime(datetime(2024, 5, 1)), H9, H8)])


class EffectiveDurationTest(unittest.TestCase):
    def setUp(self):
        self.table = make_table()

    def test_spring_forward_window_counts_true_duration(self):
        # 开放 01:00-04:00,02:00 前拨一小时:墙钟 3 小时,实际只有 2 小时
        calendar = EffectiveCalendar(self.table, [DailyWindow(time(1), time(4))])
        start = to_instant(self.table, datetime(2024, 3, 10, 1, 0))
        end = to_instant(self.table, datetime(2024, 3, 10, 4, 0))
        self.assertEqual(calendar.effective_duration(start, end), Duration.of(hours=2))

    def test_fall_back_window_counts_repeated_hour(self):
        # 回拨:墙钟 01:00-04:00 实际经过 4 小时(01:00-02:00 走两遍)
        calendar = EffectiveCalendar(self.table, [DailyWindow(time(1), time(4))])
        start = to_instant(self.table, datetime(2024, 11, 3, 1, 0), ambiguous="first")
        end = to_instant(self.table, datetime(2024, 11, 3, 4, 0))
        self.assertEqual(calendar.effective_duration(start, end), Duration.of(hours=4))

    def test_overlapping_closed_intervals_deducted_once(self):
        # 关闭段 12:00-14:00 与 13:00-15:00 重叠,合并后只扣 3 小时
        calendar = EffectiveCalendar(
            self.table,
            [DailyWindow(time(9), time(17))],
            [
                ClosedInterval(WallTime(datetime(2024, 1, 8, 12)), WallTime(datetime(2024, 1, 8, 14))),
                ClosedInterval(WallTime(datetime(2024, 1, 8, 13)), WallTime(datetime(2024, 1, 8, 15))),
            ],
        )
        start = to_instant(self.table, datetime(2024, 1, 8, 9))
        end = to_instant(self.table, datetime(2024, 1, 8, 17))
        self.assertEqual(calendar.effective_duration(start, end), Duration.of(hours=5))

    def test_partial_overlap_with_open_window(self):
        calendar = EffectiveCalendar(
            self.table,
            [DailyWindow(time(9), time(17))],
            [ClosedInterval(WallTime(datetime(2024, 1, 8, 16)), WallTime(datetime(2024, 1, 8, 20)))],
        )
        start = to_instant(self.table, datetime(2024, 1, 8, 15))
        end = to_instant(self.table, datetime(2024, 1, 8, 17))
        self.assertEqual(calendar.effective_duration(start, end), Duration.of(hours=1))

    def test_duration_across_days_and_transition(self):
        # 跨多日与夏令时切换:周五 10:00 到下周二 12:00,每日 09:00-17:00
        calendar = EffectiveCalendar(self.table, [DailyWindow(time(9), time(17))])
        start = to_instant(self.table, datetime(2024, 3, 8, 10, 0))
        end = to_instant(self.table, datetime(2024, 3, 12, 12, 0))
        # 周五 7h + 周六 8h + 周日(前拨日,09-17 不受影响)8h + 周一 8h + 周二 3h = 34h
        self.assertEqual(calendar.effective_duration(start, end), Duration.of(hours=34))


class InverseAddTest(unittest.TestCase):
    def setUp(self):
        self.table = make_table()

    def test_add_skips_closed_interval(self):
        calendar = EffectiveCalendar(
            self.table,
            [DailyWindow(time(9), time(17))],
            [ClosedInterval(WallTime(datetime(2024, 1, 8, 12)), WallTime(datetime(2024, 1, 8, 13)))],
        )
        start = to_instant(self.table, datetime(2024, 1, 8, 9))
        # 09:00 + 4h 有效:09-12 共 3h,跳过 12-13 关闭段,再计 1h 到 14:00
        end = calendar.add(start, Duration.of(hours=4))
        self.assertEqual(to_wall(self.table, end), datetime(2024, 1, 8, 14, 0))

    def test_add_landing_on_closed_boundary_jumps_to_next_open(self):
        # 两个开放段 09:00-12:00、13:00-17:00,中间 12-13 为间隙
        calendar = EffectiveCalendar(
            self.table,
            [DailyWindow(time(9), time(12)), DailyWindow(time(13), time(17))],
        )
        start = to_instant(self.table, datetime(2024, 1, 8, 9))
        # 恰好 3h 落在第一段结束边界(关闭段起点),须跳到下一开放段 13:00
        end = calendar.add(start, Duration.of(hours=3))
        self.assertEqual(to_wall(self.table, end), datetime(2024, 1, 8, 13, 0))

    def test_add_across_spring_forward(self):
        # 01:00 + 1.5h 有效:01-02 计 1h,前拨跳过 02-03,03:00-03:30 再计 0.5h
        calendar = EffectiveCalendar(self.table, [DailyWindow(time(1), time(4))])
        start = to_instant(self.table, datetime(2024, 3, 10, 1, 0))
        end = calendar.add(start, Duration.of(hours=1, minutes=30))
        self.assertEqual(to_wall(self.table, end), datetime(2024, 3, 10, 3, 30))
        self.assertEqual(end - start, Duration.of(hours=1, minutes=30))

    def test_add_exactly_filling_window_jumps_to_next_open(self):
        # 前拨日 01:00-04:00 实际仅 2h;恰好加满后落在关闭边界,跳到下一开放段
        calendar = EffectiveCalendar(self.table, [DailyWindow(time(1), time(4))])
        start = to_instant(self.table, datetime(2024, 3, 10, 1, 0))
        end = calendar.add(start, Duration.of(hours=2))
        self.assertEqual(to_wall(self.table, end), datetime(2024, 3, 11, 1, 0))

    def test_add_from_inside_closed_interval(self):
        calendar = EffectiveCalendar(
            self.table,
            [DailyWindow(time(9), time(17))],
            [ClosedInterval(WallTime(datetime(2024, 1, 8, 12)), WallTime(datetime(2024, 1, 8, 13)))],
        )
        start = to_instant(self.table, datetime(2024, 1, 8, 12, 30))  # 位于关闭段内
        end = calendar.add(start, Duration.of(hours=1))
        self.assertEqual(to_wall(self.table, end), datetime(2024, 1, 8, 14, 0))

    def test_round_trip(self):
        calendar = EffectiveCalendar(
            self.table,
            [DailyWindow(time(9), time(12)), DailyWindow(time(13), time(17))],
            [ClosedInterval(WallTime(datetime(2024, 3, 11, 14)), WallTime(datetime(2024, 3, 11, 15)))],
        )
        start = to_instant(self.table, datetime(2024, 3, 8, 10, 0))
        end = to_instant(self.table, datetime(2024, 3, 12, 16, 0))
        duration = calendar.effective_duration(start, end)
        self.assertEqual(calendar.add(start, duration), end)

    def test_add_zero_duration(self):
        calendar = EffectiveCalendar(self.table, [DailyWindow(time(9), time(17))])
        start = to_instant(self.table, datetime(2024, 1, 8, 10))
        self.assertEqual(calendar.add(start, Duration.of(hours=0)), start)

    def test_add_negative_duration_rejected(self):
        calendar = EffectiveCalendar(self.table, [DailyWindow(time(9), time(17))])
        with self.assertRaises(ValueError):
            calendar.add(Instant(0), Duration.of(hours=-1))


if __name__ == "__main__":
    unittest.main()
