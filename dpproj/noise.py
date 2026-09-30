"""Seeded Laplace noise ledger.

Design rules enforced here:

* Noise for a given key is drawn exactly once and then stored. Releasing
  the same quantity again reuses the stored draw -- a repeated release can
  never re-draw noise, and post-processing can never resample until a
  "nice" answer appears.
* The ledger is deterministic: the same seed and the same sequence of
  (key, scale) requests reproduces identical draws, so the noise step is
  fully recomputable from (seed, request log).
* The ledger serializes to / from JSON together with the exact RNG state,
  so an archived release can be audited and continued.

Only the Python standard library is used. The mechanism is the classic
Laplace mechanism: for L1 sensitivity `sensitivity` and privacy parameter
`epsilon`, the scale is sensitivity / epsilon. This module only implements
sampling and bookkeeping; whether a given parameter choice constitutes a
formal privacy guarantee is out of scope (see README).
"""
from __future__ import annotations

import json
import math
import random
from fractions import Fraction
from typing import Union


class NoiseLedger:
    """Draw-once, reuse-forever Laplace noise ledger keyed by string."""

    def __init__(self, seed: int):
        self.seed = seed
        self._rng = random.Random(seed)
        self.draws: dict[str, dict] = {}

    def laplace(self, key: str, scale: Union[Fraction, int]) -> float:
        """Return Laplace(0, scale) noise for `key`, drawing once."""
        scale = Fraction(scale)
        if scale <= 0:
            raise ValueError("scale must be positive")
        if key in self.draws:
            entry = self.draws[key]
            if Fraction(entry["scale_num"], entry["scale_den"]) != scale:
                raise ValueError(
                    f"key {key!r} already used with a different scale")
            return entry["value"]
        # Inverse-CDF sampling, guarded against log(0).
        u = self._rng.random() - 0.5
        while u == -0.5:
            u = self._rng.random() - 0.5
        value = -float(scale) * math.copysign(1.0, u) * \
            math.log1p(-2.0 * abs(u))
        self.draws[key] = {
            "value": value,
            "scale_num": scale.numerator,
            "scale_den": scale.denominator,
        }
        return value

    def to_json(self) -> str:
        state = self._rng.getstate()
        return json.dumps({
            "seed": self.seed,
            "draws": self.draws,
            "rng_state": [state[0], list(state[1]), state[2]],
        }, indent=2, sort_keys=True)

    @classmethod
    def from_json(cls, text: str) -> "NoiseLedger":
        payload = json.loads(text)
        ledger = cls(seed=payload["seed"])
        ledger.draws = {k: dict(v) for k, v in payload["draws"].items()}
        state = payload["rng_state"]
        ledger._rng.setstate((state[0], tuple(state[1]), state[2]))
        return ledger
