"""Concrete contract tests + randomized index-vs-reference property test."""

from __future__ import annotations

import random
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from bitemporal import (  # noqa: E402
    BitemporalIndex,
    Kind,
    OPEN_ENDED,
    VersionRecord,
    reference_query,
)
from fixtures import EXPECTED_ALPHA_AT_10, OBSERVATIONS, build_index  # noqa: E402

INF = OPEN_ENDED


# --------------------------------------------------------------------- #
# concrete contract
# --------------------------------------------------------------------- #

@pytest.fixture()
def idx() -> BitemporalIndex:
    return build_index()


def test_correction_visibility(idx):
    """Recorded 20 as A, corrected 30 as B: as_of 25 -> A, as_of 35 -> B."""
    assert idx.query("alpha", 10, as_of=25).value == "A"
    assert idx.query("alpha", 10, as_of=35).value == "C"  # 2nd correction at 35
    assert idx.query("alpha", 10, as_of=32).value == "B"


def test_all_observations_match_contract(idx):
    for as_of in OBSERVATIONS:
        assert idx.query("alpha", 10, as_of=as_of).value == (
            EXPECTED_ALPHA_AT_10[as_of]
        ), f"as_of={as_of}"


def test_retroactive_retraction(idx):
    r = idx.query("alpha", 10, as_of=45)
    assert r.value is None and r.retracted
    # ...but the pre-retraction view of the world is untouched:
    assert idx.query("alpha", 10, as_of=37).value == "C"


def test_old_queries_not_polluted_by_latest_values(idx):
    """Re-running an old query after later writes must give the old answer."""
    before = idx.query("alpha", 10, as_of=25).value
    idx.add("alpha", 10, INF, recorded_at=70, kind=Kind.CORRECT, value="Z")
    idx.add("alpha", 10, INF, recorded_at=80, kind=Kind.RETRACT)
    assert idx.query("alpha", 10, as_of=25).value == before == "A"
    assert idx.query("alpha", 10, as_of=75).value == "Z"
    assert idx.query("alpha", 10, as_of=85).value is None


def test_adjacent_intervals_left_closed_right_open(idx):
    assert idx.query("beta", 9, as_of=10).value == "X"
    assert idx.query("beta", 10, as_of=10).value == "Y"  # boundary -> right
    assert idx.query("beta", 19, as_of=10).value == "Y"
    assert idx.query("beta", 20, as_of=10).value is None


def test_future_version_does_not_leak(idx):
    assert idx.query("gamma", 10, as_of=60).value is None
    assert idx.query("gamma", 1000, as_of=60).value == "FUTURE"
    # recorded in the "future" relative to the observation time:
    assert idx.query("gamma", 1000, as_of=24).value is None


def test_backfill_invisible_before_recording(idx):
    assert idx.query("delta", 10, as_of=44).value is None
    assert idx.query("delta", 10, as_of=45).value == "BACKFILL"


def test_multiple_corrections_of_same_fact(idx):
    """A@20, B@30, C@35: each observation window sees its own version."""
    assert idx.query("alpha", 10, as_of=29).value == "A"
    assert idx.query("alpha", 10, as_of=30).value == "B"
    assert idx.query("alpha", 10, as_of=34).value == "B"
    assert idx.query("alpha", 10, as_of=35).value == "C"


def test_priority_tiebreak_is_explicit_not_arrival_order():
    """Same recorded_at: explicit priority decides, insertion order must not."""
    def build(reverse: bool) -> BitemporalIndex:
        ix = BitemporalIndex()
        recs = [
            dict(value="low", priority=0),
            dict(value="high", priority=10),
        ]
        if reverse:
            recs.reverse()
        for r in recs:
            ix.add("obj", 0, 100, recorded_at=50, kind=Kind.ASSERT, **r)
        return ix

    assert build(False).query("obj", 5, as_of=60).value == "high"
    assert build(True).query("obj", 5, as_of=60).value == "high"


# --------------------------------------------------------------------- #
# randomized property test: index == reference, any insertion order
# --------------------------------------------------------------------- #

def _random_records(rng: random.Random, n: int) -> list[VersionRecord]:
    records = []
    for rid in range(n):
        obj = f"obj-{rng.randrange(4)}"
        start = rng.randrange(0, 40)
        length = rng.choice([1, 2, 5, 10, 1000])
        kind = rng.choice([Kind.ASSERT, Kind.CORRECT, Kind.CORRECT, Kind.RETRACT])
        records.append(
            VersionRecord(
                record_id=rid,
                object_id=obj,
                valid_from=start,
                valid_to=start + length,
                recorded_at=rng.randrange(0, 60),
                kind=kind,
                value=None if kind is Kind.RETRACT else f"v{rid}",
                priority=rng.randrange(0, 3),
            )
        )
    return records


@pytest.mark.parametrize("seed", range(40))
def test_index_matches_reference_random_sequences(seed: int):
    rng = random.Random(seed)
    records = _random_records(rng, n=60)

    shuffled = records[:]
    rng.shuffle(shuffled)  # prove insertion order does not matter
    idx = BitemporalIndex()
    for rec in shuffled:
        idx.add_record(rec)

    for _ in range(300):
        obj = f"obj-{rng.randrange(4)}"
        t = rng.randrange(0, 45)
        as_of = rng.randrange(0, 70)
        got = idx.query(obj, t, as_of).record
        want = reference_query(records, obj, t, as_of)
        assert got == want, f"obj={obj} t={t} as_of={as_of}: {got} != {want}"
