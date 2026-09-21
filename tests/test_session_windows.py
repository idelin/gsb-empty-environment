"""Functional tests for the incremental engine."""
import itertools

import pytest

from session_windows import (
    AdvanceWatermark,
    ChangeKind,
    Disposition,
    Event,
    Retract,
    SessionWindows,
)


def feed(sw, source, seq, t, value=None):
    return sw.feed(Event(source, seq, t, value))


def member_times(window):
    return [e.event_ms for e in window.members]


# ------------------------------------------------------------ merge / split

def test_late_event_merges_two_windows():
    sw = SessionWindows(gap_ms=5)
    feed(sw, "s", 1, 0)
    feed(sw, "s", 2, 9)
    assert [w.id for w in sw.snapshot()] == ["s@0", "s@9"]

    rev = feed(sw, "s", 3, 4)  # late record bridges the gap
    kinds = [c.kind for c in rev.changes]
    assert kinds == [ChangeKind.RETRACT, ChangeKind.UPSERT]
    assert rev.changes[0].window_id == "s@9"  # absorbed id is retracted
    merged = rev.changes[1].window
    assert merged.id == "s@0"  # earliest window's id survives
    assert member_times(merged) == [0, 4, 9]
    assert [w.id for w in sw.snapshot()] == ["s@0"]


def test_retract_bridge_splits_window():
    sw = SessionWindows(gap_ms=5)
    feed(sw, "s", 1, 0)
    feed(sw, "s", 2, 9)
    feed(sw, "s", 3, 4)
    rev = sw.retract("s", 3)
    assert rev.disposition is Disposition.RETRACTED
    kinds = [c.kind for c in rev.changes]
    assert kinds == [ChangeKind.UPSERT, ChangeKind.UPSERT]
    assert [c.window_id for c in rev.changes] == ["s@0", "s@9"]
    assert [w.id for w in sw.snapshot()] == ["s@0", "s@9"]


def test_arrival_orders_converge():
    orders = [
        [(1, 0), (2, 9), (3, 4)],  # in-order-ish, late bridge
        [(3, 4), (1, 0), (2, 9)],  # bridge first
        [(2, 9), (3, 4), (1, 0)],  # reverse
    ]
    finals = []
    for order in orders:
        sw = SessionWindows(gap_ms=5)
        for seq, t in order:
            feed(sw, "s", seq, t)
        finals.append([(w.id, member_times(w)) for w in sw.snapshot()])
    assert finals[0] == finals[1] == finals[2] == [("s@0", [0, 4, 9])]


def test_all_permutations_converge():
    events = [(1, 0), (2, 4), (3, 9), (4, 30), (5, 34)]
    finals = set()
    for perm in itertools.permutations(events):
        sw = SessionWindows(gap_ms=5)
        for seq, t in perm:
            feed(sw, "s", seq, t)
        finals.add(tuple((w.id, tuple(member_times(w))) for w in sw.snapshot()))
    assert finals == {(("s@0", (0, 4, 9)), ("s@30", (30, 34)))}


def test_one_event_merges_many_windows():
    sw = SessionWindows(gap_ms=5)
    for i, t in enumerate([0, 10, 20, 30]):
        feed(sw, "s", i, t)
    assert len(sw.snapshot()) == 4
    rev = feed(sw, "s", 99, 15)  # |15-10|=5 and |20-15|=5: bridges both
    assert rev.stats.sessions_merged == 2
    assert [w.id for w in sw.snapshot()] == ["s@0", "s@10", "s@30"]
    assert member_times(sw.snapshot()[1]) == [10, 15, 20]
    rev = feed(sw, "s", 100, 12)  # lands inside the s@10..20 window
    assert rev.stats.sessions_merged == 1
    rev = feed(sw, "s", 101, 25)  # bridges s@10 and s@30
    assert rev.stats.sessions_merged == 2
    assert [w.id for w in sw.snapshot()] == ["s@0", "s@10"]


def test_split_into_three_segments():
    sw = SessionWindows(gap_ms=2)
    for i, t in enumerate([0, 2, 4, 6, 8]):
        feed(sw, "s", i, t)
    rev = sw.retract("s", 2)  # remove t=4 -> [0,2] [6,8]
    assert rev.stats.segments_produced == 2
    assert [w.id for w in sw.snapshot()] == ["s@0", "s@6"]
    sw2 = SessionWindows(gap_ms=2)
    for i, t in enumerate([0, 2, 4, 6, 8]):
        feed(sw2, "s", i, t)
    rev = sw2.retract("s", 1)  # remove t=2 -> [0] [4,6,8]
    assert [w.id for w in sw2.snapshot()] == ["s@0", "s@4"]


def test_late_event_before_window_start_moves_id():
    sw = SessionWindows(gap_ms=5)
    feed(sw, "s", 1, 10)
    rev = feed(sw, "s", 2, 6)  # joins s@10 but becomes the new start
    kinds = [(c.kind, c.window_id) for c in rev.changes]
    assert kinds == [(ChangeKind.RETRACT, "s@10"), (ChangeKind.UPSERT, "s@6")]
    assert [w.id for w in sw.snapshot()] == ["s@6"]


def test_retract_earliest_event_moves_id():
    sw = SessionWindows(gap_ms=5)
    feed(sw, "s", 1, 0)
    feed(sw, "s", 2, 4)
    rev = sw.retract("s", 1)
    kinds = [(c.kind, c.window_id) for c in rev.changes]
    assert kinds == [(ChangeKind.RETRACT, "s@0"), (ChangeKind.UPSERT, "s@4")]


