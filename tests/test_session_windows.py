"""Tests for the incremental session-window store.

The randomized suites cross-check the incremental engine against
``session_windows.reference.compute_windows`` (an independent recompute-from-
scratch oracle) and against a downstream mirror that is fed *only* the
retract/upsert change stream.
"""

from __future__ import annotations

import itertools
import random

from session_windows import (
    CloseWindow,
    Disposition,
    Event,
    Retract,
    RetractWindow,
    SessionWindowStore,
    Upsert,
    Window,
    compute_windows,
)

GAP = 5


def ev(seq, t, source="a", value=None):
    return Event(source=source, seq=seq, event_ms=t, value=value)


def members_of(window: Window) -> tuple[int, ...]:
    return tuple(m.event_ms for m in window.members)


class Mirror:
    """Downstream consumer driven purely by the change stream."""

    def __init__(self):
        self.live: dict[int, Window] = {}
        self.closed: dict[int, Window] = {}

    def apply(self, changes):
        for c in changes:
            if isinstance(c, Upsert):
                self.live[c.window.id] = c.window
            elif isinstance(c, RetractWindow):
                del self.live[c.window_id]
            elif isinstance(c, CloseWindow):
                del self.live[c.window.id]
                self.closed[c.window.id] = c.window
            else:  # pragma: no cover
                raise AssertionError(c)


def assert_mirror_matches(store: SessionWindowStore, mirror: Mirror):
    assert mirror.live == {w.id: w for w in store.snapshot()}


def assert_matches_reference(store: SessionWindowStore, live_events, gap_ms=GAP):
    got = [(w.source, w.start_ms, w.end_ms, members_of(w)) for w in store.snapshot()]
    want = [
        (w.source, w.start_ms, w.end_ms, tuple(m.event_ms for m in w.members))
        for w in compute_windows(live_events, gap_ms)
    ]
    assert got == want


# ---------------------------------------------------------------------------
# The merge / split scenario from the spec
# ---------------------------------------------------------------------------

def test_late_record_merges_then_retract_splits():
    store = SessionWindowStore(gap_ms=GAP)
    mirror = Mirror()

    r1 = store.apply(ev(1, 0))
    r2 = store.apply(ev(2, 9))
    mirror.apply(r1.changes + r2.changes)
    w0, w9 = store.snapshot()
    assert (w0.start_ms, w0.end_ms) == (0, 0)
    assert (w9.start_ms, w9.end_ms) == (9, 9)

    # The backfilled record at t=4 bridges both sessions.
    r3 = store.apply(ev(3, 4))
    assert r3.disposition is Disposition.APPLIED
    kinds = [type(c) for c in r3.changes]
    assert kinds == [RetractWindow, Upsert]
    retract, upsert = r3.changes
    # Merge continuity: the window with the smaller start keeps its id.
    assert retract.window_id == w9.id
    assert upsert.window.id == w0.id
    assert members_of(upsert.window) == (0, 4, 9)
    mirror.apply(r3.changes)
    assert_mirror_matches(store, mirror)

    # Retracting the bridge splits the session again.
    r4 = store.apply(Retract("a", 3))
    assert r4.disposition is Disposition.RETRACTED
    kinds = [type(c) for c in r4.changes]
    assert kinds == [Upsert, Upsert]
    left, right = r4.changes
    # Split continuity: fragment holding the original start keeps the id.
    assert left.window.id == w0.id
    assert members_of(left.window) == (0,)
    assert right.window.id not in (w0.id, w9.id)  # fresh id
    assert members_of(right.window) == (9,)
    mirror.apply(r4.changes)
    assert_mirror_matches(store, mirror)


def test_final_membership_is_order_independent():
    orders = list(itertools.permutations([0, 4, 9]))
    assert len(orders) == 6  # covers the "three arrival orders" and more
    finals = set()
    for order in orders:
        store = SessionWindowStore(gap_ms=GAP)
        for seq, t in enumerate(order):
            store.apply(ev(seq, t))
        finals.add(tuple(members_of(w) for w in store.snapshot()))
        # ... and after withdrawing the middle record.
        seq_of_4 = order.index(4)
        store.apply(Retract("a", seq_of_4))
        finals.add(tuple(members_of(w) for w in store.snapshot()))
    assert finals == {((0, 4, 9),), ((0,), (9,))}


