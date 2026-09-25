"""Unit tests for exact effective-time computation (python3.11 -m unittest -v)."""

import unittest
from datetime import datetime, time, timezone


from schedule import (ClosedInterval, DailyWindow, Schedule,
                      add_effective_duration, effective_duration)
from timepoint import (Duration, Instant, OffsetTable, OffsetTransition,
                       WallTime)

HOUR = 3600


def utc(iso: str) -> Instant:
    return Instant.from_utc(datetime.fromisoformat(iso).replace(tzinfo=timezone.utc))


def make_table() -> OffsetTable:
    """UTC+0 base; spring forward to UTC+1 at 2024-03-31 02:00Z,
    fall back to UTC+0 at 2024-10-27 02:00Z."""
    return OffsetTable(
        base_offset=Duration(0),
        transitions=(
            OffsetTransition(at=utc("2024-03-31T02:00:00"),
                             offset_after=Duration(HOUR)),
            OffsetTransition(at=utc("2024-10-27T02:00:00"),
                             offset_after=Duration(0)),
        ),
    )


class TypeDistinctionTests(unittest.TestCase):
    def test_duration_rejects_non_int(self):
        with self.assertRaises(TypeError):
            Duration(1.5)  # type: ignore[arg-type]

    def test_instant_rejects_non_int(self):
        with self.assertRaises(TypeError):
            Instant(0.5)  # type: ignore[arg-type]

    def test_wall_time_rejects_aware_datetime(self):
        with self.assertRaises(ValueError):
            WallTime(datetime(2024, 1, 1, tzinfo=timezone.utc))

    def test_instant_plus_instant_is_type_error(self):
        with self.assertRaises(TypeError):
            utc("2024-01-01T00:00:00") + utc("2024-01-01T01:00:00")  # type: ignore[operator]

    def test_duration_plus_instant_is_type_error(self):
        with self.assertRaises(TypeError):
            Duration(60) + utc("2024-01-01T00:00:00")  # type: ignore[operator]

    def test_effective_duration_rejects_wall_time(self):
        schedule = Schedule(make_table(), [DailyWindow(time(1), time(4))])
        with self.assertRaises(TypeError):
            effective_duration(schedule, WallTime.of(2024, 5, 6, 1),  # type: ignore[arg-type]
                               utc("2024-05-06T04:00:00"))

    def test_instant_minus_instant_gives_duration(self):
        delta = utc("2024-01-01T02:00:00") - utc("2024-01-01T01:00:00")
        self.assertIsInstance(delta, Duration)
        self.assertEqual(delta, Duration(HOUR))


class OffsetTableTests(unittest.TestCase):
    def setUp(self):
        self.table = make_table()

    def test_round_trip_normal_day(self):
        wall = WallTime.of(2024, 5, 6, 12, 30, 15)
        self.assertEqual(self.table.to_wall(self.table.to_instant(wall)), wall)

    def test_spring_forward_skips_wall_hour(self):
        before = self.table.to_instant(WallTime.of(2024, 3, 31, 1, 59, 59))
        after = self.table.to_instant(WallTime.of(2024, 3, 31, 3, 0, 0))
        self.assertEqual((after - before).seconds, 1)

    def test_skipped_wall_snaps_to_transition(self):
        snapped = self.table.to_instant(WallTime.of(2024, 3, 31, 2, 30))
        self.assertEqual(snapped, utc("2024-03-31T02:00:00"))
        self.assertEqual(self.table.to_wall(snapped).naive.hour, 3)

    def test_repeated_wall_uses_earliest_instant(self):
        instant = self.table.to_instant(WallTime.of(2024, 10, 27, 2, 30))
        self.assertEqual(instant, utc("2024-10-27T01:30:00"))

    def test_fall_back_repeats_wall_hour(self):
        first = self.table.to_instant(WallTime.of(2024, 10, 27, 2, 0))
        second = utc("2024-10-27T02:00:00")
        self.assertEqual(self.table.to_wall(first).naive.hour, 2)
        self.assertEqual(self.table.to_wall(second).naive.hour, 2)
        self.assertEqual((second - first).seconds, HOUR)


