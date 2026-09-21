"""Tests for the incremental session-window index.

Run with either:
    python -m pytest tests/ -v
    python -m unittest discover -s tests -v
"""

from __future__ import annotations

import itertools
import random
import unittest

from session_windows import (
    CloseWindow,
    Disposition,
    Event,
    ReferenceSessionWindows,
    RetractEvent,
    RetractWindow,
    SessionWindowIndex,
    Upsert,
)


def member_sets(windows):
    """Id-agnostic, order-agnostic view of a window collection."""
    return sorted(
        (w.source, w.start_ms, w.end_ms, w.members) for w in windows
    )


class Downstream:
    """A consumer that materialises the changelog into a local view."""

    def __init__(self):
        self.live = {}
        self.closed = {}

    def apply(self, changes):
        for ch in changes:
            if isinstance(ch, Upsert):
                self.live[ch.window.id] = ch.window
            elif isinstance(ch, RetractWindow):
                del self.live[ch.window_id]
            elif isinstance(ch, CloseWindow):
                del self.live[ch.window.id]
                self.closed[ch.window.id] = ch.window
            else:  # pragma: no cover - defensive
                raise AssertionError(f"unknown change {ch!r}")


class MergeSplitTest(unittest.TestCase):
    def test_late_event_merges_then_retraction_splits(self):
        idx = SessionWindowIndex(gap_ms=5)

        d, ch = idx.apply(Event("s", 1, 0))
        self.assertEqual(d, Disposition.APPLIED)
        self.assertEqual(len(ch), 1)
        id0 = ch[0].window.id

        d, ch = idx.apply(Event("s", 2, 9))
        self.assertEqual(d, Disposition.APPLIED)
        id9 = ch[0].window.id
        self.assertNotEqual(id0, id9)
        self.assertEqual(len(idx.windows()), 2)

        # Late record at t=4 bridges the two windows: they must merge.
        d, ch = idx.apply(Event("s", 3, 4))
        self.assertEqual(d, Disposition.APPLIED)
        retracts = [c for c in ch if isinstance(c, RetractWindow)]
        upserts = [c for c in ch if isinstance(c, Upsert)]
        self.assertEqual(len(retracts), 1)
        self.assertEqual(len(upserts), 1)
        # The earliest window keeps its id; the later one is retracted.
        self.assertEqual(retracts[0].window_id, id9)
        merged = upserts[0].window
        self.assertEqual(merged.id, id0)
        self.assertEqual((merged.start_ms, merged.end_ms), (0, 9))
        self.assertEqual([m[0] for m in merged.members], [1, 3, 2])

        # Retracting the bridge splits the window again.
        d, ch = idx.apply(RetractEvent("s", 3))
        self.assertEqual(d, Disposition.RETRACTED)
        upserts = [c for c in ch if isinstance(c, Upsert)]
        self.assertEqual(len(upserts), 2)
        by_member = {u.window.members[0][0]: u.window for u in upserts}
        # Earliest fragment keeps the merged id; the other gets a fresh id.
        self.assertEqual(by_member[1].id, id0)
        self.assertNotIn(by_member[2].id, (id0, id9))
        self.assertEqual((by_member[1].start_ms, by_member[1].end_ms), (0, 0))
        self.assertEqual((by_member[2].start_ms, by_member[2].end_ms), (9, 9))

    def test_merge_keeps_earliest_windows_id_regardless_of_arrival(self):
        # The window at t=0 arrives *after* the one at t=9.
        idx = SessionWindowIndex(gap_ms=5)
        _, ch = idx.apply(Event("s", 2, 9))
        id9 = ch[0].window.id
        _, ch = idx.apply(Event("s", 1, 0))
        id0 = ch[0].window.id
        _, ch = idx.apply(Event("s", 3, 4))
        retracts = [c for c in ch if isinstance(c, RetractWindow)]
        upserts = [c for c in ch if isinstance(c, Upsert)]
        self.assertEqual(upserts[0].window.id, id0)
        self.assertEqual(retracts[0].window_id, id9)

    def test_all_arrival_orders_give_same_members(self):
        events = [Event("s", 1, 0), Event("s", 2, 9), Event("s", 3, 4)]
        expected = None
        for perm in itertools.permutations(events):
            idx = SessionWindowIndex(gap_ms=5)
            for ev in perm:
                idx.apply(ev)
            got = member_sets(idx.windows())
            if expected is None:
                expected = got
            self.assertEqual(got, expected, f"order {perm}")
        self.assertEqual(len(expected), 1)
        self.assertEqual(expected[0][1:], (0, 9, ((1, 0, None), (3, 4, None), (2, 9, None))))

    def test_all_arrival_orders_with_retraction_give_same_members(self):
        events = [Event("s", 1, 0), Event("s", 2, 9), Event("s", 3, 4)]
        expected = None
        for perm in itertools.permutations(events):
            idx = SessionWindowIndex(gap_ms=5)
            for ev in perm:
                idx.apply(ev)
            idx.apply(RetractEvent("s", 3))
            got = member_sets(idx.windows())
            if expected is None:
                expected = got
            self.assertEqual(got, expected, f"order {perm}")
        self.assertEqual(len(expected), 2)


