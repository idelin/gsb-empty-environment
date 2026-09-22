"""Tests for the source-indexed RLE status history.

Every edit sequence is validated through three paths:
  1. direct query of the current segments (StatusHistory.segments)
  2. replaying all emitted patches onto the initial (empty) snapshot
  3. recomputing from the surviving raw events (build_segments)
"""

import unittest

from status_rle import (
    Event,
    StatusHistory,
    apply_patch,
    build_segments,
)


def replay(patches):
    snapshot = []
    for p in patches:
        snapshot = apply_patch(snapshot, p)
    return snapshot


class TrackedHistory:
    """StatusHistory + record of patches and surviving raw events."""

    def __init__(self):
        self.history = StatusHistory()
        self.patches = []
        self.events = {}

    def insert(self, source, seq, time, status):
        self.patches.append(self.history.insert(source, seq, time, status))
        self.events[(source, seq)] = Event(time, source, seq, status)

    def retract(self, source, seq):
        self.patches.append(self.history.retract(source, seq))
        del self.events[(source, seq)]

    def correct(self, source, seq, **kw):
        self.patches.append(self.history.correct(source, seq, **kw))
        old = self.events[(source, seq)]
        self.events[(source, seq)] = Event(
            kw.get("time", old.time), source, seq, kw.get("status", old.status))

class ThreePathMixin:
    def check(self, tracked):
        direct = tracked.history.segments()
        patched = replay(tracked.patches)
        rebuilt = build_segments(tracked.events.values())
        self.assertEqual(direct, patched, "patch replay diverged from query")
        self.assertEqual(direct, rebuilt, "query diverged from raw recompute")


class SpecScenarioTest(ThreePathMixin, unittest.TestCase):
    def test_split_and_restore(self):
        t = TrackedHistory()
        t.insert("sensor", 1, 0, "A")
        t.insert("sensor", 2, 2, "A")
        t.insert("sensor", 3, 5, "B")

        segs = t.history.segments()
        self.assertEqual(len(segs), 2)
        self.assertEqual((segs[0].status, segs[0].start, segs[0].end), ("A", 0, 5))
        self.assertEqual(segs[0].refs(), (("sensor", 1), ("sensor", 2)))
        self.assertEqual((segs[1].status, segs[1].start, segs[1].end), ("B", 5, None))
        self.check(t)

        # Insert B@1: the A segment splits around it.
        t.insert("sensor", 4, 1, "B")
        got = [(s.status, s.start, s.end) for s in t.history.segments()]
        self.assertEqual(got, [("A", 0, 1), ("B", 1, 2), ("A", 2, 5), ("B", 5, None)])
        self.check(t)

        # Retract B@1: original structure restored, A@2 still indexed.
        t.retract("sensor", 4)
        segs = t.history.segments()
        self.assertEqual(segs, [  # structurally identical to the original
            segs[0].__class__("A", 0, 5, segs[0].provenance),
            segs[1].__class__("B", 5, None, segs[1].provenance),
        ])
        self.assertEqual(len(segs), 2)
        self.assertIn(("sensor", 2), segs[0].refs())
        self.assertEqual(t.history.status_at(3), "A")
        self.check(t)

    def test_merge_keeps_duplicate_evidence(self):
        t = TrackedHistory()
        t.insert("a", 1, 0, "ON")
        t.insert("a", 2, 3, "ON")   # duplicate status inside the run
        t.insert("a", 3, 7, "ON")   # another one
        t.insert("a", 4, 9, "OFF")
        segs = t.history.segments()
        self.assertEqual(len(segs), 2)
        self.assertEqual(segs[0].refs(), (("a", 1), ("a", 2), ("a", 3)))
        self.check(t)


