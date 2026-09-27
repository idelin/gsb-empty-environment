"""Exact alias-table sampling with rational weights (Python 3.11, stdlib only).

Design goals
------------
* Constant-step sampling with replacement via the alias method (Vose's
  algorithm), carried out entirely with ``fractions.Fraction`` so tiny
  weights and normalization introduce zero rounding error.
* The caller injects an unbiased integer random source (``randbelow(n)``).
  Sampling draws exactly two integers per pick: one to choose the column,
  one to compare against the column threshold.
* Tables serialize to JSON and restore bit-exactly, so a seeded run is
  replayable after a round trip.
* A publisher supports batch weight updates with dual-version release:
  the new table is built off to the side and swapped in atomically, while
  sessions opened earlier keep sampling the old distribution.
"""

from __future__ import annotations

import json
import math
import random
from decimal import Decimal
from fractions import Fraction
from typing import Protocol, Sequence, runtime_checkable

__all__ = [
    "UnbiasedIntSource",
    "SeededRandomSource",
    "AliasTable",
    "AliasTablePublisher",
    "SamplingSession",
]

_FORMAT_TAG = "exact-alias-table/1"


@runtime_checkable
class UnbiasedIntSource(Protocol):
    """Caller-injected source of unbiased uniform integers."""

    def randbelow(self, n: int) -> int:
        """Return a uniform integer in [0, n). Must be unbiased."""
        ...


class SeededRandomSource:
    """Unbiased integer source backed by ``random.Random``.

    Records the seed and the number of integers drawn so a run can be
    replayed exactly. ``random.Random.randrange`` uses rejection sampling
    internally, so every value in [0, n) is equiprobable.
    """

    def __init__(self, seed: int | str | bytes):
        self.seed = seed
        self.draws = 0
        self._rng = random.Random(seed)

    def randbelow(self, n: int) -> int:
        if n <= 0:
            raise ValueError(f"randbelow requires n > 0, got {n}")
        self.draws += 1
        return self._rng.randrange(n)

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return f"SeededRandomSource(seed={self.seed!r}, draws={self.draws})"


def _as_fraction(value, index: int) -> Fraction:
    """Convert one weight to an exact Fraction, rejecting invalid input."""
    if isinstance(value, bool):
        raise TypeError(f"weight[{index}]: bool is not a valid weight")
    if isinstance(value, Fraction):
        frac = value
    elif isinstance(value, int):
        frac = Fraction(value)
    elif isinstance(value, float):
        if not math.isfinite(value):
            raise ValueError(f"weight[{index}]: non-finite weight {value!r}")
        frac = Fraction(value)  # exact binary expansion, no rounding
    elif isinstance(value, Decimal):
        if not value.is_finite():
            raise ValueError(f"weight[{index}]: non-finite weight {value!r}")
        frac = Fraction(value)
    elif isinstance(value, str):
        try:
            frac = Fraction(value)
        except ValueError as exc:
            raise ValueError(
                f"weight[{index}]: unparseable weight {value!r}"
            ) from exc
    else:
        raise TypeError(
            f"weight[{index}]: unsupported weight type {type(value).__name__}"
        )
    if frac < 0:
        raise ValueError(f"weight[{index}]: negative weight {frac}")
    return frac


def _lcm(a: int, b: int) -> int:
    return a // math.gcd(a, b) * b