class DispositionTest(unittest.TestCase):
    def test_duplicate_delivery_is_idempotent(self):
        idx = SessionWindowIndex(gap_ms=5)
        d1, ch1 = idx.apply(Event("s", 1, 10, "a"))
        d2, ch2 = idx.apply(Event("s", 1, 10, "a"))
        self.assertEqual(d1, Disposition.APPLIED)
        self.assertEqual(d2, Disposition.DUPLICATE)
        self.assertEqual(ch2, [])
        self.assertEqual(len(idx.windows()), 1)
        self.assertEqual(idx.stats()["events"], 1)

    def test_retract_unknown_event(self):
        idx = SessionWindowIndex(gap_ms=5)
        d, ch = idx.apply(RetractEvent("s", 99))
        self.assertEqual(d, Disposition.RETRACTION_UNKNOWN)
        self.assertEqual(ch, [])
        idx.apply(Event("s", 1, 0))
        d, ch = idx.apply(RetractEvent("other", 1))  # same seq, other source
        self.assertEqual(d, Disposition.RETRACTION_UNKNOWN)
        self.assertEqual(ch, [])

    def test_retract_only_event_removes_window(self):
        idx = SessionWindowIndex(gap_ms=5)
        _, ch = idx.apply(Event("s", 1, 0))
        wid = ch[0].window.id
        d, ch = idx.apply(RetractEvent("s", 1))
        self.assertEqual(d, Disposition.RETRACTED)
        self.assertEqual(len(ch), 1)
        self.assertIsInstance(ch[0], RetractWindow)
        self.assertEqual(ch[0].window_id, wid)
        self.assertEqual(ch[0].last.members, ((1, 0, None),))
        self.assertEqual(idx.windows(), [])
        # Retracting again is unknown, not an error.
        d, _ = idx.apply(RetractEvent("s", 1))
        self.assertEqual(d, Disposition.RETRACTION_UNKNOWN)

    def test_sources_are_independent(self):
        idx = SessionWindowIndex(gap_ms=5)
        idx.apply(Event("a", 1, 0))
        idx.apply(Event("b", 1, 3))  # same seq and close in time, other source
        self.assertEqual(len(idx.windows()), 2)
        idx.apply(RetractEvent("a", 1))
        wins = idx.windows()
        self.assertEqual(len(wins), 1)
        self.assertEqual(wins[0].source, "b")


class WatermarkTest(unittest.TestCase):
    def test_close_reclaim_and_late_rejection(self):
        idx = SessionWindowIndex(gap_ms=5, allowed_lateness_ms=10)
        idx.apply(Event("s", 1, 0))
        idx.apply(Event("s", 2, 100))

        # horizon = 14 - 10 = 4; window {0} needs end+gap=5 <= horizon: no.
        self.assertEqual(idx.advance_watermark(14), [])
        self.assertEqual(idx.stats()["events"], 2)

        # horizon = 5; window {0} is final and reclaimed.
        ch = idx.advance_watermark(15)
        self.assertEqual(len(ch), 1)
        self.assertIsInstance(ch[0], CloseWindow)
        self.assertEqual(ch[0].window.members, ((1, 0, None),))
        self.assertEqual(idx.stats()["events"], 1)
        self.assertEqual(len(idx.windows()), 1)

        # Regressive watermark is a no-op.
        self.assertEqual(idx.advance_watermark(10), [])

        # event_ms == horizon (5) is too late; horizon+1 is still admissible.
        d, ch = idx.apply(Event("s", 3, 5))
        self.assertEqual(d, Disposition.REJECTED_LATE)
        self.assertEqual(ch, [])
        d, _ = idx.apply(Event("s", 4, 6))
        self.assertEqual(d, Disposition.APPLIED)

        # The reclaimed event can no longer be retracted or re-delivered.
        d, _ = idx.apply(RetractEvent("s", 1))
        self.assertEqual(d, Disposition.RETRACTION_UNKNOWN)
        d, _ = idx.apply(Event("s", 1, 0))
        self.assertEqual(d, Disposition.REJECTED_LATE)

    def test_close_waits_for_gap(self):
        # A window is not final while an admissible event could still extend it.
        idx = SessionWindowIndex(gap_ms=5, allowed_lateness_ms=0)
        idx.apply(Event("s", 1, 0))
        self.assertEqual(idx.advance_watermark(4), [])  # end+gap=5 > 4
        ch = idx.advance_watermark(5)  # 5 <= 5 -> final
        self.assertEqual(len(ch), 1)
        self.assertIsInstance(ch[0], CloseWindow)


