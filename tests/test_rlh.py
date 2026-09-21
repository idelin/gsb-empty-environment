import random

import pytest

from rlh import HistoryStore, Record, SegmentedView, build_runs

A0 = Record(0, "A", "gw1", 1)
A2 = Record(2, "A", "gw1", 2)
B5 = Record(5, "B", "gw1", 3)
B1 = Record(1, "B", "gw2", 10)


def keys(segments):
    return [(s.state, s.start, s.end, tuple(r.ref for r in s.sources)) for s in segments]


def raw_keys(records):
    return [(r.state, r.start, r.end, r.sources) for r in build_runs(records)]


def ids(segments):
    return [s.sid for s in segments]


def test_initial_two_segments_half_open():
    store = HistoryStore([A0, A2, B5])
    seg = store.segments()
    assert keys(seg) == [
        ("A", 0, 5, (A0.ref, A2.ref)),
        ("B", 5, None, (B5.ref,)),
    ]
    assert store.segment_at(4).state == "A"
    assert store.segment_at(5).state == "B"  # half-open: B owns its start


def test_insert_splits_and_retract_restores_exactly():
    store = HistoryStore([A0, A2, B5])
    original = store.segments()
    original_ids = ids(original)
    view = SegmentedView(original)

    patch = store.insert(B1)
    assert {s.sid for s in patch.removed} == {original_ids[0]}
    assert len(patch.added) == 3  # A[0,1), B[1,2), A[2,5); trailing B kept
    view.apply(patch)

    assert keys(store.segments()) == [
        ("A", 0, 1, (A0.ref,)),
        ("B", 1, 2, (B1.ref,)),
        ("A", 2, 5, (A2.ref,)),
        ("B", 5, None, (B5.ref,)),
    ]
    assert keys(view.segments()) == keys(store.segments())

    # Split segments still trace back to the original A events.
    assert store.trace_at(0) == (A0,)
    assert store.trace_at(2) == (A2,)

    patch = store.retract("gw2", 10)
    view.apply(patch)

    # Exact structural restoration: same content and same segment ids;
    # A@2 is back in the source index of the A segment.
    assert ids(store.segments()) == original_ids
    assert keys(store.segments()) == keys(original)
    assert keys(view.segments()) == keys(store.segments())
    assert store.trace(original_ids[0]) == (A0, A2)
    assert store.record("gw1", 2) == A2


def test_first_and_last_event_retraction():
    store = HistoryStore([A0, A2, B5])
    view = SegmentedView(store.segments())

    p = store.retract("gw1", 3)  # retract the last (trailing) event
    view.apply(p)
    assert keys(store.segments()) == [("A", 0, None, (A0.ref, A2.ref))]
    assert keys(view.segments()) == keys(store.segments())

    p = store.retract("gw1", 1)  # retract the first event; A@2 survives
    view.apply(p)
    assert keys(store.segments()) == [("A", 2, None, (A2.ref,))]
    assert keys(view.segments()) == keys(store.segments())


def test_same_time_conflict_newer_seq_wins_loser_kept():
    store = HistoryStore([A0, A2, B5])
    view = SegmentedView(store.segments())

    c0 = Record(0, "C", "gw3", 99)
    p = store.insert(c0)  # same time as A0, higher seq -> C wins
    view.apply(p)
    conflict = store.conflict_at(0)
    assert conflict is not None
    assert conflict.winner == c0
    assert conflict.losers == (A0,)
    assert store.segment_at(0).state == "C"
    assert store.trace_at(0) == (c0,)
    assert keys(view.segments()) == keys(store.segments())

    p = store.retract("gw3", 99)  # winner withdrawn -> A reasserts
    view.apply(p)
    assert store.conflicts() == ()
    assert keys(store.segments()) == [
        ("A", 0, 5, (A0.ref, A2.ref)),
        ("B", 5, None, (B5.ref,)),
    ]

    # Retracting the shadowed loser changes no segments: empty patch.
    store.insert(c0)
    view.apply(store.retract("gw1", 1))
    assert store.segment_at(0).state == "C"


