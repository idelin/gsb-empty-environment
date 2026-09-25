import unittest

from vtline.time_model import (
    AmbiguousWallTimeError,
    Duration,
    Instant,
    InvalidWallTimeError,
    OffsetTable,
    WallClock,
    _civil_from_days,
)

NANOS_PER_DAY = 86_400 * 1_000_000_000


def make_table():
    """Synthetic offset table: +08:00 from the epoch, a leap-like +1s jump at
    instant JUMP (skips one wall second), and a fallback at instant FALL that
    repeats one wall second."""
    base = Duration.hours(8)
    jump = Instant(1_000_000_000_000)
    fall = Instant(2_000_000_000_000)
    table = OffsetTable(
        [
            (Instant.EPOCH, base),
            (jump, base + Duration.seconds(1)),
            (fall, base),
        ]
    )
    return table, base, jump, fall


def wall_from_local(local_nanos):
    days, rem = divmod(local_nanos, NANOS_PER_DAY)
    y, m, d = _civil_from_days(days)
    hh, rem = divmod(rem, 3_600_000_000_000)
    mm, rem = divmod(rem, 60_000_000_000)
    ss, nn = divmod(rem, 1_000_000_000)
    return WallClock(y, m, d, hh, mm, ss, nn)


class DurationTests(unittest.TestCase):
    def test_exact_integer_arithmetic(self):
        d = Duration.seconds(3) + Duration.nanoseconds(7)
        self.assertEqual(d.nanos, 3_000_000_007)
        self.assertEqual(
            (Duration.hours(2) - Duration.minutes(90)).nanos,
            30 * 60 * 1_000_000_000,
        )
        self.assertEqual((Duration.seconds(5) * 4).nanos, 20_000_000_000)

    def test_duration_is_not_a_point(self):
        with self.assertRaises(TypeError):
            _ = Duration.seconds(1) + Instant(5)
        with self.assertRaises(TypeError):
            _ = Duration.seconds(1) < Instant(5)


class InstantTests(unittest.TestCase):
    def test_point_arithmetic(self):
        t = Instant(100) + Duration.nanoseconds(50)
        self.assertEqual(t.nanos, 150)
        self.assertEqual((t - Duration.nanoseconds(20)).nanos, 130)
        self.assertIsInstance(t - Instant(40), Duration)
        self.assertEqual((t - Instant(40)).nanos, 110)

    def test_adding_two_points_is_a_type_error(self):
        with self.assertRaises(TypeError):
            _ = Instant(1) + Instant(2)

    def test_ordering_and_equality(self):
        self.assertLess(Instant(1), Instant(2))
        self.assertEqual(Instant(7), Instant(7))
        self.assertNotEqual(Instant(7), Duration(7))


class WallClockTests(unittest.TestCase):
    def test_wallclock_is_not_an_instant(self):
        w = WallClock(2024, 1, 1, 8, 0, 0)
        self.assertNotEqual(w, Instant(w._local_nanos()))
        with self.assertRaises(TypeError):
            _ = w + Duration.seconds(1)

    def test_validation(self):
        with self.assertRaises(ValueError):
            WallClock(2024, 13, 1)
        with self.assertRaises(ValueError):
            WallClock(2024, 1, 1, 24)
        with self.assertRaises(ValueError):
            WallClock(2024, 1, 1, 0, 0, 0, 1_000_000_000)


class OffsetTableTests(unittest.TestCase):
    def test_round_trip(self):
        table, base, _, _ = make_table()
        wall = WallClock(2024, 3, 5, 12, 30, 15, 123_456_789)
        instant = table.to_instant(wall)
        self.assertEqual(table.to_wall(instant), wall)
        # exact: local nanos minus offset nanos, no float involved
        self.assertEqual(instant.nanos, wall._local_nanos() - base.nanos)

    def test_offset_extends_to_negative_infinity(self):
        table, base, _, _ = make_table()
        self.assertEqual(table.offset_at(Instant(-(10**18))), base)

    def test_skipped_wall_time_is_invalid(self):
        table, base, jump, _ = make_table()
        # the +1s leap at JUMP skips one wall second
        local_at_jump = jump.nanos + base.nanos + 1
        with self.assertRaises(InvalidWallTimeError):
            table.to_instant(wall_from_local(local_at_jump))

    def test_repeated_wall_time_is_ambiguous(self):
        table, base, _, fall = make_table()
        # the -1s fallback at FALL repeats one wall second
        local_repeated = fall.nanos + base.nanos + 500_000_000
        with self.assertRaises(AmbiguousWallTimeError):
            table.to_instant(wall_from_local(local_repeated))

    def test_empty_table_rejected(self):
        with self.assertRaises(ValueError):
            OffsetTable([])


if __name__ == "__main__":
    unittest.main()