class BatchInterfaceTest(unittest.TestCase):
    def test_batch_matches_sequential(self):
        ops = [
            Event("s", 1, 0),
            Event("s", 2, 9),
            Event("s", 3, 4),
            Event("s", 3, 4),  # duplicate
            RetractEvent("s", 3),
            RetractEvent("s", 99),
            Event("t", 1, 100),
        ]
        a = SessionWindowIndex(gap_ms=5)
        b = SessionWindowIndex(gap_ms=5)
        batch = a.apply_all(ops)
        sequential = [(op, *b.apply(op)) for op in ops]
        self.assertEqual([r[0] for r in batch], ops)
        self.assertEqual(
            [(d, c) for _, d, c in batch], [(d, c) for _, d, c in sequential]
        )
        self.assertEqual(member_sets(a.windows()), member_sets(b.windows()))


class OraclePropertyTest(unittest.TestCase):
    def test_random_ops_match_reference_and_changelog_replays(self):
        rng = random.Random(20260921)
        gap, lateness = 5, 8
        idx = SessionWindowIndex(gap_ms=gap, allowed_lateness_ms=lateness)
        ref = ReferenceSessionWindows(gap_ms=gap, allowed_lateness_ms=lateness)
        downstream = Downstream()

        sources = ["a", "b", "c"]
        clock = 0
        for step in range(4000):
            roll = rng.random()
            if roll < 0.62:  # event, sometimes a duplicate re-delivery
                clock += rng.choice([0, 1, 2, 3, 7, 20])
                op = Event(
                    source=rng.choice(sources),
                    seq=rng.randrange(60),  # small space forces duplicates
                    event_ms=max(0, clock - rng.choice([0, 0, 0, 3, 30])),
                    value=rng.randrange(1000),
                )
            elif roll < 0.87:  # retraction, often of a non-existent event
                op = RetractEvent(
                    source=rng.choice(sources), seq=rng.randrange(70)
                )
            else:  # watermark advance
                wm = max(0, clock - rng.choice([0, 5, 15]))
                downstream.apply(idx.advance_watermark(wm))
                ref.advance_watermark(wm)
                continue

            disposition, changes = idx.apply(op)
            downstream.apply(changes)
            ref_disposition = ref.apply(op)
            self.assertEqual(
                disposition,
                ref_disposition,
                f"step {step}: op={op}",
            )
            # The changelog replay must always reproduce the live state.
            self.assertEqual(
                member_sets(downstream.live.values()),
                member_sets(idx.windows()),
                f"step {step}: downstream drift after {op}",
            )

        self.assertEqual(member_sets(idx.windows()), member_sets(ref.windows()))
        self.assertEqual(idx.stats()["events"], len(ref._events))

    def test_random_ops_without_watermark(self):
        rng = random.Random(7)
        idx = SessionWindowIndex(gap_ms=4)
        ref = ReferenceSessionWindows(gap_ms=4)
        for _ in range(3000):
            if rng.random() < 0.7:
                op = Event("s", rng.randrange(200), rng.randrange(500))
            else:
                op = RetractEvent("s", rng.randrange(200))
            d1, _ = idx.apply(op)
            d2 = ref.apply(op)
            self.assertEqual(d1, d2)
        self.assertEqual(member_sets(idx.windows()), member_sets(ref.windows()))


if __name__ == "__main__":
    unittest.main()
