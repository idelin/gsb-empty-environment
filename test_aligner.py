import random

import pytest

from aligner import (
    MAIN,
    Aligner,
    AlignmentTable,
    AlignedResult,
    Anchor,
    FinalizeResult,
    RetractResult,
    Sample,
    UpsertAux,
    UpsertMain,
    RetractAux,
    RetractMain,
    Watermark,
)
from reference import reference_table


def only_match(row, source="aux"):
    return next(m for m in row.matches if m.source == source)


# ---------------------------------------------------------------------------
# 1. The example from the requirements
# ---------------------------------------------------------------------------
def test_prompt_example_tie_then_late_better_replaces():
    al = Aligner(["aux"], tolerance=2)
    out = al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),
        UpsertAux(Sample("aux", "x8", 8, 1)),
        UpsertAux(Sample("aux", "x12", 12, 2)),
    ])
    # distance 2 on both sides -> earlier time wins
    assert only_match(out[-1]).sample_id == "x8"
    assert out[-1].version == 2  # v1 was the initial all-missing row

    out = al.apply(UpsertAux(Sample("aux", "x9", 9, 3)))
    # late, strictly better sample -> replacement event, version bumped
    assert len(out) == 1
    assert isinstance(out[0], AlignedResult)
    assert only_match(out[0]).sample_id == "x9"
    assert out[0].version == 3


# ---------------------------------------------------------------------------
# 2. Tie-breaking: equal distance -> earlier time; same time -> smaller seq
# ---------------------------------------------------------------------------
def test_tie_prefers_earlier_time():
    al = Aligner(["aux"], tolerance=5)
    al.apply_all([
        UpsertAux(Sample("aux", "late", 14, 1)),
        UpsertAux(Sample("aux", "early", 6, 2)),
    ])
    out = al.apply(UpsertMain(Anchor("A", 10, 1)))
    assert only_match(out[0]).sample_id == "early"


def test_tie_same_time_prefers_smaller_seq():
    al = Aligner(["aux"], tolerance=5)
    al.apply_all([
        UpsertAux(Sample("aux", "hi", 9, 7)),
        UpsertAux(Sample("aux", "lo", 9, 3)),
    ])
    out = al.apply(UpsertMain(Anchor("A", 10, 1)))
    assert only_match(out[0]).sample_id == "lo"


# ---------------------------------------------------------------------------
# 3. One aux sample may serve many anchors
# ---------------------------------------------------------------------------
def test_shared_sample_serves_multiple_anchors():
    al = Aligner(["aux"], tolerance=2)
    out = al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),
        UpsertMain(Anchor("B", 11, 2)),
        UpsertAux(Sample("aux", "shared", 10, 1)),
    ])
    table = AlignmentTable()
    table.apply_all(out)
    assert table.as_dict() == {
        "A": {"time": 10, "version": 2, "aux": "shared"},
        "B": {"time": 11, "version": 2, "aux": "shared"},
    }


# ---------------------------------------------------------------------------
# 4. Aux retraction falls back to the next-best candidate
# ---------------------------------------------------------------------------
def test_retract_falls_back_to_next_best():
    al = Aligner(["aux"], tolerance=3)
    al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),
        UpsertAux(Sample("aux", "best", 9, 1)),
        UpsertAux(Sample("aux", "next", 13, 2)),
    ])
    out = al.apply(RetractAux("aux", "best"))
    assert len(out) == 1 and only_match(out[0]).sample_id == "next"
    assert out[0].version == 3

    out = al.apply(RetractAux("aux", "next"))
    assert only_match(out[0]).missing is True
    assert out[0].version == 4


# ---------------------------------------------------------------------------
# 5. A single sample outside tolerance -> missing
# ---------------------------------------------------------------------------
def test_single_sample_out_of_tolerance_is_missing():
    al = Aligner(["aux"], tolerance=2)
    out = al.apply_all([
        UpsertAux(Sample("aux", "far", 20, 1)),
        UpsertMain(Anchor("A", 10, 1)),
    ])
    m = only_match(out[-1])
    assert m.missing is True and m.sample_id is None


