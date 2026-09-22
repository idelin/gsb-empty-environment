"""Scenario tests for the incremental SessionTracker."""

from itertools import permutations

import pytest

from session_windows import (
    AdvanceWatermark,
    ChangeKind,
    Disposition,
    Event,
    Retract,
    SessionTracker,
    Upsert,
)


def upsert(tracker, seq, ms, source="s", value=None):
    return tracker.apply(Upsert(Event(source, seq, ms, value)))


def changes_of_kind(result, kind):
    return [c for c in result.changes if c.kind is kind]


class TestMergeThenSplit:
    """gap=5: 0 and 9 are separate sessions; a late 4 merges them;
    retracting 4 splits them again."""

    def test_merge_then_split_changelog(self):
        t = SessionTracker(gap_ms=5)

        r1 = upsert(t, 1, 0)
        assert r1.disposition is Disposition.APPLIED
        assert [c.kind for c in r1.changes] == [ChangeKind.UPSERT]
        assert r1.changes[0].window.window_id == "s:0:1"

        r2 = upsert(t, 2, 9)
        assert [c.window.window_id for c in r2.changes] == ["s:9:2"]
        assert [w.window_id for w in t.active_windows()] == ["s:0:1", "s:9:2"]

        # Late event at 4 bridges the two windows.
        r3 = upsert(t, 3, 4)
        upserts = changes_of_kind(r3, ChangeKind.UPSERT)
        retracts = changes_of_kind(r3, ChangeKind.RETRACT)
        assert len(upserts) == 1
        merged = upserts[0].window
        # The merged window continues the id of the earliest member.
        assert merged.window_id == "s:0:1"
        assert [m.event_ms for m in merged.members] == [0, 4, 9]
        # The absorbed window's id is retracted with its last snapshot.
        assert [c.window.window_id for c in retracts] == ["s:9:2"]
        assert [m.event_ms for m in retracts[0].window.members] == [9]
        assert [w.window_id for w in t.active_windows()] == ["s:0:1"]

        # Retracting the bridge splits the window again.
        r4 = t.apply(Retract("s", 3))
        assert r4.disposition is Disposition.APPLIED
        assert changes_of_kind(r4, ChangeKind.RETRACT) == []
        parts = {c.window.window_id: c.window for c in r4.changes}
        assert set(parts) == {"s:0:1", "s:9:2"}
        assert [m.event_ms for m in parts["s:0:1"].members] == [0]
        assert [m.event_ms for m in parts["s:9:2"].members] == [9]

    def test_arrival_orders_converge(self):
        """Every arrival order of {0, 4, 9} yields the same final
        membership and window ids, before and after retracting 4."""
        finals = []
        after_split = []
        for order in permutations([(1, 0), (2, 9), (3, 4)]):
            t = SessionTracker(gap_ms=5)
            for seq, ms in order:
                upsert(t, seq, ms)
            finals.append(t.active_windows())
            t.apply(Retract("s", 3))
            after_split.append(t.active_windows())
        for windows in finals[1:]:
            assert windows == finals[0]
        assert len(finals[0]) == 1
        assert [m.event_ms for m in finals[0][0].members] == [0, 4, 9]
        for windows in after_split[1:]:
            assert windows == after_split[0]
        assert [w.window_id for w in after_split[0]] == ["s:0:1", "s:9:2"]


