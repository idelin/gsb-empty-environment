import unittest

from hlc.time_model import (
    Duration,
    Instant,
    OffsetTable,
    SyntheticClock,
    WallReading,
)


class TestTimeModel(unittest.TestCase):
    def test_duration_units_and_arithmetic(self):
        self.assertEqual(Duration.millis(2), Duration.micros_of(2_000))
        self.assertEqual(Duration.seconds(1), Duration.micros_of(1_000_000))
        self.assertEqual(Duration.micros_of(3) + Duration.micros_of(4), Duration.micros_of(7))
        self.assertEqual(Duration.micros_of(3) - Duration.micros_of(10), Duration.micros_of(-7))
        self.assertTrue(Duration.micros_of(-1).is_negative())
        self.assertEqual(abs(Duration.micros_of(-5)), Duration.micros_of(5))

    def test_instant_geometry_keeps_types_separate(self):
        t0 = Instant.from_millis(10)
        t1 = t0.add(Duration.millis(2))
        self.assertEqual(t1, Instant.from_micros(12_000))
        self.assertEqual(t0.distance_to(t1), Duration.millis(2))
        self.assertTrue(t1.is_after(t0))
        with self.assertRaises(TypeError):
            t0.add(2_000)  # an int is not a Duration
        with self.assertRaises(TypeError):
            t0.distance_to(12_000)  # an int is not an Instant

    def test_wall_reading_is_raw_only(self):
        reading = WallReading(1_000)
        self.assertEqual(reading.plus_raw(5), WallReading(1_005))

    def test_offset_table_precise_conversion(self):
        table = OffsetTable.create(
            segments=[(0, 2_000, 100), (2_000, 4_000, -50)],
            default_offset_micros=10,
        )
        self.assertEqual(table.convert(WallReading(1_999)), Instant(2_099))
        self.assertEqual(table.convert(WallReading(2_000)), Instant(1_950))
        self.assertEqual(table.convert(WallReading(9_000)), Instant(9_010))
        with self.assertRaises(ValueError):
            OffsetTable.create(segments=[(0, 10, 0), (5, 20, 0)])

    def test_synthetic_clock_wall_goes_backward_via_table(self):
        clock = SyntheticClock(WallReading(1_000), OffsetTable.create())
        self.assertEqual(clock.now(), Instant(1_000))
        clock.advance_raw(1_000)  # hardware only moves forward
        self.assertEqual(clock.now(), Instant(2_000))
        clock.install_table(OffsetTable.create(default_offset_micros=-1_500))
        # observed wall time jumps backward even though the counter advanced
        self.assertEqual(clock.now(), Instant(500))


if __name__ == "__main__":
    unittest.main()
