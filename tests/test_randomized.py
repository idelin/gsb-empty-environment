"""Randomized step-by-step comparison: incremental store vs full recompute.

After every mutation the live diffs of the CounterStore must equal the
full-sort reference, and the emitted Revision must exactly describe the
difference between the reference before and after the mutation.
"""

import random
from collections import Counter

import pytest

from counter_analysis import CounterStore, Sample, SourceConfig, reference_diffs


def run_trace(seed: int, steps: int) -> None:
    rng = random.Random(seed)
    cfg = SourceConfig(bit_width=8, max_interval=30.0, config_version="v1")
    store = CounterStore()
    store.configure("dev", cfg)
    live: dict[str, Sample] = {}
    next_id = 0

    for _ in range(steps):
        before = Counter(reference_diffs(live.values(), cfg))

        if live and rng.random() < 0.35:
            victim = rng.choice(list(live))
            rev = store.retract("dev", victim)
            del live[victim]
        else:
            sample = Sample(
                sample_id=f"s{next_id}",
                source="dev",
                event_time=rng.uniform(0.0, 500.0),
                seq=rng.randrange(5),
                count=rng.randrange(256),
                reset=rng.random() < 0.1,
            )
            next_id += 1
            rev = store.insert(sample)
            live[sample.sample_id] = sample

        after = Counter(reference_diffs(live.values(), cfg))

        # 1. Incremental state equals the full-sort reference.
        assert Counter(store.diffs("dev")) == after

        # 2. The revision exactly explains before -> after.
        assert Counter(rev.invalidated) == before - after
        assert Counter(rev.emitted) == after - before

        # 3. Only intervals adjacent to the touched sample were revised.
        assert len(rev.invalidated) <= 2
        assert len(rev.emitted) <= 2


@pytest.mark.parametrize("seed", range(20))
def test_incremental_matches_full_recompute(seed: int):
    run_trace(seed, steps=200)