class EffectiveDurationTests(unittest.TestCase):
    def setUp(self):
        self.table = make_table()
        self.window = DailyWindow(time(1, 0), time(4, 0))

    def wall(self, *args) -> Instant:
        return self.table.to_instant(WallTime.of(*args))

    def test_plain_day_full_window(self):
        schedule = Schedule(self.table, [self.window])
        result = effective_duration(schedule, self.wall(2024, 5, 6, 1),
                                    self.wall(2024, 5, 6, 4))
        self.assertEqual(result, Duration.of(hours=3))

    def test_spring_forward_day_counts_real_time(self):
        schedule = Schedule(self.table, [self.window])
        result = effective_duration(schedule, self.wall(2024, 3, 31, 1),
                                    self.wall(2024, 3, 31, 4))
        self.assertEqual(result, Duration.of(hours=2))

    def test_fall_back_day_counts_real_time(self):
        schedule = Schedule(self.table, [self.window])
        result = effective_duration(schedule, self.wall(2024, 10, 27, 1),
                                    self.wall(2024, 10, 27, 4))
        self.assertEqual(result, Duration.of(hours=4))

    def test_time_outside_window_not_counted(self):
        schedule = Schedule(self.table, [self.window])
        result = effective_duration(schedule, self.wall(2024, 5, 6, 0, 30),
                                    self.wall(2024, 5, 6, 2, 0))
        self.assertEqual(result, Duration.of(hours=1))

    def test_closed_interval_deducted(self):
        closed = [ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2),
                                           WallTime.of(2024, 5, 6, 2, 30))]
        schedule = Schedule(self.table, [self.window], closed)
        result = effective_duration(schedule, self.wall(2024, 5, 6, 1),
                                    self.wall(2024, 5, 6, 4))
        self.assertEqual(result, Duration.of(hours=2, minutes=30))

    def test_overlapping_closed_intervals_deducted_once(self):
        closed = [
            ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2),
                                     WallTime.of(2024, 5, 6, 3)),
            ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2, 30),
                                     WallTime.of(2024, 5, 6, 3, 30)),
        ]
        schedule = Schedule(self.table, [self.window], closed)
        result = effective_duration(schedule, self.wall(2024, 5, 6, 1),
                                    self.wall(2024, 5, 6, 4))
        self.assertEqual(result, Duration.of(hours=1, minutes=30))

    def test_fully_closed_range_is_zero(self):
        closed = [ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2),
                                           WallTime.of(2024, 5, 6, 3))]
        schedule = Schedule(self.table, [self.window], closed)
        result = effective_duration(schedule, self.wall(2024, 5, 6, 2, 15),
                                    self.wall(2024, 5, 6, 2, 45))
        self.assertEqual(result, Duration(0))

    def test_spanning_multiple_days(self):
        schedule = Schedule(self.table, [self.window])
        result = effective_duration(schedule, self.wall(2024, 5, 6, 2),
                                    self.wall(2024, 5, 8, 2))
        self.assertEqual(result, Duration.of(hours=2 + 3 + 1))

    def test_end_before_start_raises(self):
        schedule = Schedule(self.table, [self.window])
        with self.assertRaises(ValueError):
            effective_duration(schedule, self.wall(2024, 5, 6, 4),
                               self.wall(2024, 5, 6, 1))


class AddEffectiveDurationTests(unittest.TestCase):
    def setUp(self):
        self.table = make_table()
        self.window = DailyWindow(time(1, 0), time(4, 0))

    def wall(self, *args) -> Instant:
        return self.table.to_instant(WallTime.of(*args))

    def test_plain_addition_within_window(self):
        schedule = Schedule(self.table, [self.window])
        result = add_effective_duration(schedule, self.wall(2024, 5, 6, 1),
                                        Duration.of(hours=1, minutes=30))
        self.assertEqual(result, self.wall(2024, 5, 6, 2, 30))

    def test_closed_interval_skipped(self):
        closed = [ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2),
                                           WallTime.of(2024, 5, 6, 2, 30))]
        schedule = Schedule(self.table, [self.window], closed)
        result = add_effective_duration(schedule, self.wall(2024, 5, 6, 1),
                                        Duration.of(hours=1, minutes=30))
        self.assertEqual(result, self.wall(2024, 5, 6, 3, 0))

    def test_result_landing_on_closed_jumps_to_next_open(self):
        closed = [ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2),
                                           WallTime.of(2024, 5, 6, 2, 30))]
        schedule = Schedule(self.table, [self.window], closed)
        result = add_effective_duration(schedule, self.wall(2024, 5, 6, 1),
                                        Duration.of(hours=1))
        self.assertEqual(result, self.wall(2024, 5, 6, 2, 30))

    def test_overlapping_closed_intervals_skipped_once(self):
        closed = [
            ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2),
                                     WallTime.of(2024, 5, 6, 3)),
            ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2, 30),
                                     WallTime.of(2024, 5, 6, 3, 30)),
        ]
        schedule = Schedule(self.table, [self.window], closed)
        result = add_effective_duration(schedule, self.wall(2024, 5, 6, 1),
                                        Duration.of(hours=1, minutes=30))
        self.assertEqual(result, self.wall(2024, 5, 6, 4, 0))

    def test_addition_across_spring_forward(self):
        schedule = Schedule(self.table, [self.window])
        result = add_effective_duration(schedule, self.wall(2024, 3, 31, 1),
                                        Duration.of(hours=2))
        self.assertEqual(result, utc("2024-03-31T03:00:00"))
        self.assertEqual(self.table.to_wall(result), WallTime.of(2024, 3, 31, 4))

    def test_addition_across_midnight(self):
        schedule = Schedule(self.table, [self.window])
        result = add_effective_duration(schedule, self.wall(2024, 5, 6, 2),
                                        Duration.of(hours=4))
        self.assertEqual(result, self.wall(2024, 5, 7, 3))

    def test_zero_duration_returns_start(self):
        schedule = Schedule(self.table, [self.window])
        start = self.wall(2024, 5, 6, 1)
        self.assertEqual(add_effective_duration(schedule, start, Duration(0)), start)

    def test_negative_duration_raises(self):
        schedule = Schedule(self.table, [self.window])
        with self.assertRaises(ValueError):
            add_effective_duration(schedule, self.wall(2024, 5, 6, 1), Duration(-1))

    def test_round_trip_inverse_of_effective_duration(self):
        closed = [ClosedInterval.from_wall(self.table, WallTime.of(2024, 5, 6, 2),
                                           WallTime.of(2024, 5, 6, 2, 45))]
        schedule = Schedule(self.table, [self.window], closed)
        start = self.wall(2024, 5, 6, 1)
        end = self.wall(2024, 5, 6, 3, 30)
        delta = effective_duration(schedule, start, end)
        self.assertEqual(delta, Duration.of(hours=1, minutes=45))
        self.assertEqual(add_effective_duration(schedule, start, delta), end)


if __name__ == "__main__":
    unittest.main()
