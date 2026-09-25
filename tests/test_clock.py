import unittest

from hlc.clock import HybridLogicalClock, is_far_future
from hlc.tag import Tag
from hlc.time_model import Duration, Instant


class TestTagOrdering(unittest.TestCase):
    def test_ordering_is_physical_epoch_logical(self):
        older = Tag(100, 9, 0, "a")
        restarted = Tag(100, 0, 1, "a")
        self.assertGreater(restarted, older)
        self.assertEqual(
            sorted([restarted, older]),
            [older, restarted],
        )

    def test_wire_roundtrip_and_rejection(self):
        tag = Tag(0x1F, 3, 2, "n7")
        self.assertEqual(Tag.from_wire(tag.to_wire()), tag)
        for bad in ("hlc/a.0.0/", "hlc/-1.0.0/n", "garbage", "x/1.2.3/n"):
            with self.assertRaises(ValueError):
                Tag.from_wire(bad)


class TestHybridLogicalClock(unittest.TestCase):
    def boot(self, max_logical=7, epoch=0, floor=-1):
        clock = HybridLogicalClock("n", epoch=epoch, max_logical=max_logical)
        clock.start_incarnation(
            epoch=epoch, wall=Instant(1_000_000), physical_floor=floor
        )
        return clock

    def test_same_millisecond_events_strictly_increase(self):
        clock = self.boot()
        tags = [clock.local_tick(Instant(1_000_000)) for _ in range(4)]
        self.assertEqual([t.logical for t in tags], [1, 2, 3, 4])
        self.assertTrue(all(t.physical == 1_000_000 for t in tags))
        self.assertTrue(all(b > a for a, b in zip(tags, tags[1:])))

    def test_wall_followed_then_wall_backward_keeps_monotonic(self):
        clock = self.boot()
        ahead = clock.local_tick(Instant(2_000_000))
        behind = clock.local_tick(Instant(1_500_000))
        self.assertEqual(ahead.physical, 2_000_000)
        self.assertEqual((behind.physical, behind.logical), (2_000_000, 1))
        self.assertGreater(behind, ahead)

    def test_receiving_leading_remote_is_overtaken(self):
        clock = self.boot()
        clock.local_tick(Instant(1_000_000))
        remote = Tag(1_000_005, 0, 0, "peer")
        merged = clock.receive_tick(Instant(1_000_000), remote)
        self.assertGreater(merged, remote)
        self.assertEqual((merged.physical, merged.logical), (1_000_005, 1))

    def test_logical_overflow_leaps_physical(self):
        clock = self.boot(max_logical=3)
        tags = [clock.local_tick(Instant(1_000_000)) for _ in range(4)]
        self.assertEqual(tags[-1].physical, 1_000_001)
        self.assertEqual(tags[-1].logical, 0)
        self.assertEqual(clock.stats.logical_overflow_leaps, 1)
        # strict increase is preserved across the leap
        self.assertTrue(all(b > a for a, b in zip(tags, tags[1:])))

    def test_restart_floor_dominates_behind_wall_with_new_epoch(self):
        clock = self.boot(epoch=1, floor=5_000_000)
        tag = clock.local_tick(Instant(1_000_000))
        self.assertEqual(tag.physical, 5_000_000)
        self.assertEqual(tag.epoch, 1)
        self.assertGreater(tag, Tag(5_000_000, 99, 0, "n"))

    def test_newer_remote_epoch_leaps_physical(self):
        clock = self.boot(epoch=0)
        clock.local_tick(Instant(1_000_000))
        remote = Tag(1_000_000, 0, 1, "peer")
        merged = clock.receive_tick(Instant(900_000), remote)
        self.assertGreater(merged, remote)
        self.assertEqual(merged.physical, 1_000_001)

    def test_far_future_predicate_uses_explicit_bound(self):
        local = Tag(1_000_000, 0, 0, "n")
        skew = Duration.millis(500)
        self.assertFalse(
            is_far_future(Tag(1_400_000, 0, 0, "p"), Instant(1_000_000), local, skew)
        )
        self.assertTrue(
            is_far_future(Tag(2_000_000, 0, 0, "p"), Instant(1_000_000), local, skew)
        )
        # bound measured from local high-water mark when it is ahead of wall
        self.assertFalse(
            is_far_future(Tag(1_600_000, 0, 0, "p"), Instant(1_000_000),
                          Tag(1_200_000, 0, 0, "n"), skew)
        )


if __name__ == "__main__":
    unittest.main()
