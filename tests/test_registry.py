"""Registry hot-reload and local-invalidation tests."""

import unittest

from tztable import (
    ConversionService,
    Duration,
    TableRegistry,
    Transition,
    TransitionTable,
    WallClock,
)


def make_v1():
    # Fallback on 2024-10-27T00:00Z: +2 -> +1 (overlap 01:00..02:00 local).
    return TransitionTable(
        version="v1",
        base_offset=Duration.of(hours=2),
        transitions=(
            Transition(
                utc=WallClock.of(2024, 10, 27).to_instant_equiv(),
                offset_after=Duration.of(hours=1),
            ),
        ),
    )


def make_v2():
    # Hot update: the fallback is abolished, offset stays +2.
    return TransitionTable(version="v2", base_offset=Duration.of(hours=2))


class TestHotReload(unittest.TestCase):
    def setUp(self):
        self.registry = TableRegistry()
        self.registry.publish(make_v1())
        self.service = ConversionService(self.registry)
        self.ambiguous = WallClock.of(2024, 10, 27, 1, 30)

    def test_hot_update_binds_new_version_without_touching_history(self):
        before = self.service.wall_to_instants(self.ambiguous)
        self.assertEqual(len(before), 2)

        self.registry.publish(make_v2())
        self.assertEqual(self.registry.current_version, "v2")

        # Current lookups use the new table: no more overlap.
        after = self.service.wall_to_instants(self.ambiguous)
        self.assertEqual(len(after), 1)
        self.assertEqual(after[0].offset, Duration.of(hours=2))

        # Historical snapshot is unchanged and still answers as before.
        pinned = self.service.wall_to_instants(self.ambiguous, version="v1")
        self.assertEqual([c.instant for c in pinned], [c.instant for c in before])
        self.assertEqual(len(self.registry.snapshot("v1").transitions), 1)

    def test_local_invalidation_is_scoped_to_version(self):
        self.service.wall_to_instants(self.ambiguous)  # cached under v1
        self.assertEqual(self.service.cache_size, 1)

        self.registry.publish(make_v2())
        fresh = self.service.wall_to_instants(self.ambiguous)  # cache miss
        self.assertEqual(len(fresh), 1)
        self.assertEqual(self.service.cache_size, 2)  # v1 entry untouched

        self.service.invalidate("v2")
        self.assertEqual(self.service.cache_size, 1)
        self.service.invalidate()
        self.assertEqual(self.service.cache_size, 0)

    def test_duplicate_version_rejected(self):
        with self.assertRaises(ValueError):
            self.registry.publish(make_v1())

    def test_unknown_version_raises(self):
        with self.assertRaises(LookupError):
            self.registry.snapshot("nope")


if __name__ == "__main__":
    unittest.main()
