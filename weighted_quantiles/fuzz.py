"""Random-transaction verification harness.

Drives the treap store and the naive reference through the same random
sequence of upserts / retractions / subject retractions / sliding-window
advances (including zero-weight and duplicate-ID transactions), then
compares totals, quantile intervals and ECDF values.

The random source is caller-injected; the report records the seed and the
sample size.  Conclusions drawn from the sampled transactions are labelled
``EMPIRICAL``; deterministic identities (e.g. equal-weight degeneracy
checks) are labelled ``EXACT``.
"""

from __future__ import annotations

import random
from fractions import Fraction
from typing import Any, Dict, List, Optional, Tuple

from .reference import NaiveWeightedReference
from .store import EMPIRICAL, WeightedStore

Q_GRID = [Fraction(0), Fraction(1, 4), Fraction(1, 2), Fraction(3, 4), Fraction(1)]


def run_random_transactions(seed: int,
                            n_ops: int = 2000,
                            check_every: int = 25,
                            window: int = 40,
                            rng: Optional[random.Random] = None) -> Dict[str, Any]:
    rng = rng if rng is not None else random.Random(seed)
    tree_seed = rng.getrandbits(64)
    store = WeightedStore(random.Random(tree_seed), window=window)
    ref = NaiveWeightedReference()

    payloads: Dict[Any, Tuple] = {}   # obs_id -> last applied payload
    live_ids: List[Any] = []
    subjects = ["alpha", "beta", "gamma"]
    clock = 0
    checks = 0
    mismatches: List[str] = []

    def apply_upsert(obs_id, value, weight, subject, timestamp):
        store.upsert(obs_id, value, weight, subject=subject, timestamp=timestamp)
        ref.upsert(obs_id, value, weight, subject=subject, timestamp=timestamp)
        if weight > 0:
            payloads[obs_id] = (value, weight, subject, timestamp)
        else:
            payloads.pop(obs_id, None)

    store.reset_stats()
    for step in range(n_ops):
        roll = rng.random()
        if roll < 0.55 or not live_ids:
            obs_id = f"obs-{step}"
            value = rng.randint(0, 20)  # heavy value duplication on purpose
            weight = Fraction(rng.randint(0, 5), rng.randint(1, 4))
            subject = rng.choice(subjects)
            clock += rng.randint(0, 3)
            apply_upsert(obs_id, value, weight, subject, clock)
            if weight > 0:
                live_ids.append(obs_id)
        elif roll < 0.70:
            obs_id = rng.choice(live_ids)
            if rng.random() < 0.5 and obs_id in payloads:
                # replay the identical update: must be an idempotent no-op
                before = (store.total_weight(), len(store))
                value, weight, subject, ts = payloads[obs_id]
                changed = store.upsert(obs_id, value, weight,
                                       subject=subject, timestamp=ts)
                after = (store.total_weight(), len(store))
                if changed or before != after:
                    mismatches.append(f"step {step}: idempotent replay mutated state")
            else:
                value = rng.randint(0, 20)
                weight = Fraction(rng.randint(1, 5), rng.randint(1, 3))
                subject = rng.choice(subjects)
                clock += rng.randint(0, 2)
                apply_upsert(obs_id, value, weight, subject, clock)
        elif roll < 0.85:
            obs_id = rng.choice(live_ids)
            live_ids.remove(obs_id)
            payloads.pop(obs_id, None)
            store.retract(obs_id)
            ref.retract(obs_id)
        elif roll < 0.93:
            subject = rng.choice(subjects)
            store.retract_subject(subject)
            ref.retract_subject(subject)
            live_ids = [i for i in live_ids if i in store._records]
        else:
            clock += rng.randint(1, 5)
            store.advance(clock)
            ref.advance(clock, window)
            live_ids = [i for i in live_ids if i in store._records]

        if step % check_every == 0 and store.total_weight() > 0:
            checks += 1
            if store.total_weight() != ref.total_weight():
                mismatches.append(f"step {step}: total weight "
                                  f"{store.total_weight()} != {ref.total_weight()}")
            for q in Q_GRID:
                got = store.quantile(q)
                want = ref.quantile_interval(q)
                if (got.low, got.high) != want:
                    mismatches.append(f"step {step}: q={q} got {got} want {want}")
            x = rng.randint(-1, 21)
            if store.cdf(x) != ref.cdf(x):
                mismatches.append(f"step {step}: cdf({x}) differs")

    return {
        "label": EMPIRICAL,
        "seed": seed,
        "samples": n_ops,
        "checks": checks,
        "mismatches": mismatches,
        "ok": not mismatches,
        "treap_node_visits": store.node_visits(),
        "reference_full_sorts": ref.sorts,
        "reference_rows_scanned": ref.scanned,
        "final_observations": len(store),
        "final_total_weight": str(store.total_weight()),
    }