def test_single_record_then_empty_stream():
    store = HistoryStore()
    assert store.segments() == ()
    assert store.segment_at(0) is None

    p = store.insert(A0)
    assert p.removed == () and len(p.added) == 1
    single = store.segments()
    assert keys(single) == [("A", 0, None, (A0.ref,))]

    view = SegmentedView(())
    view.apply(p)
    assert keys(view.segments()) == keys(store.segments())

    p = store.retract("gw1", 1)
    assert p.added == ()
    view.apply(p)
    assert store.segments() == ()
    assert view.segments() == ()
    # Patch removed-id must be applicable against the replay view.
    assert p.removed[0].sid == single[0].sid


def test_merge_keeps_middle_duplicate_evidence():
    middle = Record(3, "A", "gw9", 7)
    store = HistoryStore([A0, middle, B5])
    # All three A records share one run even though B5 interrupts later.
    a_seg = store.segment_at(0)
    assert a_seg.start == 0 and a_seg.end == 5
    assert a_seg.sources == (A0, middle)

    store.insert(A2)
    seg = store.segment_at(2)
    refs = {r.ref for r in seg.sources}
    assert refs == {A0.ref, A2.ref, middle.ref}

    # Removing the middle record does not silently drop its evidence history.
    store.retract("gw9", 7)
    assert {r.ref for r in store.segment_at(2).sources} == {A0.ref, A2.ref}


def test_correction_is_atomic_single_patch():
    store = HistoryStore([A0, A2, B5])
    view = SegmentedView(store.segments())
    moved = Record(6, "B", "gw1", 3)
    p = store.correct(moved)
    assert p.record.as_ref() == ("gw1", 3)
    view.apply(p)
    assert keys(store.segments()) == [
        ("A", 0, 6, (A0.ref, A2.ref)),
        ("B", 6, None, (moved.ref,)),
    ]
    assert keys(view.segments()) == keys(store.segments())
    assert store.record("gw1", 3) == moved

    with pytest.raises(ValueError):
        store.correct(Record(9, "Z", "ghost", 1))
    with pytest.raises(ValueError):
        store.insert(A0)
    with pytest.raises(ValueError):
        store.retract("ghost", 1)


def _expected_sources(records):
    groups = {}
    for r in records:
        groups.setdefault(r.time, []).append(r)
    winners = {t: max(g, key=lambda r: (r.seq, r.source)) for t, g in groups.items()}
    return {r.ref for r in records if r.state == winners[r.time].state}


@pytest.mark.parametrize("seed", range(40))
def test_three_paths_agree_under_random_edits(seed):
    rng = random.Random(seed)
    records = [
        Record(rng.randrange(0, 8), rng.choice("AB"), f"gw{i % 3}", i)
        for i in range(rng.randrange(0, 6))
    ]
    store = HistoryStore(records)
    view = SegmentedView(store.segments())
    live: dict = {r.ref: r for r in records}
    next_seq = 1000 + seed

    for _ in range(60):
        action = rng.random()
        if action < 0.45 or not live:
            rec = Record(
                rng.randrange(0, 8),
                rng.choice("ABC"),
                f"gw{rng.randrange(0, 3)}",
                next_seq,
            )
            next_seq += 1
            if rec.ref in live:
                with pytest.raises(ValueError):
                    store.insert(rec)
                continue
            patch = store.insert(rec)
            live[rec.ref] = rec
        elif action < 0.75:
            ref = rng.choice(list(live))
            rec = live.pop(ref)
            patch = store.retract(*ref)
            assert patch.record.as_ref() == rec.ref
        else:
            ref = rng.choice(list(live))
            rec = Record(
                rng.randrange(0, 8),
                rng.choice("ABC"),
                ref[0],
                ref[1],
            )
            patch = store.correct(rec)
            live[ref] = rec

        # Every removed sid must exist in the replay view before applying.
        current_ids = {s.sid for s in view.segments()}
        assert {s.sid for s in patch.removed} <= current_ids
        view.apply(patch)

        direct = store.segments()
        replayed = view.segments()
        recomputed = build_runs(live.values())

        assert keys(direct) == keys(replayed)
        assert keys(direct) == [
            (r.state, r.start, r.end, tuple(x.ref for x in r.sources))
            for r in recomputed
        ]
        # Starts strictly increasing; end equals next start; last end None.
        starts = [s.start for s in direct]
        assert starts == sorted(starts) and len(starts) == len(set(starts))
        for i, s in enumerate(direct):
            assert s.end == (direct[i + 1].start if i + 1 < len(direct) else None)

        flattened = {ref for s in direct for ref in (r.ref for r in s.sources)}
        assert flattened == _expected_sources(list(live.values()))