# ---------------------------------------------------------------------------
# Duplicates, conflicts, unknown retractions
# ---------------------------------------------------------------------------

def test_duplicate_delivery_is_a_noop():
    store = SessionWindowStore(gap_ms=GAP)
    store.apply(ev(1, 0, value="x"))
    r = store.apply(ev(1, 0, value="x"))
    assert r.disposition is Disposition.DUPLICATE
    assert r.changes == ()
    assert len(store.snapshot()) == 1


def test_conflicting_redelivery_is_rejected():
    store = SessionWindowStore(gap_ms=GAP)
    store.apply(ev(1, 0, value="x"))
    r = store.apply(ev(1, 7, value="y"))
    assert r.disposition is Disposition.CONFLICT
    assert r.changes == ()
    assert members_of(store.snapshot()[0]) == (0,)


def test_retract_unknown_and_double_retract():
    store = SessionWindowStore(gap_ms=GAP)
    r = store.apply(Retract("a", 99))
    assert r.disposition is Disposition.RETRACT_UNKNOWN
    assert r.changes == ()

    store.apply(ev(1, 0))
    assert store.apply(Retract("a", 1)).disposition is Disposition.RETRACTED
    assert store.snapshot() == []
    r = store.apply(Retract("a", 1))
    assert r.disposition is Disposition.RETRACT_UNKNOWN


def test_retract_last_member_removes_window():
    store = SessionWindowStore(gap_ms=GAP)
    store.apply(ev(1, 0))
    wid = store.snapshot()[0].id
    r = store.apply(Retract("a", 1))
    assert [type(c) for c in r.changes] == [RetractWindow]
    assert r.changes[0].window_id == wid
    assert store.snapshot() == []


def test_three_way_split():
    store = SessionWindowStore(gap_ms=GAP)
    for seq, t in enumerate([0, 4, 8, 12, 16]):
        store.apply(ev(seq, t))
    assert len(store.snapshot()) == 1
    keep_id = store.snapshot()[0].id
    store.apply(Retract("a", 1))  # remove t=4
    store.apply(Retract("a", 3))  # remove t=12
    got = [members_of(w) for w in store.snapshot()]
    assert got == [(0,), (8,), (16,)]
    ids = [w.id for w in store.snapshot()]
    assert ids[0] == keep_id
    assert len(set(ids)) == 3


# ---------------------------------------------------------------------------
# Watermark, allowed lateness, reclamation
# ---------------------------------------------------------------------------

def test_watermark_closes_windows_and_reclaims_state():
    store = SessionWindowStore(gap_ms=GAP, allowed_lateness_ms=10)
    store.apply(ev(1, 0))
    store.apply(ev(2, 100))

    changes = store.advance_watermark("a", 20)  # horizon = 10
    assert len(changes) == 1
    close = changes[0]
    assert isinstance(close, CloseWindow)
    assert (close.window.start_ms, close.window.end_ms) == (0, 0)

    # Reclaimed: only the t=100 session and its event remain.
    stats = store.stats()["a"]
    assert stats["events"] == stats["times"] == stats["sessions"] == 1

    # Records older than the horizon are explicitly rejected...
    r = store.apply(ev(3, 9))
    assert r.disposition is Disposition.LATE_REJECTED
    assert r.changes == ()
    # ...while a record exactly on the horizon is still admissible.
    assert store.apply(ev(4, 10)).disposition is Disposition.APPLIED

    # The retracted identity of a reclaimed event is forgotten.
    assert store.apply(Retract("a", 1)).disposition is Disposition.RETRACT_UNKNOWN


def test_session_stays_open_while_still_mergeable():
    store = SessionWindowStore(gap_ms=GAP, allowed_lateness_ms=10)
    store.apply(ev(1, 0))
    # horizon = 4; session end + gap = 5 >= 4, so a late event could still join.
    assert store.advance_watermark("a", 14) == ()
    r = store.apply(ev(2, 5))
    assert r.disposition is Disposition.APPLIED
    assert members_of(store.snapshot()[0]) == (0, 5)
    # horizon = 11 > end + gap = 10: now the session is finalized and closed.
    changes = store.advance_watermark("a", 21)
    assert len(changes) == 1 and isinstance(changes[0], CloseWindow)
    assert store.stats()["a"]["sessions"] == 0


