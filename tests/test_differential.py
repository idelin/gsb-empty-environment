"""Differential tests: incremental engine vs. brute-force reference.

Random op sequences (inserts, duplicate redeliveries, retractions of live
and unknown ids, watermark advances) are applied to both implementations.
After every single op we require identical dispositions, identical
changelogs, and identical snapshots.
"""
import random

import pytest

from session_windows import (
    AdvanceWatermark,
    Event,
    ReferenceSessionWindows,
    Retract,
    SessionWindows,
)


def random_ops(rng, n_ops, sources=("a", "b"), t_max=200, wm_prob=0.15):
    live = {s: [] for s in sources}  # seqs currently believed live
    next_seq = {s: 0 for s in sources}
    watermark = {s: 0 for s in sources}
    ops = []
    for _ in range(n_ops):
        source = rng.choice(sources)
        roll = rng.random()
        if roll < wm_prob:
            watermark[source] += rng.randint(0, 30)
            ops.append(AdvanceWatermark(source, watermark[source]))
        elif roll < 0.30 and live[source] and rng.random() < 0.8:
            ops.append(Retract(source, rng.choice(live[source])))
            live[source].remove(ops[-1].seq)
        elif roll < 0.30:
            ops.append(Retract(source, rng.randint(0, next_seq[source] + 5)))
        else:
            seq = next_seq[source]
            next_seq[source] += 1
            t = rng.randint(0, t_max)
            ops.append(Event(source, seq, t, value=f"v{seq}"))
            live[source].append(seq)
            if rng.random() < 0.15:  # duplicate redelivery
                ops.append(Event(source, seq, t, value=f"v{seq}-dup"))
    return ops


@pytest.mark.parametrize("seed", range(30))
@pytest.mark.parametrize("gap,lateness", [(5, 10), (0, 0), (3, 25)])
def test_engine_matches_reference(seed, gap, lateness):
    rng = random.Random(seed)
    ops = random_ops(rng, 300)
    eng = SessionWindows(gap_ms=gap, allowed_lateness_ms=lateness)
    ref = ReferenceSessionWindows(gap_ms=gap, allowed_lateness_ms=lateness)
    for i, op in enumerate(ops):
        rev_e = eng.apply([op]).revisions[0]
        rev_r = ref.apply([op]).revisions[0]
        assert rev_e.disposition is rev_r.disposition, f"op {i}: {op}"
        assert sorted(map(repr, rev_e.changes)) == sorted(map(repr, rev_r.changes)), (
            f"op {i}: {op}\neng={rev_e.changes}\nref={rev_r.changes}"
        )
        assert eng.snapshot() == ref.snapshot(), f"op {i}: {op}"


def test_changelog_replay_matches_reference_snapshot():
    """A downstream store fed only the engine's changelog equals the
    reference implementation's ground truth at every step."""
    rng = random.Random(7)
    ops = random_ops(rng, 500)
    eng = SessionWindows(gap_ms=4, allowed_lateness_ms=8)
    ref = ReferenceSessionWindows(gap_ms=4, allowed_lateness_ms=8)
    downstream = {}
    for op in ops:
        for c in eng.apply([op]).revisions[0].changes:
            if c.kind.value == "retract":
                downstream.pop(c.window_id, None)
            else:
                downstream[c.window_id] = c.window
        ref.apply([op])
        truth = {w.id: w for w in ref.snapshot()}
        assert {k: v for k, v in downstream.items() if not v.closed} == truth