# ------------------------------------------------------------- dispositions

def test_duplicate_delivery_is_ignored():
    sw = SessionWindows(gap_ms=5)
    feed(sw, "s", 1, 0)
    rev = feed(sw, "s", 1, 0)
    assert rev.disposition is Disposition.DUPLICATE
    assert rev.changes == ()
    rev = feed(sw, "s", 1, 0, value="different")  # first write wins
    assert rev.disposition is Disposition.DUPLICATE
    assert len(sw.snapshot()) == 1


def test_retract_unknown_event():
    sw = SessionWindows(gap_ms=5)
    rev = sw.retract("s", 42)
    assert rev.disposition is Disposition.RETRACT_UNKNOWN
    assert rev.changes == ()
    feed(sw, "s", 1, 0)
    sw.retract("s", 1)
    rev = sw.retract("s", 1)  # already retracted
    assert rev.disposition is Disposition.RETRACT_UNKNOWN


def test_retract_last_event_retracts_window():
    sw = SessionWindows(gap_ms=5)
    feed(sw, "s", 1, 0)
    rev = sw.retract("s", 1)
    assert [(c.kind, c.window_id) for c in rev.changes] == [
        (ChangeKind.RETRACT, "s@0")
    ]
    assert sw.snapshot() == []


# ---------------------------------------------------- watermark / lateness

def test_watermark_closes_and_reclaims():
    sw = SessionWindows(gap_ms=5, allowed_lateness_ms=10)
    feed(sw, "s", 1, 0)
    feed(sw, "s", 2, 4)
    feed(sw, "s", 3, 100)
    rev = sw.advance_watermark("s", 20)
    # window [0,4]: end+gap = 9 < 20-10 = 10 -> closed; [100] stays open
    assert [(c.kind, c.window_id) for c in rev.changes] == [
        (ChangeKind.CLOSE, "s@0")
    ]
    closed = rev.changes[0].window
    assert closed.closed and member_times(closed) == [0, 4]
    assert rev.stats.sessions_reclaimed == 1
    assert rev.stats.events_reclaimed == 2
    assert [w.id for w in sw.snapshot()] == ["s@100"]

    # Late event beyond allowed lateness: explicit drop.
    rev = feed(sw, "s", 9, 9)
    assert rev.disposition is Disposition.DROPPED_LATE
    assert rev.changes == ()
    # Event inside the allowed lateness band is still accepted.
    rev = feed(sw, "s", 10, 11)
    assert rev.disposition is Disposition.ACCEPTED
    # Retracting a reclaimed event is an explicit no-op.
    rev = sw.retract("s", 1)
    assert rev.disposition is Disposition.RETRACT_UNKNOWN


def test_watermark_regression_is_noop():
    sw = SessionWindows(gap_ms=5, allowed_lateness_ms=0)
    feed(sw, "s", 1, 0)
    sw.advance_watermark("s", 100)
    rev = sw.advance_watermark("s", 50)
    assert rev.changes == ()
    rev = feed(sw, "s", 2, 99)
    assert rev.disposition is Disposition.DROPPED_LATE


def test_sources_are_independent():
    sw = SessionWindows(gap_ms=5)
    feed(sw, "a", 1, 0)
    feed(sw, "b", 1, 3)  # would merge if sources were mixed
    assert {w.id for w in sw.snapshot()} == {"a@0", "b@3"}
    sw.advance_watermark("a", 1000)
    assert [w.id for w in sw.snapshot()] == ["b@3"]


# ------------------------------------------------------------- batch / misc

def test_batch_equals_single_ops():
    ops = [
        Event("s", 1, 0),
        Event("s", 2, 9),
        Event("s", 3, 4),
        Retract("s", 3),
        AdvanceWatermark("s", 100),
    ]
    sw1 = SessionWindows(gap_ms=5, allowed_lateness_ms=5)
    batch = sw1.apply(ops)
    sw2 = SessionWindows(gap_ms=5, allowed_lateness_ms=5)
    revs2 = [sw2.apply([op]).revisions[0] for op in ops]
    assert [r.disposition for r in batch.revisions] == [r.disposition for r in revs2]
    assert batch.changes == tuple(c for r in revs2 for c in r.changes)
    assert sw1.snapshot() == sw2.snapshot()
    assert [r.rev for r in batch.revisions] == [1, 2, 3, 4, 5]


def test_downstream_consumer_replays_changelog():
    """A consumer applying only retract/upsert/close stays in sync."""
    sw = SessionWindows(gap_ms=3, allowed_lateness_ms=2)
    downstream = {}
    ops = [
        Event("s", 1, 0), Event("s", 2, 10), Event("s", 3, 5),
        Event("s", 4, 7), Retract("s", 3), Event("s", 5, 20),
        AdvanceWatermark("s", 15), Retract("s", 2), Event("s", 6, 1),
    ]
    for op in ops:
        rev = sw.apply([op]).revisions[0]
        for c in rev.changes:
            if c.kind is ChangeKind.RETRACT:
                downstream.pop(c.window_id, None)
            else:
                downstream[c.window_id] = c.window
        open_windows = {w.id: w for w in sw.snapshot()}
        assert {k: v for k, v in downstream.items() if not v.closed} == open_windows