class TestWatermarkAndLateness:
    def test_window_close_and_reclamation(self):
        t = SessionTracker(gap_ms=5, allowed_lateness_ms=10)
        upsert(t, 1, 0)
        upsert(t, 2, 20)

        # horizon = 15 - 10 = 5; window {0} ends at 0, 0 + gap(5) < 5 is false.
        r = t.apply(AdvanceWatermark("s", 15))
        assert r.disposition is Disposition.WATERMARK_ADVANCED
        assert r.changes == ()

        # horizon = 6; window {0} can no longer change -> finalized.
        r = t.apply(AdvanceWatermark("s", 16))
        finals = changes_of_kind(r, ChangeKind.FINALIZE)
        assert [c.window.window_id for c in finals] == ["s:0:1"]
        assert [w.window_id for w in t.active_windows()] == ["s:20:2"]

        # Inside the lateness horizon -> rejected with an explicit result.
        r = upsert(t, 3, 1)
        assert r.disposition is Disposition.TOO_LATE
        assert r.changes == ()

        # Exactly at the horizon is still admissible.
        r = upsert(t, 4, 6)
        assert r.disposition is Disposition.APPLIED

        # State of the finalized window was reclaimed.
        assert t.apply(Retract("s", 1)).disposition is Disposition.NOT_FOUND

    def test_watermark_regression_ignored(self):
        t = SessionTracker(gap_ms=5)
        upsert(t, 1, 0)
        assert t.apply(AdvanceWatermark("s", 10)).disposition is (
            Disposition.WATERMARK_ADVANCED
        )
        assert t.apply(AdvanceWatermark("s", 5)).disposition is (
            Disposition.WATERMARK_IGNORED
        )
        assert t.apply(AdvanceWatermark("s", 10)).disposition is (
            Disposition.WATERMARK_IGNORED
        )

    def test_watermark_is_per_source(self):
        t = SessionTracker(gap_ms=5)
        upsert(t, 1, 0, source="a")
        upsert(t, 2, 0, source="b")
        t.apply(AdvanceWatermark("a", 100))
        assert [w.source for w in t.active_windows()] == ["b"]
        assert upsert(t, 3, 0, source="a").disposition is Disposition.TOO_LATE
        # b is unaffected by a's watermark.
        assert upsert(t, 4, 0, source="b").disposition is Disposition.APPLIED
        assert upsert(t, 5, 3, source="b").disposition is Disposition.APPLIED


class TestDedupAndRetract:
    def test_duplicate_delivery_is_noop(self):
        t = SessionTracker(gap_ms=5)
        upsert(t, 1, 0, value="x")
        r = upsert(t, 1, 0, value="x")
        assert r.disposition is Disposition.DUPLICATE
        assert r.changes == ()
        assert len(t.active_windows()) == 1

    def test_update_same_key_replaces(self):
        t = SessionTracker(gap_ms=5)
        upsert(t, 1, 0, value="x")
        r = upsert(t, 1, 7, value="y")
        assert r.disposition is Disposition.UPDATED
        kinds = {c.kind: c.window for c in r.changes}
        assert kinds[ChangeKind.RETRACT].window_id == "s:0:1"
        assert kinds[ChangeKind.UPSERT].window_id == "s:7:1"
        (w,) = t.active_windows()
        assert w.members[0].value == "y" and w.members[0].event_ms == 7

    def test_retract_missing_event(self):
        t = SessionTracker(gap_ms=5)
        r = t.apply(Retract("s", 99))
        assert r.disposition is Disposition.NOT_FOUND
        assert r.changes == ()

    def test_retract_last_member_removes_window(self):
        t = SessionTracker(gap_ms=5)
        upsert(t, 1, 0)
        r = t.apply(Retract("s", 1))
        assert r.disposition is Disposition.APPLIED
        assert [c.kind for c in r.changes] == [ChangeKind.RETRACT]
        assert r.changes[0].window.window_id == "s:0:1"
        assert t.active_windows() == ()

    def test_retract_endpoint_shrinks_window(self):
        t = SessionTracker(gap_ms=5)
        upsert(t, 1, 0)
        upsert(t, 2, 3)
        r = t.apply(Retract("s", 1))
        # The first member is gone: old id dies, remainder gets a fresh id.
        kinds = {c.kind: c.window for c in r.changes}
        assert kinds[ChangeKind.RETRACT].window_id == "s:0:1"
        assert kinds[ChangeKind.UPSERT].window_id == "s:3:2"


class TestBatch:
    def test_apply_all_matches_sequential(self):
        ops = [
            Upsert(Event("s", 1, 0)),
            Upsert(Event("s", 2, 9)),
            Upsert(Event("s", 3, 4)),
            Retract("s", 3),
            AdvanceWatermark("s", 100),
            Upsert(Event("s", 4, 50)),
        ]
        t1 = SessionTracker(gap_ms=5)
        batch = t1.apply_all(ops)
        t2 = SessionTracker(gap_ms=5)
        sequential = [t2.apply(op) for op in ops]
        assert list(batch.results) == sequential
        assert list(batch.changes) == [c for r in sequential for c in r.changes]
        assert t1.active_windows() == t2.active_windows()

    def test_batch_result_dispositions(self):
        t = SessionTracker(gap_ms=5)
        batch = t.apply_all(
            [
                Upsert(Event("s", 1, 0)),
                Upsert(Event("s", 1, 0)),
                Retract("s", 7),
            ]
        )
        assert [r.disposition for r in batch.results] == [
            Disposition.APPLIED,
            Disposition.DUPLICATE,
            Disposition.NOT_FOUND,
        ]
