import unittest

from logmerge import (
    Event,
    InfeasibleError,
    LogMerger,
    OffsetInterval,
    OffsetTable,
    WallClock,
)
from tests.test_merger import EVENTS, TABLE


class TestVersioning(unittest.TestCase):
    def setUp(self):
        self.merger = LogMerger(TABLE)
        self.v1 = self.merger.build(EVENTS, note="initial")

    def test_correction_publishes_new_version(self):
        v2 = self.merger.correct("c1", wall=WallClock(1030, "cache"))
        self.assertEqual(v2.number, 2)
        self.assertEqual(len(self.merger.versions), 2)
        diff = self.merger.diff(self.v1, v2)
        self.assertTrue(diff.order_unchanged)
        self.assertEqual(diff.became_necessary, frozenset({("w2", "c1")}))
        self.assertEqual(diff.became_uncertain, frozenset())

    def test_correction_can_move_events(self):
        v2 = self.merger.correct("c1", wall=WallClock(940, "cache"))
        self.assertEqual(v2.result.order, ("w1", "d1", "d2", "c1", "w2"))
        diff = self.merger.diff(self.v1, v2)
        self.assertFalse(diff.order_unchanged)
        self.assertEqual(diff.moved["c1"], (4, 3))
        self.assertEqual(diff.moved["w2"], (3, 4))

    def test_old_readers_keep_their_version(self):
        self.merger.correct("c1", wall=WallClock(1030, "cache"))
        old = self.merger.get_version(1)
        self.assertIs(old, self.v1)
        self.assertEqual(old.result.order, ("w1", "d1", "d2", "w2", "c1"))
        self.assertEqual(old.result.uncertain_pairs, frozenset({("c1", "w2")}))

    def test_contradictory_correction_rejected_without_new_version(self):
        with self.assertRaises(InfeasibleError):
            self.merger.correct("w1", wall=WallClock(2000, "web"))
        self.assertEqual(len(self.merger.versions), 1)

    def test_correct_unknown_event(self):
        with self.assertRaises(KeyError):
            self.merger.correct("ghost", wall=WallClock(1, "web"))

    def test_causal_correction(self):
        # Removing the d2 -> w2 edge makes d2/w2 mutually unordered? No:
        # local order + times still separate them; but c1 stays uncertain.
        v2 = self.merger.correct("w2", remove_causes=("d2",))
        self.assertIn(("d2", "w2"), v2.result.necessary_pairs)  # time-separated
        self.assertEqual(v2.result.uncertain_pairs, frozenset({("c1", "w2")}))


if __name__ == "__main__":
    unittest.main()
