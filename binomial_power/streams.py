"""Reproducible, independent random streams per scenario.

The caller injects randomness: every simulator takes an explicit
`random.Random` instance. `scenario_streams` derives one child stream
per scenario from a single base seed, so scenarios are independent of
each other yet the whole batch is reproducible from (base_seed, names).
"""
from __future__ import annotations

import random


def scenario_streams(base_seed: int, names: list[str]) -> dict[str, tuple[int, random.Random]]:
    """Return {name: (child_seed, rng)} with one independent stream per name.

    Child seeds are drawn from a parent RNG seeded with base_seed, in the
    declared order of `names`; both the base seed and each child seed are
    recorded so any scenario can be replayed in isolation.
    """
    if len(set(names)) != len(names):
        raise ValueError("scenario names must be unique")
    parent = random.Random(base_seed)
    streams: dict[str, tuple[int, random.Random]] = {}
    for name in names:
        child_seed = parent.getrandbits(64)
        streams[name] = (child_seed, random.Random(child_seed))
    return streams
