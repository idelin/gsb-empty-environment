"""Reproducible event-replay tests for the debouncer.

No wall clock is involved anywhere: every test drives time explicitly via
ingest()/advance_time(), so timing boundaries are exact and deterministic.
"""

import json
import unittest

from debouncer import Confirmation, Debouncer


def replay(events, threshold=3.0, start=None):
    """Feed a [(ts, source, value), ...] script; return the debouncer."""
    deb = start if start is not None else Debouncer(threshold)
    for item in events:
        if item[0] == "advance":
            deb.advance_time(item[1])
        else:
            ts, source, value = item
            deb.ingest(source, value, ts)
    return deb


def roundtrip(deb):
    """save -> JSON -> restore (JSON proves the snapshot is serializable)."""
    return Debouncer.restore(json.loads(json.dumps(deb.save())))


class ThresholdScenarioTests(unittest.TestCase):
    """The spec scenario: threshold 3, A@0, B@1, B@4 confirms B."""

    def test_candidate_stable_for_threshold_confirms(self):
        deb = replay([(0, "s", "A"), (1, "s", "B"), (4, "s", "B")])
        out = deb.drain()
        self.assertEqual(out, [Confirmation("s", "B", since=1.0, confirmed_at=4.0)])

    def test_advance_time_also_confirms(self):
        deb = replay([(0, "s", "A"), (1, "s", "B"), ("advance", 4)])
        self.assertEqual([c.value for c in deb.drain()], ["B"])

    def test_nothing_confirmed_before_threshold(self):
        deb = replay([(0, "s", "A"), (1, "s", "B"), ("advance", 3.999)])
        self.assertEqual(deb.pending, 0)

    def test_unknown_at_3_interrupts_candidate(self):
        deb = replay([(0, "s", "A"), (1, "s", "B"), (3, "s", None), ("advance", 4)])
        self.assertEqual(deb.drain(), [])  # B@1's timer was destroyed at t=3

    def test_unknown_is_not_a_candidate_and_timer_restarts_fresh(self):
        deb = replay(
            [
                (0, "s", "A"),
                (1, "s", "B"),
                (3, "s", None),   # interrupt
                (4, "s", "B"),    # brand-new candidate starting at 4
                ("advance", 6.999),
            ]
        )
        self.assertEqual(deb.pending, 0)
        deb.advance_time(7)
        self.assertEqual(
            deb.drain(), [Confirmation("s", "B", since=4.0, confirmed_at=7.0)]
        )


class CheckpointRestoreTests(unittest.TestCase):
    """Checkpoint at each interesting position; verify resume semantics."""

    SCRIPT_HEAD = [(0, "s", "A"), (1, "s", "B")]

    def test_checkpoint_right_after_candidate_started(self):
        deb = replay(self.SCRIPT_HEAD)          # candidate B since 1, now=1
        restored = roundtrip(deb)
        replay([(4, "s", "B")], start=restored)  # duplicate B carries time to 4
        self.assertEqual(
            restored.drain(), [Confirmation("s", "B", 1.0, 4.0)]
        )
        # Original instance is untouched by the restore/continue.
        self.assertEqual(deb.pending, 0)

    def test_checkpoint_just_before_confirmation(self):
        deb = replay(self.SCRIPT_HEAD + [("advance", 3.999)])
        restored = roundtrip(deb)
        restored.advance_time(4)                 # crosses the boundary
        self.assertEqual(
            restored.drain(), [Confirmation("s", "B", 1.0, 4.0)]
        )

    def test_checkpoint_after_confirm_before_consume(self):
        deb = replay(self.SCRIPT_HEAD + [(4, "s", "B")])  # B confirmed, in outbox
        self.assertEqual(deb.pending, 1)
        restored = roundtrip(deb)
        # The unconsumed confirmation survives the restore, exactly once.
        self.assertEqual(
            restored.drain(), [Confirmation("s", "B", 1.0, 4.0)]
        )
        # Further time advancement must NOT re-emit the confirmed change.
        restored.advance_time(100)
        replay([(150, "s", "B")], start=restored)  # duplicate of confirmed
        self.assertEqual(restored.drain(), [])

    def test_consumed_confirmation_is_not_resurrected_by_restore(self):
        deb = replay(self.SCRIPT_HEAD + [(4, "s", "B")])
        deb.drain()                              # consume BEFORE checkpoint
        restored = roundtrip(deb)
        self.assertEqual(restored.drain(), [])
        restored.advance_time(50)
        self.assertEqual(restored.drain(), [])

    def test_pending_candidate_duration_not_lost_across_restore(self):
        deb = replay([(0, "s", "A"), (2, "s", "B")])  # B since 2, due at 5
        restored = roundtrip(deb)
        restored.advance_time(4.999)
        self.assertEqual(restored.pending, 0)
        restored.advance_time(5)
        self.assertEqual(
            restored.drain(), [Confirmation("s", "B", 2.0, 5.0)]
        )


class IsolationAndDuplicateTests(unittest.TestCase):
    def test_sources_are_isolated(self):
        deb = replay(
            [
                (0, "s1", "A"),
                (1, "s2", "X"),
                (2, "s1", "B"),   # s1 candidate B since 2
                (3, "s2", None),  # interrupts only s2
                ("advance", 5),   # s1's B due at 5; s2 has nothing
            ]
        )
        self.assertEqual(deb.drain(), [Confirmation("s1", "B", 2.0, 5.0)])
        # s2 can still start over independently.
        replay([(6, "s2", "Y"), ("advance", 9)], start=deb)
        self.assertEqual(deb.drain(), [Confirmation("s2", "Y", 6.0, 9.0)])

    def test_duplicate_does_not_extend_stable_timer(self):
        deb = replay(
            [
                (0, "s", "A"),
                (2, "s", "A"),    # duplicate: timer must stay anchored at 0
                ("advance", 3),
            ]
        )
        self.assertEqual(deb.drain(), [Confirmation("s", "A", 0.0, 3.0)])

    def test_duplicate_of_confirmed_value_is_ignored(self):
        deb = replay([(0, "s", "A"), ("advance", 3), (5, "s", "A"), ("advance", 9)])
        self.assertEqual(deb.drain(), [Confirmation("s", "A", 0.0, 3.0)])

    def test_revert_to_confirmed_value_cancels_candidate(self):
        deb = replay(
            [
                (0, "s", "A"),
                ("advance", 3),   # A confirmed
                (4, "s", "B"),    # candidate B
                (5, "s", "A"),    # back to confirmed value: cancel B
                ("advance", 8),
            ]
        )
        self.assertEqual(deb.drain(), [Confirmation("s", "A", 0.0, 3.0)])


class OrderingTests(unittest.TestCase):
    def test_same_timestamp_events_apply_in_arrival_order(self):
        deb = replay([(1, "s", "A"), (1, "s", "B"), ("advance", 4)])
        self.assertEqual(deb.drain(), [Confirmation("s", "B", 1.0, 4.0)])

    def test_event_on_boundary_preempts_confirmation(self):
        # Candidate B since 1 is due at 4, but C@4 lands first (same-instant
        # events apply before confirmations are evaluated).
        deb = replay([(1, "s", "B"), (4, "s", "C"), ("advance", 7)])
        self.assertEqual(deb.drain(), [Confirmation("s", "C", 4.0, 7.0)])

    def test_backwards_time_rejected(self):
        deb = replay([(5, "s", "A")])
        with self.assertRaises(ValueError):
            deb.ingest("s", "B", 4)
        with self.assertRaises(ValueError):
            deb.advance_time(4)


if __name__ == "__main__":
    unittest.main()
