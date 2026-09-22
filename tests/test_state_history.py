"""Tests: every scenario cross-checks three paths.

1. direct query on StateHistory
2. initial snapshot + replay of every emitted patch (SegmentView)
3. independent naive recomputation from the raw events (oracle)
"""

import pytest

from rle_history import EventRef, StateHistory, apply_patch, build_segments


def oracle_segments(events):
    """Independent naive RLE recomputation used as the test oracle."""
    ordered = sorted(events, key=lambda e: (e.time, e.source, e.seq))
    runs = []
    for e in ordered:
        if runs and runs[-1][0] == e.state:
            runs[-1][2].append(e)
        else:
            runs.append([e.state, e.time, [e]])
    out = []
    for i, (state, start, members) in enumerate(runs):
        end = runs[i + 1][1] if i + 1 < len(runs) else None
        out.append((state, start, end, tuple((m.source, m.seq) for m in members)))
    return out


def as_tuples(segments):
    return [
        (s.state, s.start, s.end, tuple((m.source, m.seq) for m in s.members))
        for s in segments
    ]


class PatchReplayer:
    """Path 2: starts from the initial snapshot, consumes every patch."""

    def __init__(self, history):
        self.view = history.segments()

    def feed(self, patch):
        self.view = apply_patch(self.view, patch)


def run_script(ops):
    """Execute ops, returning (history, replayer, patch_log)."""
    history = StateHistory()
    replayer = PatchReplayer(history)
    patches = []
    for op, args in ops:
        patch = getattr(history, op)(*args)
        assert isinstance(patch.removed + patch.added, tuple)
        replayer.feed(patch)
        patches.append(patch)
    return history, replayer, patches


def check_three_paths(history, replayer):
    direct = history.segments()
    assert replayer.view == direct, "patch replay diverged from direct query"
    assert as_tuples(direct) == oracle_segments(history.events()), "oracle mismatch"
    # segment view is sorted, non-overlapping, half-open
    for prev, nxt in zip(direct, direct[1:]):
        assert prev.end == nxt.start
        assert prev.state != nxt.state
    return direct


# ---- the headline scenario -------------------------------------------------

def test_split_then_retract_restores_original_structure():
    ops = [
        ("insert", ("dev", 1, 0, "A")),
        ("insert", ("dev", 2, 2, "A")),
        ("insert", ("dev", 3, 5, "B")),
    ]
    history, replayer, _ = run_script(ops)
    baseline = check_three_paths(history, replayer)
    assert as_tuples(baseline) == [
        ("A", 0, 5, (("dev", 1), ("dev", 2))),
        ("B", 5, None, (("dev", 3),)),
    ]

    # insert B@1: the A segment splits, A@2 moves to the second A run
    split_patch = history.insert("dev", 4, 1, "B")
    replayer.feed(split_patch)
    split = check_three_paths(history, replayer)
    assert as_tuples(split) == [
        ("A", 0, 1, (("dev", 1),)),
        ("B", 1, 2, (("dev", 4),)),
        ("A", 2, 5, (("dev", 2),)),
        ("B", 5, None, (("dev", 3),)),
    ]
    assert {s.state for s in split_patch.added} == {"A", "B"}
    assert [s.state for s in split_patch.removed] == ["A"]  # old [0,5) A run gone

    # retract B@1: original two-segment structure restored, A@2 evidence kept
    heal_patch = history.retract("dev", 4)
    replayer.feed(heal_patch)
    healed = check_three_paths(history, replayer)
    assert healed == baseline
    assert history.provenance(3) == (
        EventRef("dev", 1, 0, "A"),
        EventRef("dev", 2, 2, "A"),
    )


def test_merge_does_not_drop_middle_duplicate_evidence():
    history, replayer, _ = run_script([
        ("insert", ("dev", 1, 0, "A")),
        ("insert", ("dev", 2, 1, "B")),
        ("insert", ("dev", 3, 2, "A")),
    ])
    patch = history.retract("dev", 2)  # remove the middle B -> two A runs merge
    replayer.feed(patch)
    segs = check_three_paths(history, replayer)
    assert as_tuples(segs) == [("A", 0, None, (("dev", 1), ("dev", 3)))]
    assert len(patch.removed) == 3 and len(patch.added) == 1


# ---- edge cases ------------------------------------------------------------

def test_empty_stream():
    history, replayer, _ = run_script([])
    assert check_three_paths(history, replayer) == []
    assert history.state_at(0) is None
    assert history.provenance(0) == ()
    with pytest.raises(KeyError):
        history.retract("dev", 99)