# ---------------------------------------------------------------------------
# 6. Main anchor retraction removes the row
# ---------------------------------------------------------------------------
def test_main_anchor_retraction():
    al = Aligner(["aux"], tolerance=2)
    table = AlignmentTable()
    table.apply_all(al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),
        UpsertAux(Sample("aux", "x", 10, 1)),
    ]))
    assert "A" in table.rows

    out = al.apply(RetractMain("A"))
    assert out == [RetractResult("A", version=2)]
    table.apply_all(out)
    assert table.rows == {}

    # retracting again is a no-op
    assert al.apply(RetractMain("A")) == []


# ---------------------------------------------------------------------------
# 7. Watermark finalization locks the result
# ---------------------------------------------------------------------------
def test_watermark_finalizes_and_blocks_rewrites():
    al = Aligner(["aux"], tolerance=2)
    table = AlignmentTable()
    table.apply_all(al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),
        UpsertAux(Sample("aux", "x9", 9, 1)),
    ]))
    out = al.apply_all([Watermark("aux", 12), Watermark(MAIN, 10)])
    assert out == [FinalizeResult("A", version=2)]
    table.apply_all(out)

    # a late, perfect sample must not rewrite the finalized row
    assert al.apply(UpsertAux(Sample("aux", "perfect", 10, 2))) == []
    # nor may a retraction or anchor withdrawal touch it
    assert al.apply(RetractAux("aux", "x9")) == []
    assert al.apply(RetractMain("A")) == []
    assert only_match(table.rows["A"]).sample_id == "x9"


def test_late_correction_before_watermark_then_lock():
    al = Aligner(["aux"], tolerance=2)
    al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),
        UpsertAux(Sample("aux", "x8", 8, 1)),
        Watermark("aux", 11),  # not yet final: 10+2=12 > 11
    ])
    out = al.apply(UpsertAux(Sample("aux", "x9", 9, 2)))
    assert only_match(out[0]).sample_id == "x9"  # still open -> correction
    out = al.apply_all([Watermark("aux", 12), Watermark(MAIN, 10)])
    assert out == [FinalizeResult("A", version=3)]
    assert al.apply(UpsertAux(Sample("aux", "x10", 10, 3))) == []  # locked


# ---------------------------------------------------------------------------
# 8. GC must keep candidates usable by unclosed anchors
# ---------------------------------------------------------------------------
def test_gc_keeps_candidates_for_open_anchors():
    al = Aligner(["aux"], tolerance=2)
    al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),       # stays open (no aux watermark)
        UpsertAux(Sample("aux", "keep", 9, 1)),
        UpsertAux(Sample("aux", "drop", 50, 2)),
        Watermark(MAIN, 100),                 # triggers gc
    ])
    st = al.stats()
    # "keep" is within tolerance of open anchor A -> retained;
    # "drop" is beyond every live anchor and beyond main_wm - tol -> collected
    assert st["samples"]["aux"] == 1

    # proof "keep" survived: retracting it still produces a correction
    out = al.apply(RetractAux("aux", "keep"))
    assert len(out) == 1 and only_match(out[0]).missing is True


def test_gc_collects_only_after_anchor_closes():
    al = Aligner(["aux"], tolerance=2)
    al.apply_all([
        UpsertMain(Anchor("A", 10, 1)),
        UpsertAux(Sample("aux", "x9", 9, 1)),
        UpsertAux(Sample("aux", "x8", 8, 2)),  # fallback candidate
        Watermark(MAIN, 100),
    ])
    assert al.stats()["samples"]["aux"] == 2  # both near open anchor
    al.apply_all([Watermark("aux", 12)])      # finalizes A
    assert al.stats()["samples"]["aux"] == 0  # now safe to collect