def test_watermark_is_monotonic():
    store = SessionWindowStore(gap_ms=GAP, allowed_lateness_ms=0)
    store.apply(ev(1, 0))
    assert len(store.advance_watermark("a", 100)) == 1
    assert store.advance_watermark("a", 50) == ()   # ignored
    assert store.advance_watermark("a", 100) == ()  # ignored
    assert store.apply(ev(2, 99)).disposition is Disposition.LATE_REJECTED


# ---------------------------------------------------------------------------
# Batch interface
# ---------------------------------------------------------------------------

def test_batch_matches_sequential_application():
    ops = [ev(1, 0), ev(2, 9), ev(3, 4), Retract("a", 3), ev(4, 30)]
    seq_store = SessionWindowStore(gap_ms=GAP)
    seq_changes = [c for op in ops for c in seq_store.apply(op).changes]

    batch_store = SessionWindowStore(gap_ms=GAP)
    result = batch_store.apply_batch(ops)

    assert [r.disposition for r in result.results] == [
        Disposition.APPLIED, Disposition.APPLIED, Disposition.APPLIED,
        Disposition.RETRACTED, Disposition.APPLIED,
    ]
    # Same final state; same change stream modulo freshly assigned ids.
    assert [(w.start_ms, w.end_ms, members_of(w)) for w in batch_store.snapshot()] == [
        (w.start_ms, w.end_ms, members_of(w)) for w in seq_store.snapshot()
    ]
    assert [type(c) for c in result.changes] == [type(c) for c in seq_changes]


# ---------------------------------------------------------------------------
# Sources are independent
# ---------------------------------------------------------------------------

def test_sources_are_isolated():
    store = SessionWindowStore(gap_ms=GAP)
    store.apply(ev(1, 0, source="a"))
    store.apply(ev(1, 3, source="b"))  # same seq, different source
    store.apply(ev(2, 100, source="a"))
    assert len(store.windows("a")) == 2
    assert len(store.windows("b")) == 1
    store.apply(Retract("b", 1))
    assert store.windows("b") == []
    assert len(store.windows("a")) == 2


# ---------------------------------------------------------------------------
# Randomized cross-check against the reference oracle + change-stream mirror
# ---------------------------------------------------------------------------

def test_randomized_against_reference_and_mirror():
    rng = random.Random(20260922)
    for trial in range(30):
        store = SessionWindowStore(gap_ms=GAP)
        mirror = Mirror()
        live: dict[int, Event] = {}
        next_seq = 0
        for _ in range(300):
            if live and rng.random() < 0.35:
                victim = rng.choice(list(live))
                res = store.apply(Retract("a", victim))
                assert res.disposition is Disposition.RETRACTED
                del live[victim]
            else:
                e = ev(next_seq, rng.randrange(0, 400), value=next_seq)
                next_seq += 1
                res = store.apply(e)
                assert res.disposition is Disposition.APPLIED
                live[e.seq] = e
                # duplicate redelivery must not disturb anything
                dup = store.apply(e)
                assert dup.disposition is Disposition.DUPLICATE
                assert dup.changes == ()
            mirror.apply(res.changes)
            assert_mirror_matches(store, mirror)
        assert_matches_reference(store, live.values())


def test_randomized_multi_source():
    rng = random.Random(7)
    store = SessionWindowStore(gap_ms=3)
    mirror = Mirror()
    live: dict[tuple[str, int], Event] = {}
    for step in range(600):
        source = rng.choice("abc")
        seq = step  # unique per source is not required, but keep it simple
        if live and rng.random() < 0.3:
            key = rng.choice(list(live))
            res = store.apply(Retract(*key))
            assert res.disposition is Disposition.RETRACTED
            del live[key]
        else:
            e = ev(seq, rng.randrange(0, 200), source=source, value=step)
            res = store.apply(e)
            assert res.disposition is Disposition.APPLIED
            live[(source, seq)] = e
        mirror.apply(res.changes)
    assert_mirror_matches(store, mirror)
    assert_matches_reference(store, live.values(), gap_ms=3)
