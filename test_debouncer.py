"""Deterministic replay tests for debouncer.

Everything is driven by explicit event time; no real sleeps.
The same recorded event scripts are replayed:
  * uninterrupted (baseline),
  * paused/restored at several checkpoints,
and the two must produce byte-identical confirmation output.
"""

import copy
import unittest

from debouncer import Confirmed, Debouncer

THRESHOLD = 3
SRC = "sensor"

# (value or None for unknown, ts)
SCRIPT = [("A", 0), ("B", 1), ("B", 4)]
SCRIPT_WITH_UNKNOWN = [("A", 0), ("B", 1), (None, 3), ("B", 4)]


def replay(script, threshold=THRESHOLD, source=SRC, snapshots=None):
    """Replay a script; optionally restore from a snapshot at a given step.

    snapshots: {step_index: snapshot_dict} -> just before applying event i,
               the debouncer is replaced by Debouncer.restore(snapshot).
    Returns list of Confirmed tuples collected by draining after each step.
    """
    deb = Debouncer(threshold)
    out = []
    for i, (value, ts) in enumerate(script):
        if snapshots and i in snapshots:
            deb = Debouncer.restore(copy.deepcopy(snapshots[i]))
        deb.ingest(source, value, ts)
        out.extend(deb.drain())
    return out


def record_checkpoint(script, after_step):
    """Run through events [0, after_step) and return (snapshot, debouncer)."""
    deb = Debouncer(THRESHOLD)
    for value, ts in script[:after_step]:
        deb.ingest(SRC, value, ts)
    return deb.save(), deb


class ThresholdScenario(unittest.TestCase):
    def test_b_confirmed_at_four(self):
        # A@0, B@1, B@4 -> exactly one confirmation: B, matured at t=4,
        # anchored at t=1 (the duplicate B@4 must not extend the window).
        out = replay(SCRIPT)
        self.assertEqual(out, [Confirmed(1, SRC, "B", at=4, since=1)])

    def test_time_maturity_on_advance_without_event(self):
        # Same result obtained purely by advancing the clock after B@1.
        deb = Debouncer(THRESHOLD)
        deb.ingest(SRC, "A", 0)
        deb.ingest(SRC, "B", 1)
        self.assertEqual(deb.drain(), [])
        deb.advance(3)  # 3 - 1 < 3 -> not yet
        self.assertEqual(deb.drain(), [])
        deb.advance(4)  # 4 - 1 == 3 -> exactly at the threshold
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "B", at=4, since=1)])

    def test_unknown_at_three_blocks_confirmation(self):
        # Unknown at t=3 discards the B candidate; B@4 restarts the window,
        # which then needs to remain stable until t=7.
        deb = Debouncer(THRESHOLD)
        for value, ts in SCRIPT_WITH_UNKNOWN:
            deb.ingest(SRC, value, ts)
        self.assertEqual(deb.drain(), [])
        self.assertEqual(deb.state_of(SRC),
                         {"candidate": "B", "since": 4,
                          "last_confirmed": "__unset__"})
        deb.advance(7)
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "B", at=7, since=4)])

    def test_duplicate_does_not_extend_or_emit_again(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest(SRC, "A", 0)
        deb.advance(3)
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "A", at=3, since=0)])
        for ts in (3, 4, 5):
            deb.ingest(SRC, "A", ts)  # repeated observations
        self.assertEqual(deb.drain(), [])  # never re-emitted


class SameTimeOrdering(unittest.TestCase):
    def test_later_event_at_same_ts_wins(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest(SRC, "A", 0)
        deb.ingest(SRC, "B", 5)
        deb.ingest(SRC, "C", 5)  # same ts, later in ingestion order
        self.assertEqual(deb.state_of(SRC)["candidate"], "C")
        self.assertEqual(deb.state_of(SRC)["since"], 5)
        deb.advance(8)
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "C", at=8, since=5)])

    def test_unknown_at_same_ts_clears_candidate(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest(SRC, "A", 0)
        deb.ingest(SRC, "B", 5)
        deb.ingest_unknown(SRC, 5)  # unknown last in the same timestamp
        self.assertIsNone(deb.state_of(SRC)["candidate"])
        deb.advance(100)
        self.assertEqual(deb.drain(), [])

    def test_unknown_followed_by_value_at_same_ts_anchors_at_ts(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest(SRC, "A", 0)
        deb.ingest_unknown(SRC, 5)
        deb.ingest(SRC, "C", 5)
        self.assertEqual(deb.state_of(SRC)["since"], 5)
        deb.advance(7)
        self.assertEqual(deb.drain(), [])
        deb.advance(8)
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "C", at=8, since=5)])


