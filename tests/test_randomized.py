"""Randomized differential test: incremental engine vs full-sort recompute.

After every random insert/retract we check, per source:
  * interval judgements equal the from-scratch reference;
  * totals equal the reference totals;
  * conservation: definite_sum moves by exactly revision.definite_total_delta;
  * invalidations are exact: `removed` intervals existed before and are gone
    after, `added` intervals exist after.
All randomness is generated locally from fixed seeds.
"""

import random

import pytest

from counter_analysis import (
    CounterConfig,
    IncrementalEngine,
    Sample,
    SourceConfig,
    recompute,
)

SOURCES = ["alpha", "beta", "gamma"]


def make_config(rng: random.Random) -> CounterConfig:
    return CounterConfig(
        sources={
            name: SourceConfig(
                bit_width=rng.choice([4, 8, 8, 16]),
                max_interval=rng.choice([5.0, 10.0, 60.0]),
            )
            for name in SOURCES
        },
        version=f"rand-{rng.randrange(1000)}",
    )


def random_sample(rng: random.Random, cfg: CounterConfig, n: int) -> Sample:
    source = rng.choice(SOURCES)
    modulus = cfg.source(source).modulus
    return Sample(
        sample_id=f"s{n}",
        source=source,
        seq=n,  # production order
        event_time=round(rng.uniform(0.0, 500.0), 3),
        count=rng.randrange(modulus),
        reset=rng.random() < 0.1,
    )


def run_one(seed: int, n_ops: int) -> None:
    rng = random.Random(seed)
    cfg = make_config(rng)
    eng = IncrementalEngine(cfg)
    live: dict[str, Sample] = {}
    prev_totals = {name: eng.total(name) for name in SOURCES}
    prev_intervals = {name: eng.intervals(name) for name in SOURCES}

    for op_idx in range(n_ops):
        if live and rng.random() < 0.3:
            victim = rng.choice(list(live))
            rev = eng.retract(victim)
            del live[victim]
        else:
            smp = random_sample(rng, cfg, op_idx)
            rev = eng.insert(smp)
            live[smp.sample_id] = smp

        src = rev.source
        before_iv, after_iv = prev_intervals[src], eng.intervals(src)

        # Invalidation exactness.
        for r in rev.removed:
            assert r.interval_id in before_iv
            assert r.interval_id not in after_iv
        for r in rev.added:
            assert r.interval_id in after_iv
            assert r.interval_id not in before_iv
        # Untouched intervals are byte-identical.
        changed = {r.interval_id for r in rev.removed} | {
            r.interval_id for r in rev.added
        }
        for iid in set(before_iv) & set(after_iv):
            assert iid not in changed
            assert before_iv[iid] == after_iv[iid]

        # Conservation of the definite total.
        new_total = eng.total(src)
        assert (
            new_total.definite_sum - prev_totals[src].definite_sum
            == rev.definite_total_delta
        )

        # Full cross-check against the reference recompute.
        ref = recompute(list(live.values()), cfg)
        for name in SOURCES:
            expected = {
                iid: res for iid, res in ref.intervals.items() if iid[0] == name
            }
            assert eng.intervals(name) == expected
            assert eng.total(name) == ref.totals.get(
                name, type(new_total)(0, 0, ())
            )
        # Every judgement carries the active config version.
        assert all(
            r.config_version == cfg.version for r in ref.intervals.values()
        )

        prev_totals = {name: eng.total(name) for name in SOURCES}
        prev_intervals = {name: eng.intervals(name) for name in SOURCES}


@pytest.mark.parametrize("seed", range(20))
def test_incremental_matches_reference(seed):
    run_one(seed, n_ops=120)