class EdgeCaseTest(ThreePathMixin, unittest.TestCase):
    def test_retract_first_event(self):
        t = TrackedHistory()
        t.insert("s", 1, 0, "A")
        t.insert("s", 2, 4, "B")
        t.insert("s", 3, 8, "C")
        t.retract("s", 1)
        got = [(s.status, s.start, s.end) for s in t.history.segments()]
        self.assertEqual(got, [("B", 4, 8), ("C", 8, None)])
        self.assertIsNone(t.history.status_at(0))  # nothing before first event
        self.check(t)

    def test_retract_last_event(self):
        t = TrackedHistory()
        t.insert("s", 1, 0, "A")
        t.insert("s", 2, 4, "B")
        t.retract("s", 2)
        segs = t.history.segments()
        self.assertEqual(len(segs), 1)
        self.assertEqual((segs[0].status, segs[0].start, segs[0].end), ("A", 0, None))
        self.check(t)

    def test_same_time_conflict(self):
        t = TrackedHistory()
        t.insert("alpha", 1, 5, "A")
        t.insert("beta", 1, 5, "B")   # same time; canonical order decides
        segs = t.history.segments()
        self.assertEqual(len(segs), 1)
        self.assertEqual(segs[0].status, "B")  # ("beta",1) sorts after ("alpha",1)
        self.assertEqual(segs[0].refs(), (("alpha", 1), ("beta", 1)))
        self.assertEqual(segs[0].effective_event.ref, ("beta", 1))
        self.check(t)

        # Retracting the winner reveals the overridden record.
        t.retract("beta", 1)
        segs = t.history.segments()
        self.assertEqual(len(segs), 1)
        self.assertEqual(segs[0].status, "A")
        self.assertEqual(segs[0].refs(), (("alpha", 1),))
        self.check(t)

    def test_single_record(self):
        t = TrackedHistory()
        t.insert("s", 1, 3, "A")
        segs = t.history.segments()
        self.assertEqual(len(segs), 1)
        self.assertEqual((segs[0].start, segs[0].end), (3, None))
        self.assertEqual(t.history.status_at(100), "A")
        self.assertIsNone(t.history.status_at(2))
        self.check(t)

    def test_empty_stream(self):
        t = TrackedHistory()
        self.assertEqual(t.history.segments(), [])
        self.assertIsNone(t.history.status_at(0))
        self.assertIsNone(t.history.segment_at(0))
        with self.assertRaises(KeyError):
            t.history.retract("s", 1)
        t.insert("s", 1, 0, "A")
        t.retract("s", 1)
        self.assertEqual(t.history.segments(), [])
        self.assertFalse(t.patches[-1].is_empty)  # removal patch was emitted
        self.check(t)

    def test_correction_atomic_patch(self):
        t = TrackedHistory()
        t.insert("s", 1, 0, "A")
        t.insert("s", 2, 5, "B")
        n_before = len(t.patches)
        t.correct("s", 2, time=2, status="C")  # move + change in one patch
        self.assertEqual(len(t.patches), n_before + 1)
        got = [(s.status, s.start, s.end) for s in t.history.segments()]
        self.assertEqual(got, [("A", 0, 2), ("C", 2, None)])
        self.check(t)

    def test_duplicate_identity_rejected(self):
        h = StatusHistory()
        h.insert("s", 1, 0, "A")
        with self.assertRaises(ValueError):
            h.insert("s", 1, 9, "B")


class PatchReplayTest(ThreePathMixin, unittest.TestCase):
    def test_long_mixed_sequence(self):
        t = TrackedHistory()
        ops = [
            ("ins", "s", 1, 10, "A"), ("ins", "s", 2, 20, "B"),
            ("ins", "s", 3, 30, "B"), ("ins", "s", 4, 15, "C"),
            ("ins", "s", 5, 25, "A"), ("cor", "s", 2, {"time": 18}),
            ("ins", "s", 6, 20, "D"), ("ret", "s", 4),
            ("cor", "s", 5, {"status": "B"}), ("ret", "s", 1),
            ("ret", "s", 6), ("ins", "s", 7, 40, "C"),
            ("ret", "s", 7), ("ret", "s", 3),
        ]
        for op in ops:
            kind, source, seq = op[0], op[1], op[2]
            if kind == "ins":
                t.insert(source, seq, op[3], op[4])
            elif kind == "ret":
                t.retract(source, seq)
            else:
                t.correct(source, seq, **op[3])
            self.check(t)  # three paths agree after every single step

    def test_stale_patch_rejected(self):
        h = StatusHistory()
        h.insert("s", 1, 0, "A")
        p1 = h.insert("s", 2, 5, "B")
        h.insert("s", 3, 9, "C")
        with self.assertRaises(ValueError):
            apply_patch(h.segments(), p1)  # p1 targets an older snapshot

    def test_patch_invert_roundtrip(self):
        h = StatusHistory()
        h.insert("s", 1, 0, "A")
        before = h.segments()
        p = h.insert("s", 2, 5, "B")
        after = h.segments()
        self.assertEqual(apply_patch(after, p.invert()), before)


if __name__ == "__main__":
    unittest.main()