class SourceIsolation(unittest.TestCase):
    def test_states_are_independent(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s1", "A", 0)
        deb.ingest("s2", "X", 0)
        deb.ingest("s1", "B", 1)  # s1 flickering does not touch s2
        deb.ingest_unknown("s1", 2)
        deb.advance(3)
        # s2 has held X since t=0; s1 has no candidate after the unknown.
        self.assertEqual(deb.drain(), [Confirmed(1, "s2", "X", at=3, since=0)])
        self.assertIsNone(deb.state_of("s1")["candidate"])

    def test_multiple_sources_mature_together_in_insertion_order(self):
        deb = Debouncer(THRESHOLD)
        deb.ingest("s1", "A", 1)
        deb.ingest("s2", "B", 1)
        deb.advance(4)
        self.assertEqual(deb.drain(), [
            Confirmed(1, "s1", "A", 4, 1),
            Confirmed(2, "s2", "B", 4, 1),
        ])


class BackwardsTime(unittest.TestCase):
    def test_events_and_advance_reject_backwards_time(self):
        deb = Debouncer(THRESHOLD, now=10)
        with self.assertRaises(ValueError):
            deb.ingest(SRC, "A", 9)
        with self.assertRaises(ValueError):
            deb.advance(5)


# ---------------------------------------------------------------------------
# Checkpoint restore tests for SCRIPT = A@0, B@1, B@4.
# Baseline uninterrupted output is a single B confirmed at t=4.
# ---------------------------------------------------------------------------

class CheckpointCandidateJustStarted(unittest.TestCase):
    """Checkpoint 1: candidate B has only just begun (after B@1)."""

    def test_restore_preserves_unmet_candidate_and_confirms(self):
        snapshot, _ = record_checkpoint(SCRIPT, after_step=2)  # A@0, B@1
        # Resume and feed only the remaining event B@4.
        deb = Debouncer.restore(snapshot)
        self.assertEqual(deb.state_of(SRC),
                         {"candidate": "B", "since": 1,
                          "last_confirmed": "__unset__"})
        deb.ingest(SRC, "B", 4)
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "B", at=4, since=1)])

    def test_restore_then_unknown_interrupts_preserved_candidate(self):
        snapshot, _ = record_checkpoint(SCRIPT, after_step=2)
        deb = Debouncer.restore(snapshot)
        deb.ingest_unknown(SRC, 3)  # unknown after restore breaks timing
        deb.ingest(SRC, "B", 4)
        self.assertEqual(deb.drain(), [])


class CheckpointAboutToConfirm(unittest.TestCase):
    """Checkpoint 2: saved just before the stability duration is met."""

    def test_restore_and_advance_to_boundary_confirms(self):
        deb0 = Debouncer(THRESHOLD)
        deb0.ingest(SRC, "A", 0)
        deb0.ingest(SRC, "B", 1)
        deb0.advance(3)  # elapsed 2 < 3: nothing yet
        self.assertEqual(deb0.drain(), [])
        deb = Debouncer.restore(deb0.save())
        self.assertEqual(deb.drain(), [])  # still nothing on resume
        deb.advance(4)  # crossing the threshold after restore
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "B", at=4, since=1)])

    def test_restore_with_no_further_time_keeps_candidate(self):
        deb0 = Debouncer(THRESHOLD)
        deb0.ingest(SRC, "B", 1)
        deb0.advance(3)
        deb = Debouncer.restore(deb0.save())
        self.assertEqual(deb.state_of(SRC),
                         {"candidate": "B", "since": 1,
                          "last_confirmed": "__unset__"})
        self.assertEqual(deb.drain(), [])


class CheckpointConfirmedNotConsumed(unittest.TestCase):
    """Checkpoint 3: confirmation generated but still in the outbox."""

    def test_confirmation_survives_restore_and_delivered_once(self):
        deb0 = Debouncer(THRESHOLD)
        deb0.ingest(SRC, "A", 0)
        deb0.ingest(SRC, "B", 1)
        deb0.ingest(SRC, "B", 4)
        self.assertEqual(list(deb0.pending), [Confirmed(1, SRC, "B", 4.0, 1.0)])

        deb = Debouncer.restore(deb0.save())  # saved BEFORE draining
        self.assertEqual(list(deb.pending), [Confirmed(1, SRC, "B", 4.0, 1.0)])
        first = deb.drain()
        self.assertEqual(first, [Confirmed(1, SRC, "B", at=4, since=1)])
        # Idempotent consumption; a second save/restore cannot resend it.
        deb2 = Debouncer.restore(deb.save())
        self.assertEqual(deb2.drain(), [])
        # Further duplicate observations never re-confirm the same value.
        deb2.ingest(SRC, "B", 5)
        self.assertEqual(deb2.drain(), [])

    def test_undrained_item_survives_json_roundtrip(self):
        deb0 = Debouncer(THRESHOLD)
        deb0.ingest(SRC, "B", 1)
        deb0.advance(4)
        deb = Debouncer.from_json(deb0.to_json())
        self.assertEqual(deb.drain(), [Confirmed(1, SRC, "B", 4, 1)])
        self.assertEqual(Debouncer.from_json(deb.to_json()).drain(), [])


class ReplayEquivalence(unittest.TestCase):
    """Paused/resumed replay must equal uninterrupted replay."""

    def assert_same_as_baseline(self, script, checkpoint_step):
        baseline = replay(script)
        # Snapshot is taken by a separate run at the checkpoint, then injected
        # into the replay immediately before event checkpoint_step is applied.
        pre = Debouncer(THRESHOLD)
        for value, ts in script[:checkpoint_step]:
            pre.ingest(SRC, value, ts)
        resumed = replay(script, snapshots={checkpoint_step: pre.save()})
        self.assertEqual(resumed, baseline)

    def test_checkpoint_after_first_event(self):
        self.assert_same_as_baseline(SCRIPT, 1)

    def test_checkpoint_candidate_just_started(self):
        self.assert_same_as_baseline(SCRIPT, 2)

    def test_checkpoint_before_last_event_with_unknown_script(self):
        # Baseline emits nothing within SCRIPT_WITH_UNKNOWN; resumed must match.
        self.assert_same_as_baseline(SCRIPT_WITH_UNKNOWN, 3)

    def test_json_checkpoint_matches_baseline(self):
        baseline = replay(SCRIPT)
        pre = Debouncer(THRESHOLD)
        pre.ingest(SRC, "A", 0)
        pre.ingest(SRC, "B", 1)
        deb = Debouncer.from_json(pre.to_json())
        deb.ingest(SRC, "B", 4)
        self.assertEqual(deb.drain(), baseline)


if __name__ == "__main__":
    unittest.main(verbosity=2)
