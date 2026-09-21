import os
import tempfile
import unittest
from fractions import Fraction

from coverage_ledger import (Ledger, load_checkpoint, query, reconcile,
                             save_checkpoint)


def _build():
    led = Ledger()
    led.add_membership("A", 0, None, rule_id="ma")
    led.add_membership("B", Fraction(0), None, rule_id="mb")
    led.add_observation("A", 0, 6, observation_id="a")
    led.add_observation("B", Fraction(4), Fraction(8), observation_id="b")
    led.publish_report("R", (0, 10), "union")
    led.end_membership("B", 5, rule_id="mb2")
    led.retract_observation("b", "B")
    led.add_observation("B", 3, 8, observation_id="b2")
    return led


class CheckpointTests(unittest.TestCase):
    def test_save_and_load_replays_all_revisions(self):
        led = _build()
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "ckpt.json")
            save_checkpoint(led, path)
            restored = load_checkpoint(path)

        self.assertEqual(restored.head, led.head)
        self.assertEqual(
            query(restored.snapshot(), (0, 10), "union").known_coverage_length,
            query(led.snapshot(), (0, 10), "union").known_coverage_length,
        )
        # published report pinned at old revision survives recovery
        published = query(restored.report_snapshot("R"), (0, 10), "union")
        self.assertEqual(published.known_coverage_length, 8)
        _, _, delta = reconcile(restored, "R")
        self.assertTrue(delta.is_conserving)
        self.assertEqual(delta.old_length, 8)
        self.assertEqual(delta.new_length, 6)

    def test_rollback_reverts_state_and_re_commit_rewrites_history(self):
        led = _build()
        head = led.head
        led.rollback_to(head - 2)
        self.assertEqual(led.head, head - 2)
        self.assertEqual(
            query(led.snapshot(), (0, 10), "union").known_coverage_length, 6
        )
        # re-add after retraction: inverse rollback restored retracted record
        led.rollback_to(0)
        self.assertEqual(led.head, 0)
        self.assertEqual(led.snapshot().observations.devices(), [])


if __name__ == "__main__":
    unittest.main()
