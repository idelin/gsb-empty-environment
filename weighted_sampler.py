"""Exact weighted random sampling over a Fenwick tree of rational weights.

Probability model (exact, all weights are fractions.Fraction)
-------------------------------------------------------------
With replacement:  P(index i) = w_i / T,  T = sum of weights.

Without replacement (sequential conditional definition): an ordered batch
(i_1, ..., i_k) is produced by repeatedly sampling proportional to the
*current* weights and removing the drawn item, so

    P(i_1, ..., i_k) = prod_{t=1..k}  w_{i_t} / (T - sum_{s<t} w_{i_s}).

The probability of an unordered set is the sum over its permutations.

Sampling mechanism (exact)
--------------------------
Let D = lcm of the weight denominators and N = T * D (an integer). A uniform
integer r in [0, N) is drawn by rejection sampling (see random_sources.py;
no modulo is ever used). Index i is selected iff r lies in the half-open
interval [cum_i, cum_i + w_i * D). Each r has probability exactly 1/N, hence
P(i) = w_i * D / N = w_i / T exactly. A zero weight gives an empty interval,
so a zero-weight index can never be selected. A total weight of zero raises
ZeroTotalWeightError (explicit failure).

Concurrency / versioning
------------------------
Every mutation increments a monotonic `version`. snapshot() freezes the
current weights into an independent copy; later updates to the live sampler
never affect existing snapshots.

Transactions (sampling without replacement, with rollback)
----------------------------------------------------------
tx = sampler.begin() snapshots the weights and the random-source state.
tx.draw()/tx.sample(k) draw without replacement by zeroing drawn weights.
  - commit():   drawn weights stay zero; consumed random values stay consumed.
  - rollback(): weights AND the random-source state are restored, i.e. the
                random values drawn inside the transaction are NOT consumed.
Do not call add() while a transaction is open (rollback restores exactly the
indices that existed at begin()).
"""

from __future__ import annotations

from collections import namedtuple
from fractions import Fraction
from math import lcm
from typing import Iterable, Optional, Sequence, Tuple

from random_sources import SeededRandomSource


class ZeroTotalWeightError(RuntimeError):
    """Raised when sampling is requested while the total weight is zero."""


DrawInfo = namedtuple("DrawInfo", ["r", "denominator", "scaled_total", "index"])


