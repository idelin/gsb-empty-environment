import unittest

from recur import (
    Ambiguous,
    AmbiguousWallTime,
    Gap,
    NonexistentWallTime,
    STD,
    DST,
    WallTime,
    make_table,
)


class OffsetTableTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_table()

    def test_standard_and_daylight_offsets(self):
        winter = self.table.resolve(WallTime.from_parts(2024, 1, 15, 12))
        summer = self.table.resolve(WallTime.from_parts(2024, 7, 15, 12))
        self.assertEqual(winter.offset_us, STD)
        self.assertEqual(summer.offset_us, DST)
        self.assertEqual(winter.instant.isoformat_utc(), "2024-01-15T04:00:00Z")
        self.assertEqual(summer.instant.isoformat_utc(), "2024-07-15T03:00:00Z")

    def test_wall_at_round_trip(self):
        for wall in (
            WallTime.from_parts(2024, 1, 15, 12),
            WallTime.from_parts(2024, 7, 15, 12, 30, 45, 1),
        ):
            resolved = self.table.resolve(wall)
            back, off = self.table.wall_at(resolved.instant)
            self.assertEqual(back, wall)
            self.assertEqual(off, resolved.offset_us)

    def test_spring_forward_gap_policies(self):
        missing = WallTime.from_parts(2024, 3, 10, 2, 30)
        with self.assertRaises(NonexistentWallTime):
            self.table.resolve(missing, gap=Gap.RAISE)

        fwd = self.table.resolve(missing, gap=Gap.FORWARD)
        self.assertEqual(fwd.kind, "gap")
        self.assertEqual(fwd.adjusted_to, WallTime.from_parts(2024, 3, 10, 3, 0))
        self.assertEqual(fwd.instant.isoformat_utc(), "2024-03-09T18:00:00Z")
        self.assertEqual(fwd.offset_us, DST)

        back = self.table.resolve(missing, gap=Gap.BACKWARD)
        self.assertEqual(back.kind, "gap")
        self.assertEqual(
            back.adjusted_to, WallTime.from_parts(2024, 3, 10, 1, 59, 59, 999_999)
        )
        self.assertEqual(back.offset_us, STD)

    def test_fall_back_overlap_policies(self):
        repeated = WallTime.from_parts(2024, 11, 3, 1, 30)
        early = self.table.resolve(repeated, ambiguous=Ambiguous.EARLIEST)
        late = self.table.resolve(repeated, ambiguous=Ambiguous.LATEST)
        both = self.table.resolve(repeated, ambiguous=Ambiguous.BOTH)

        self.assertEqual(early.kind, "ambiguous")
        self.assertEqual(early.offset_us, DST)
        self.assertEqual(late.offset_us, STD)
        self.assertEqual(late.instant.epoch_us - early.instant.epoch_us, 3600 * 1_000_000)
        self.assertEqual(len(both.instants), 2)
        self.assertEqual(both.instants[0], early.instant)
        self.assertEqual(both.instants[1], late.instant)

        with self.assertRaises(AmbiguousWallTime):
            self.table.resolve(repeated, ambiguous=Ambiguous.RAISE)

    def test_transition_edges_are_explicit(self):
        # 01:59 exists under STD; 03:00 is the first DST reading.
        before = self.table.resolve(WallTime.from_parts(2024, 3, 10, 1, 59))
        after = self.table.resolve(WallTime.from_parts(2024, 3, 10, 3, 0))
        self.assertEqual(before.kind, "unique")
        self.assertEqual(before.offset_us, STD)
        self.assertEqual(after.kind, "unique")
        self.assertEqual(after.offset_us, DST)

    def test_version_changes_with_content(self):
        from recur import make_modified_table

        self.assertNotEqual(self.table.version, make_modified_table().version)
        self.assertEqual(self.table.version, make_table().version)


if __name__ == "__main__":
    unittest.main()