class AliasTable:
    """Exact alias table over rational weights.

    Internal representation: ``n`` columns; column ``i`` selects item ``i``
    when a uniform integer ``r`` in ``[0, lcm)`` satisfies ``r < q[i]``,
    and selects ``alias[i]`` otherwise. ``q[i] / lcm`` is exactly the
    per-column probability produced by Vose's algorithm in rational
    arithmetic, so the induced distribution equals the normalized weights
    with no floating-point error.
    """

    __slots__ = ("_weights", "_n", "_lcm", "_q", "_alias")

    def __init__(self, weights: Sequence):
        fracs = [_as_fraction(w, i) for i, w in enumerate(weights)]
        n = len(fracs)
        if n == 0:
            raise ValueError("at least one weight is required")
        total = sum(fracs)
        if total <= 0:
            raise ValueError("at least one weight must be positive")
        q, alias, lcm = self._build(fracs, total)
        self._weights = tuple(fracs)
        self._n = n
        self._lcm = lcm
        self._q = q
        self._alias = alias

    # ------------------------------------------------------------------
    # construction (exact Vose algorithm over Fraction)
    # ------------------------------------------------------------------
    @staticmethod
    def _build(fracs: list[Fraction], total: Fraction):
        n = len(fracs)
        scaled = [w * n / total for w in fracs]  # mean exactly 1
        prob: list[Fraction | None] = [None] * n
        alias = list(range(n))
        small = [i for i, p in enumerate(scaled) if p < 1]
        large = [i for i, p in enumerate(scaled) if p > 1]
        for i, p in enumerate(scaled):
            if p == 1:
                prob[i] = Fraction(1)
        while small and large:
            s = small.pop()
            g = large.pop()
            prob[s] = scaled[s]
            alias[s] = g
            scaled[g] += scaled[s] - 1
            if scaled[g] < 1:
                small.append(g)
            elif scaled[g] > 1:
                large.append(g)
            else:
                prob[g] = Fraction(1)
        # With exact arithmetic every residual is precisely 1.
        for i in small + large:
            assert scaled[i] == 1, f"construction residual {scaled[i]} != 1"
            prob[i] = Fraction(1)
        lcm = 1
        for p in prob:
            lcm = _lcm(lcm, p.denominator)
        q = [int(p * lcm) for p in prob]
        return q, alias, lcm

    # ------------------------------------------------------------------
    # sampling
    # ------------------------------------------------------------------
    def sample_one(self, source: UnbiasedIntSource) -> int:
        """Draw one item index using exactly two unbiased integers."""
        col = source.randbelow(self._n)
        if not 0 <= col < self._n:
            raise ValueError(f"random source returned out-of-range column {col}")
        r = source.randbelow(self._lcm)
        if not 0 <= r < self._lcm:
            raise ValueError(f"random source returned out-of-range threshold {r}")
        return col if r < self._q[col] else self._alias[col]

    def sample(self, source: UnbiasedIntSource, k: int = 1) -> list[int]:
        """Draw ``k`` item indices with replacement."""
        if k < 0:
            raise ValueError(f"k must be >= 0, got {k}")
        return [self.sample_one(source) for _ in range(k)]

    # ------------------------------------------------------------------
    # exact distribution introspection
    # ------------------------------------------------------------------
    @property
    def weights(self) -> tuple[Fraction, ...]:
        return self._weights

    @property
    def total_weight(self) -> Fraction:
        return sum(self._weights)

    @property
    def normalized_weights(self) -> list[Fraction]:
        total = self.total_weight
        return [w / total for w in self._weights]

    def probabilities(self) -> list[Fraction]:
        """Reconstruct the exact per-item probabilities from the table.

        P(j) = (q[j] + sum_{i: alias[i] == j} (lcm - q[i])) / (n * lcm)
        """
        counts = [Fraction(0)] * self._n
        for col in range(self._n):
            counts[col] += self._q[col]
            counts[self._alias[col]] += self._lcm - self._q[col]
        denom = self._n * self._lcm
        return [c / denom for c in counts]

    def enumerate_outcomes(self) -> list[int]:
        """Return the item produced by every (column, threshold) pair.

        The returned list has length ``n * lcm``; each outcome is equally
        likely under an unbiased source, so item frequencies in this list
        are the exact sampling distribution.
        """
        out = []
        for col in range(self._n):
            cut = self._q[col]
            other = self._alias[col]
            for r in range(self._lcm):
                out.append(col if r < cut else other)
        return out

    # ------------------------------------------------------------------
    # serialization (bit-exact round trip)
    # ------------------------------------------------------------------
    def to_dict(self) -> dict:
        return {
            "format": _FORMAT_TAG,
            "weights": [f"{w.numerator}/{w.denominator}" for w in self._weights],
            "lcm": str(self._lcm),
            "q": [str(x) for x in self._q],
            "alias": list(self._alias),
        }

    def dumps(self) -> str:
        return json.dumps(self.to_dict(), sort_keys=True)

    @classmethod
    def from_dict(cls, data: dict) -> "AliasTable":
        if data.get("format") != _FORMAT_TAG:
            raise ValueError(f"unsupported format tag: {data.get('format')!r}")
        weights = [Fraction(s) for s in data["weights"]]
        lcm = int(data["lcm"])
        q = [int(x) for x in data["q"]]
        alias = [int(a) for a in data["alias"]]
        n = len(weights)
        if not (len(q) == len(alias) == n):
            raise ValueError("corrupt table: column count mismatch")
        if lcm <= 0 or any(not 0 <= x <= lcm for x in q):
            raise ValueError("corrupt table: thresholds out of range")
        if any(not 0 <= a < n for a in alias):
            raise ValueError("corrupt table: alias index out of range")
        obj = cls.__new__(cls)
        obj._weights = tuple(weights)
        obj._n = n
        obj._lcm = lcm
        obj._q = q
        obj._alias = alias
        # Integrity check: the restored table must reproduce the normalized
        # weights exactly, otherwise the payload was tampered with.
        if obj.total_weight <= 0:
            raise ValueError("corrupt table: no positive weight")
        if obj.probabilities() != obj.normalized_weights:
            raise ValueError("corrupt table: does not reproduce its weights")
        return obj

    @classmethod
    def loads(cls, payload: str) -> "AliasTable":
        return cls.from_dict(json.loads(payload))

    def __eq__(self, other) -> bool:
        if not isinstance(other, AliasTable):
            return NotImplemented
        return (
            self._weights == other._weights
            and self._lcm == other._lcm
            and self._q == other._q
            and self._alias == other._alias
        )

    def __repr__(self) -> str:  # pragma: no cover - cosmetic
        return f"AliasTable(n={self._n}, lcm={self._lcm})"