def test_single_record_then_retract_to_empty():
    history, replayer, _ = run_script([("insert", ("dev", 1, 7, "A"))])
    segs = check_three_paths(history, replayer)
    assert as_tuples(segs) == [("A", 7, None, (("dev", 1),))]
    assert history.state_at(7) == "A"
    assert history.state_at(6) is None

    patch = history.retract("dev", 1)
    replayer.feed(patch)
    assert check_three_paths(history, replayer) == []
    assert len(patch.removed) == 1 and patch.added == ()


def test_retract_first_and_last_events():
    history, replayer, _ = run_script([
        ("insert", ("dev", 1, 0, "A")),
        ("insert", ("dev", 2, 1, "B")),
        ("insert", ("dev", 3, 2, "C")),
    ])
    p1 = history.retract("dev", 1)  # first: B segment extends back to t=1
    replayer.feed(p1)
    assert as_tuples(check_three_paths(history, replayer)) == [
        ("B", 1, 2, (("dev", 2),)),
        ("C", 2, None, (("dev", 3),)),
    ]
    p2 = history.retract("dev", 3)  # last: B becomes open-ended
    replayer.feed(p2)
    assert as_tuples(check_three_paths(history, replayer)) == [
        ("B", 1, None, (("dev", 2),)),
    ]


def test_same_time_conflict_deterministic_and_no_evidence_lost():
    # canonical order at equal times: (source, seq); last one wins the instant
    history, replayer, _ = run_script([
        ("insert", ("dev", 1, 0, "A")),
        ("insert", ("dev", 2, 5, "B")),
        ("insert", ("dev", 3, 5, "C")),  # same instant as B@5
    ])
    segs = check_three_paths(history, replayer)
    assert as_tuples(segs) == [
        ("A", 0, 5, (("dev", 1),)),
        ("B", 5, 5, (("dev", 2),)),  # zero-length: superseded at t=5, kept as evidence
        ("C", 5, None, (("dev", 3),)),
    ]
    assert history.state_at(5) == "C"
    assert history.provenance(5) == (EventRef("dev", 3, 5, "C"),)
    # all three raw records still retrievable
    assert {e.key for e in history.events()} == {("dev", 1), ("dev", 2), ("dev", 3)}


def test_correction_moves_event_and_emits_single_patch():
    history, replayer, _ = run_script([
        ("insert", ("dev", 1, 0, "A")),
        ("insert", ("dev", 2, 4, "B")),
    ])
    patch = history.correct("dev", 2, time=1, state="A")  # B@4 -> A@1, merges
    replayer.feed(patch)
    assert as_tuples(check_three_paths(history, replayer)) == [
        ("A", 0, None, (("dev", 1), ("dev", 2))),
    ]
    noop = history.correct("dev", 2, time=1, state="A")
    assert noop.is_empty()
    replayer.feed(noop)
    check_three_paths(history, replayer)
    with pytest.raises(KeyError):
        history.correct("dev", 99, state="X")


def test_duplicate_insert_rejected():
    history = StateHistory()
    history.insert("dev", 1, 0, "A")
    with pytest.raises(ValueError):
        history.insert("dev", 1, 3, "B")


def test_full_replay_from_empty_matches_direct_query():
    ops = [
        ("insert", ("dev", 1, 0, "A")),
        ("insert", ("dev", 2, 2, "A")),
        ("insert", ("dev", 3, 5, "B")),
        ("insert", ("dev", 4, 1, "B")),
        ("retract", ("dev", 4)),
        ("correct", ("dev", 3,)),
        ("insert", ("dev", 5, 9, "C")),
        ("retract", ("dev", 1)),
        ("retract", ("dev", 5)),
    ]
    # correct() needs kwargs; handle separately
    history = StateHistory()
    replayer = PatchReplayer(history)
    for op, args in ops:
        if op == "correct":
            patch = history.correct("dev", 3, state="D")
        else:
            patch = getattr(history, op)(*args)
        replayer.feed(patch)
    check_three_paths(history, replayer)


def test_build_segments_directly():
    events = [
        EventRef("s", 1, 0, "A"),
        EventRef("s", 2, 1, "A"),
        EventRef("s", 3, 3, "B"),
    ]
    segs = build_segments(events)
    assert as_tuples(segs) == [
        ("A", 0, 3, (("s", 1), ("s", 2))),
        ("B", 3, None, (("s", 3),)),
    ]
