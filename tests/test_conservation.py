"""Conservation properties of determined totals under revision."""

import random

from counter_analysis import CounterStore, DiffStatus, Sample, SourceConfig


def test_splitting_determined_segment_conserves_total_property():
    """Inserting any in-interval middle sample keeps the determined total."""
    rng = random.Random(20260921)
    for _ in range(200):
        modulus = 1 << rng.choice([4, 8, 16])
        cfg = SourceConfig(bit_width=modulus.bit_length() - 1,
                           max_interval=100.0, config_version="v1")
        store = CounterStore()
        store.configure("s", cfg)
        # Physically consistent samples: real increments below one modulus
        # per decidable interval, so "at most one wrap" genuinely holds.
        c1 = rng.randrange(modulus)
        inc1 = rng.randrange(modulus)
        inc2 = rng.randrange(modulus - inc1)  # inc1 + inc2 < modulus
        t1, tm, t2 = 0.0, rng.uniform(1.0, 99.0), 0.0
        t2 = tm + rng.uniform(1.0, 100.0 - tm)
        cm = (c1 + inc1) % modulus
        c2 = (cm + inc2) % modulus
        store.insert(Sample("a", "s", t1, 1, c1))
        store.insert(Sample("c", "s", t2, 3, c2))
        before = store.totals("s").determined_total
        assert before == (inc1 + inc2) % modulus

        store.insert(Sample("b", "s", tm, 2, cm))
        after = store.totals("s").determined_total
        # At most one wrap over the whole interval and over each half forces
        # wrap counts to add up, so the determined total is invariant.
        assert after == before


def test_uncertain_segments_are_never_silently_dropped():
    """Every invalidated uncertain interval reappears as emitted intervals."""
    rng = random.Random(7)
    cfg = SourceConfig(bit_width=8, max_interval=10.0, config_version="v1")
    store = CounterStore()
    store.configure("s", cfg)
    store.insert(Sample("a", "s", 0.0, 1, 250))
    store.insert(Sample("z", "s", 1000.0, 2, 3))

    for i in range(50):
        sample = Sample(f"m{i}", "s", rng.uniform(0.0, 1000.0), 10 + i,
                        rng.randrange(256))
        rev = store.insert(sample)
        # Every invalidated interval is replaced by intervals covering the
        # exact same span: (l,r) -> (l,m),(m,r). Nothing vanishes.
        for old in rev.invalidated:
            covering = [d for d in rev.emitted
                        if d.left_id == old.left_id or d.right_id == old.right_id]
            assert covering, f"interval {old.key} dropped without replacement"

    totals = store.totals("s")
    live_uncertain = [d.key for d in store.diffs("s")
                      if d.status is DiffStatus.UNCERTAIN]
    assert list(totals.uncertain_intervals) == live_uncertain
    assert totals.complete == (not live_uncertain)
