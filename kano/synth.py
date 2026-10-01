"""Synthetic data only.

The hierarchies below (age / city / sex) and the record generator exist
solely to produce reproducible synthetic benchmarks. No real personal data
is used anywhere in this repository, and no experiment here constitutes a
formal privacy proof.
"""

from __future__ import annotations

import random

from .hierarchy import GeneralizationHierarchy

AGE_SPEC = {
    "age:*": {
        "age:0-19": {"age:0-9": [f"age:{n}" for n in range(0, 10)],
                     "age:10-19": [f"age:{n}" for n in range(10, 20)]},
        "age:20-39": {"age:20-29": [f"age:{n}" for n in range(20, 30)],
                      "age:30-39": [f"age:{n}" for n in range(30, 40)]},
        "age:40-59": {"age:40-49": [f"age:{n}" for n in range(40, 50)],
                      "age:50-59": [f"age:{n}" for n in range(50, 60)]},
    }
}

CITY_SPEC = {
    "city:*": {
        "city:north": {"city:alpha": None, "city:beta": None},
        "city:south": {"city:gamma": None, "city:delta": None},
    }
}

SEX_SPEC = {"sex:*": {"sex:m": None, "sex:f": None}}


def _fix_none_leaves(spec: dict) -> dict:
    out = {}
    for key, sub in spec.items():
        if sub is None:
            out[key] = {}
        elif isinstance(sub, dict):
            out[key] = _fix_none_leaves(sub)
        else:
            out[key] = sub
    return out


def default_hierarchies() -> list[GeneralizationHierarchy]:
    return [
        GeneralizationHierarchy.from_nested("age", _fix_none_leaves(AGE_SPEC)),
        GeneralizationHierarchy.from_nested("city", _fix_none_leaves(CITY_SPEC)),
        GeneralizationHierarchy.from_nested("sex", _fix_none_leaves(SEX_SPEC)),
    ]


def generate_records(
    n: int,
    hierarchies: list[GeneralizationHierarchy],
    seed: int = 0,
) -> list[tuple[str, ...]]:
    """Uniform-random synthetic records over the hierarchy leaves."""
    rng = random.Random(seed)
    leaves = [sorted(h.leaves) for h in hierarchies]
    return [tuple(rng.choice(pool) for pool in leaves) for _ in range(n)]