class SamplingSession:
    """A pinned view of one published table version.

    Sessions opened before a weight update keep sampling the old
    distribution; the session records how many samples it has drawn.
    """

    __slots__ = ("_table", "version", "samples_drawn")

    def __init__(self, table: AliasTable, version: int):
        self._table = table
        self.version = version
        self.samples_drawn = 0

    @property
    def table(self) -> AliasTable:
        return self._table

    def sample(self, source: UnbiasedIntSource, k: int = 1) -> list[int]:
        draws = self._table.sample(source, k)
        self.samples_drawn += k
        return draws

    def sample_one(self, source: UnbiasedIntSource) -> int:
        self.samples_drawn += 1
        return self._table.sample_one(source)


class AliasTablePublisher:
    """Publishes alias tables with atomic dual-version swaps.

    ``update_weights`` builds the new table completely before swapping the
    reference (a single assignment, atomic under the GIL), so concurrent
    readers always see a fully constructed old or new table, never a
    half-built one. Existing sessions keep their pinned table.
    """

    def __init__(self, weights: Sequence):
        self._table = AliasTable(weights)
        self._version = 1

    @property
    def version(self) -> int:
        return self._version

    @property
    def current_table(self) -> AliasTable:
        return self._table

    def update_weights(self, weights: Sequence) -> int:
        """Build and publish a new table; returns the new version number."""
        new_table = AliasTable(weights)  # fully built before publishing
        self._table = new_table  # atomic release
        self._version += 1
        return self._version

    def open_session(self) -> SamplingSession:
        return SamplingSession(self._table, self._version)