class WeightedSampler:
    """Fenwick tree over exact rational weights with O(log n) updates/draws."""

    def __init__(self, weights: Iterable = (), source=None):
        if source is None:
            raise ValueError(
                "a random source must be injected by the caller "
                "(e.g. SeededRandomSource(seed))"
            )
        self.source = source
        self._weights = [self._checked(w) for w in weights]
        n = max(1, len(self._weights))
        self._capacity = 1 << (n - 1).bit_length()
        self._tree = [Fraction(0)] * (self._capacity + 1)
        self._total = Fraction(0)
        for i, w in enumerate(self._weights):
            self._fenwick_add(i, w)
            self._total += w
        self._version = 0
        self.last_draw_info: Optional[DrawInfo] = None

    # ------------------------------------------------------------------ utils

    @staticmethod
    def _checked(w) -> Fraction:
        if isinstance(w, Fraction):
            f = w
        elif isinstance(w, int):
            f = Fraction(w)
        else:
            raise TypeError(
                f"weights must be int or Fraction (exact rationals), got {type(w).__name__}"
            )
        if f < 0:
            raise ValueError("weights must be non-negative")
        return f

    def _fenwick_add(self, i: int, delta: Fraction) -> None:
        i += 1
        while i <= self._capacity:
            self._tree[i] += delta
            i += i & (-i)

    def _rebuild_tree(self) -> None:
        self._tree = [Fraction(0)] * (self._capacity + 1)
        for i, w in enumerate(self._weights):
            self._fenwick_add(i, w)

    def _find(self, target: Fraction) -> int:
        """Smallest index whose prefix sum is strictly greater than target."""
        idx = 0
        acc = Fraction(0)
        bit = 1 << (self._capacity.bit_length() - 1)
        while bit:
            nxt = idx + bit
            if nxt <= self._capacity and acc + self._tree[nxt] <= target:
                idx = nxt
                acc += self._tree[nxt]
            bit >>= 1
        return idx

    # -------------------------------------------------------------- inspection

    def __len__(self) -> int:
        return len(self._weights)

    @property
    def version(self) -> int:
        return self._version

    @property
    def weights(self) -> Tuple[Fraction, ...]:
        return tuple(self._weights)

    def weight(self, i: int) -> Fraction:
        return self._weights[i]

    def total(self) -> Fraction:
        return self._total

    def common_denominator(self) -> int:
        d = 1
        for w in self._weights:
            d = lcm(d, w.denominator)
        return d

    def intervals(self) -> Sequence[Tuple[Fraction, Fraction]]:
        """Half-open cumulative intervals [lo, hi) for each index (exact)."""
        out = []
        acc = Fraction(0)
        for w in self._weights:
            out.append((acc, acc + w))
            acc += w
        return out

    def locate(self, target: Fraction) -> int:
        """Index whose interval contains `target` in [0, total) (exact)."""
        target = Fraction(target)
        if not 0 <= target < self._total:
            raise ValueError(f"target {target} outside [0, {self._total})")
        return self._find(target)

    # --------------------------------------------------------------- mutation

    def _apply(self, i: int, w: Fraction) -> None:
        delta = w - self._weights[i]
        if delta:
            self._weights[i] = w
            self._fenwick_add(i, delta)
            self._total += delta

    def set_weight(self, i: int, w) -> None:
        if not 0 <= i < len(self._weights):
            raise IndexError(f"index {i} out of range")
        self._apply(i, self._checked(w))
        self._version += 1

    def add(self, w) -> int:
        """Append a new item; returns its index. Capacity doubles as needed."""
        w = self._checked(w)
        if len(self._weights) == self._capacity:
            self._capacity *= 2
            self._rebuild_tree()
        self._weights.append(w)
        self._fenwick_add(len(self._weights) - 1, w)
        self._total += w
        self._version += 1
        return len(self._weights) - 1

    def remove(self, i: int) -> None:
        """Logically remove an item (its weight becomes exactly zero)."""
        self.set_weight(i, Fraction(0))

    # --------------------------------------------------------------- sampling

    def draw(self) -> int:
        """Draw one index with P(i) = w_i / total (exact)."""
        if self._total <= 0:
            raise ZeroTotalWeightError(
                "total weight is zero; weighted sampling is undefined"
            )
        d = self.common_denominator()
        scaled = self._total * d
        n = scaled.numerator  # scaled is an integer because d clears all denominators
        r = self.source.randbelow(n)
        index = self._find(Fraction(r, d))
        self.last_draw_info = DrawInfo(r=r, denominator=d, scaled_total=n, index=index)
        return index

    def begin(self) -> "Transaction":
        return Transaction(self)

    def sample_without_replacement(self, k: int) -> Sequence[int]:
        """Convenience: committed transaction of k draws without replacement."""
        tx = self.begin()
        try:
            result = tx.sample(k)
        except Exception:
            tx.rollback()
            raise
        tx.commit()
        return result

    def snapshot(self, source=None) -> "WeightedSampler":
        """Frozen copy of the current weights at the current version.

        The snapshot shares this sampler's random source unless `source` is
        given; weights are fully independent of later updates.
        """
        snap = WeightedSampler(
            self._weights, source if source is not None else self.source
        )
        snap._version = self._version
        return snap


class Transaction:
    """Without-replacement sampling session with commit/rollback semantics."""

    def __init__(self, sampler: WeightedSampler):
        self._sampler = sampler
        self._saved_weights = list(sampler._weights)
        self._saved_total = sampler._total
        self._saved_source_state = sampler.source.getstate()
        self._drawn = []
        self._closed = False

    def _check_open(self) -> None:
        if self._closed:
            raise RuntimeError("transaction is already closed")

    @property
    def drawn(self) -> Tuple[int, ...]:
        return tuple(self._drawn)

    def draw(self) -> int:
        """Draw one item without replacement (its weight is zeroed)."""
        self._check_open()
        s = self._sampler
        index = s.draw()  # raises ZeroTotalWeightError when nothing remains
        s.set_weight(index, Fraction(0))
        self._drawn.append(index)
        return index

    def sample(self, k: int) -> Sequence[int]:
        """k consecutive draws without replacement; no duplicates possible."""
        if k < 0:
            raise ValueError("k must be non-negative")
        return [self.draw() for _ in range(k)]

    def commit(self) -> None:
        """Keep drawn weights at zero; consumed random values stay consumed."""
        self._check_open()
        self._closed = True

    def rollback(self) -> None:
        """Restore weights AND random-source state (random values unconsumed)."""
        self._check_open()
        s = self._sampler
        for i, w in enumerate(self._saved_weights):
            if s._weights[i] != w:
                s._apply(i, w)
        s._total = self._saved_total
        s._version += 1
        s.source.setstate(self._saved_source_state)
        self._closed = True