# ---------------------------------------------------------------------------
# 9. Randomized cross-check against the independent scan reference
# ---------------------------------------------------------------------------
def gen_events(seed):
    rng = random.Random(seed)
    sources = ["s1", "s2"]
    tol = rng.choice([1, 2, 3, 5])
    anchors = [Anchor(f"a{i}", rng.randint(0, 50), i)
               for i in range(rng.randint(1, 12))]
    samples = {s: [Sample(s, f"{s}-{i}", rng.randint(0, 50), i)
                   for i in range(rng.randint(0, 20))]
               for s in sources}

    # 1) random data sequence: upserts, with retractions of live items mixed in
    pending = ([UpsertMain(a) for a in anchors]
               + [UpsertAux(x) for xs in samples.values() for x in xs])
    rng.shuffle(pending)
    live_main, live_aux, data = [], [], []
    while pending:
        roll = rng.random()
        if roll < 0.15 and live_main:
            data.append(RetractMain(live_main.pop(rng.randrange(len(live_main)))))
        elif roll < 0.30 and live_aux:
            data.append(RetractAux(*live_aux.pop(rng.randrange(len(live_aux)))))
        else:
            ev = pending.pop()
            data.append(ev)
            if isinstance(ev, UpsertMain):
                live_main.append(ev.anchor.anchor_id)
            else:
                live_aux.append((ev.sample.source, ev.sample.sample_id))

    # 2) interleave watermarks that respect the contract: Watermark(src, t)
    #    only after every still-pending event of src has time > t.
    def src_of(ev):
        if isinstance(ev, (UpsertMain, RetractMain)):
            return MAIN
        return ev.source if isinstance(ev, RetractAux) else ev.sample.source

    def time_of(ev):
        if isinstance(ev, UpsertMain):
            return ev.anchor.time
        if isinstance(ev, UpsertAux):
            return ev.sample.time
        return None  # retractions carry no event time

    wm_todo = {}
    for s in [MAIN] + sources:
        marks = sorted(rng.randint(0, 60) for _ in range(rng.randint(0, 3)))
        if marks:
            wm_todo[s] = marks

    remaining_times = {s: sorted(t for t in (time_of(e) for e in data
                                             if src_of(e) == s)
                                 if t is not None)
                       for s in [MAIN] + sources}
    events = []
    for ev in data:
        events.append(ev)
        t = time_of(ev)
        if t is not None:
            remaining_times[src_of(ev)].remove(t)
        # emit any watermark checkpoints that are now legal, in random order
        due = [s for s, marks in wm_todo.items()
               if marks and (not remaining_times[s]
                             or marks[0] < remaining_times[s][0])]
        rng.shuffle(due)
        for s in due:
            while (wm_todo[s] and (not remaining_times[s]
                                   or wm_todo[s][0] < remaining_times[s][0])):
                events.append(Watermark(s, wm_todo[s].pop(0)))
            if not wm_todo[s]:
                del wm_todo[s]
    for s, marks in wm_todo.items():  # flush the rest (all remaining times > t)
        events.extend(Watermark(s, m) for m in marks)
    return events, sources, tol


@pytest.mark.parametrize("seed", range(300))
def test_random_arrival_order_matches_scan_reference(seed):
    events, sources, tol = gen_events(seed)
    al = Aligner(sources, tolerance=tol)
    out = al.apply_all(events)

    # per-anchor output invariants: versions 1,2,3..., finalize/retract once
    version, present = {}, {}
    for ev in out:
        if isinstance(ev, AlignedResult):
            expected = version.get(ev.anchor_id, 0) + 1
            assert ev.version == expected
            version[ev.anchor_id] = ev.version
            present[ev.anchor_id] = True
        elif isinstance(ev, (RetractResult, FinalizeResult)):
            assert present.pop(ev.anchor_id, False)

    table = AlignmentTable()
    table.apply_all(out)
    got = {aid: (row.anchor_time,
                 {m.source: (None if m.missing else m.sample_id)
                  for m in row.matches})
           for aid, row in table.rows.items()}
    assert got == reference_table(events, sources, tol)
