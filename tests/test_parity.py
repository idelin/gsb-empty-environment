"""Property tests: the incremental tracker must agree, op by op, with
the brute-force reference implementation that re-sorts and recomputes
all sessions from scratch after every op."""

import random

import pytest

from session_windows import (
    AdvanceWatermark,
    Event,
    ReferenceSessionTracker,
    Retract,
    SessionTracker,
    Upsert,
)

SOURCES = ("a", "b")


def change_key(change):
    return (change.kind.value, change.window.window_id)


def random_ops(rng, count, watermarks):
    ops = []
    for _ in range(count):
        source = rng.choice(SOURCES)
        roll = rng.random()
        if roll < 0.55:
            ops.append(
                Upsert(
                    Event(
                        source,
                        rng.randrange(30),
                        rng.randrange(0, 200),
                        rng.randrange(5),
                    )
                )
            )
        elif roll < 0.75:
            ops.append(Retract(source, rng.randrange(30)))
        else:
            watermarks[source] += rng.randrange(0, 25)
            ops.append(AdvanceWatermark(source, watermarks[source]))
    return ops


@pytest.mark.parametrize("gap,lateness", [(10, 5), (5, 0), (0, 3)])
@pytest.mark.parametrize("seed", range(25))
def test_incremental_matches_reference(gap, lateness, seed):
    rng = random.Random(f"{gap}:{lateness}:{seed}")
    tracker = SessionTracker(gap_ms=gap, allowed_lateness_ms=lateness)
    reference = ReferenceSessionTracker(gap_ms=gap, allowed_lateness_ms=lateness)
    watermarks = {s: 0 for s in SOURCES}

    for step, op in enumerate(random_ops(rng, 400, watermarks)):
        got = tracker.apply(op)
        want = reference.apply(op)
        assert got.disposition == want.disposition, (step, op, got, want)
        assert sorted(got.changes, key=change_key) == sorted(
            want.changes, key=change_key
        ), (step, op, got.changes, want.changes)
        assert tracker.active_windows() == reference.active_windows(), (step, op)

    # Structural invariant: windows are disjoint and > gap apart.
    for source in SOURCES:
        windows = tracker.active_windows(source)
        for left, right in zip(windows, windows[1:]):
            assert right.start_ms - left.end_ms > gap


def test_mixed_batch_parity():
    rng = random.Random(7)
    tracker = SessionTracker(gap_ms=8, allowed_lateness_ms=4)
    reference = ReferenceSessionTracker(gap_ms=8, allowed_lateness_ms=4)
    watermarks = {s: 0 for s in SOURCES}
    ops = random_ops(rng, 300, watermarks)
    got = tracker.apply_all(ops)
    want = reference.apply_all(ops)
    assert [r.disposition for r in got.results] == [
        r.disposition for r in want.results
    ]
    for got_r, want_r in zip(got.results, want.results):
        assert sorted(got_r.changes, key=change_key) == sorted(
            want_r.changes, key=change_key
        )
    assert tracker.active_windows() == reference.active_windows()
