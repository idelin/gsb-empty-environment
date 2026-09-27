"""Exact alias table for constant-step sampling with replacement.

All table arithmetic uses ``fractions.Fraction``, so the table encodes the
input distribution *exactly*: no floating-point rounding and no
normalization drift.  Sampling needs exactly one column draw plus at most
one threshold draw from a caller-injected unbiased integer random source
(any object exposing ``randrange``, e.g. ``random.Random``).
"""

from __future__ import annotations

import json
import math
from fractions import Fraction


class WeightError(ValueError):
    """Raised for negative, non-finite, or otherwise invalid weights."""


def to_exact_weight(value) -> Fraction:
    """Convert a supported weight to an exact non-negative ``Fraction``.

    Accepts ``int`` and ``Fraction`` exactly, and finite ``float`` via its
    exact binary expansion.  Rejects booleans, negative values, and
    non-finite floats (inf / nan).
    """
    if isinstance(value, bool):
        raise WeightError(f"boolean is not a valid weight: {value!r}")
    if isinstance(value, Fraction):
        exact = value
    elif isinstance(value, int):
        exact = Fraction(value)
    elif isinstance(value, float):
        if not math.isfinite(value):
            raise WeightError(f"non-finite weight rejected: {value!r}")
        exact = Fraction(value)
    else:
        raise WeightError(f"unsupported weight type: {type(value).__name__}")
    if exact < 0:
        raise WeightError(f"negative weight rejected: {value!r}")
    return exact


class AliasTable:
    """Vose's alias method built with exact rational arithmetic.

    Column ``j`` keeps its own item with exact probability ``prob[j]``
    (a ``Fraction`` in [0, 1]) and otherwise falls back to ``alias[j]``.
    """

    FORMAT = "exact-alias-table/1"

    def __init__(self, weights):
        self.weights = [to_exact_weight(w) for w in weights]
        self.n = len(self.weights)
        if self.n == 0:
            raise WeightError("at least one weight is required")
        self.total = sum(self.weights, Fraction(0))
        if self.total <= 0:
            raise WeightError("at least one weight must be positive")
        self.prob, self.alias = self._build()

    def _build(self):
        """Vose's algorithm; every capacity transfer is exact rational math."""
        n = self.n
        scaled = [w * n / self.total for w in self.weights]  # exact, mean == 1
        prob = [Fraction(0)] * n
        alias = list(range(n))
        small = [i for i, s in enumerate(scaled) if s < 1]
        large = [i for i, s in enumerate(scaled) if s >= 1]
        while small and large:
            s = small.pop()
            l = large.pop()
            prob[s] = scaled[s]
            alias[s] = l
            scaled[l] += scaled[s] - 1
            (small if scaled[l] < 1 else large).append(l)
        # With exact arithmetic both lists drain together; the loop below is
        # a safety net that can only produce full (never-aliased) columns.
        for i in small + large:
            prob[i] = Fraction(1)
            alias[i] = i
        return prob, alias

    def sample(self, rng) -> int:
        """Draw one item index using the injected unbiased integer source."""
        i = rng.randrange(self.n)
        threshold = self.prob[i]
        if threshold >= 1:
            return i
        # Unbiased comparison: draw v uniformly from range(denominator) and
        # keep the column item iff v < numerator.  No floats involved.
        draw = rng.randrange(threshold.denominator)
        if draw < threshold.numerator:
            return i
        return self.alias[i]

    def probabilities(self):
        """Exact theoretical distribution encoded by this table."""
        n = self.n
        dist = [Fraction(0)] * n
        for j in range(n):
            dist[j] += self.prob[j] / n
            dist[self.alias[j]] += (1 - self.prob[j]) / n
        return dist

    def normalized_weights(self):
        """Exact normalized input weights (sums to exactly 1)."""
        return [w / self.total for w in self.weights]

    def to_dict(self) -> dict:
        return {
            "format": self.FORMAT,
            "weights": [f"{w.numerator}/{w.denominator}" for w in self.weights],
            "prob": [f"{p.numerator}/{p.denominator}" for p in self.prob],
            "alias": list(self.alias),
        }

    def dumps(self) -> str:
        return json.dumps(self.to_dict(), indent=2, sort_keys=True)

    @classmethod
    def from_dict(cls, data) -> "AliasTable":
        if data.get("format") != cls.FORMAT:
            raise ValueError(f"unsupported table format: {data.get('format')!r}")
        table = cls.__new__(cls)
        table.weights = [Fraction(w) for w in data["weights"]]
        table.n = len(table.weights)
        table.total = sum(table.weights, Fraction(0))
        table.prob = [Fraction(p) for p in data["prob"]]
        table.alias = [int(a) for a in data["alias"]]
        table._validate()
        return table

    @classmethod
    def loads(cls, text: str) -> "AliasTable":
        return cls.from_dict(json.loads(text))

    def _validate(self):
        if not (len(self.prob) == len(self.alias) == self.n):
            raise ValueError("corrupt table: length mismatch")
        if self.total <= 0:
            raise ValueError("corrupt table: no positive weight")
        for p, a in zip(self.prob, self.alias):
            if not 0 <= p <= 1:
                raise ValueError("corrupt table: threshold out of [0, 1]")
            if not 0 <= a < self.n:
                raise ValueError("corrupt table: alias index out of range")
