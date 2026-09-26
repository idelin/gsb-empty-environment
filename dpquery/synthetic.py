"""Deterministic synthetic data generators. Synthetic data only."""

from __future__ import annotations

import random
from typing import Sequence

from .models import SyntheticDataset

_CITIES = ("Beijing", "Shanghai", "Shenzhen", "Hangzhou")
_PLAN = ("A", "B", "C")


def make_synthetic_dataset(
    n: int = 400,
    seed: int = 20260927,
    *,
    name: str = "synthetic_users",
) -> SyntheticDataset:
    """Generate a synthetic user table.

    Columns: age (18..79), income (0..100_000 unclipped raw), city, plan,
    active (bool). The dataset version embeds seed and n, so regenerating with
    different parameters yields a different data version and forces fresh
    accounting.
    """
    rng = random.Random(seed)
    records = tuple(
        {
            "age": rng.randint(18, 79),
            "income": round(rng.uniform(0.0, 100_000.0), 2),
            "city": rng.choice(_CITIES),
            "plan": rng.choice(_PLAN),
            "active": rng.choice((True, False)),
        }
        for _ in range(n)
    )
    schema = {
        "age": "int",
        "income": "float",
        "city": "str",
        "plan": "str",
        "active": "bool",
    }
    version = f"synthetic-seed{seed}-n{n}-v1"
    return SyntheticDataset(
        name=name,
        version=version,
        seed=seed,
        records=records,
        schema=schema,
    )


def make_version_variant(
    n: int = 401,
    seed: int = 20260928,
    *,
    name: str = "synthetic_users",
) -> SyntheticDataset:
    """A different data version (more records, different seed)."""
    return make_synthetic_dataset(n=n, seed=seed, name=name)
