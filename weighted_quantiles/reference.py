"""Independent naive reference implementation.

Keeps raw samples and performs a *full sort* on every query.  Used to
cross-check the treap-based store on random transaction sequences; it
trades speed for obviousness.  ``sorts`` / ``scanned`` count its work so
reports can contrast it with the treap's node-visit counts.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Any, Dict, List, Tuple


class NaiveWeightedReference:
    def __init__(self) -> None:
        self._samples: Dict[Any, Tuple[Any, Fraction, Any, Any]] = {}
        self.sorts = 0    # full sorts performed
        self.scanned = 0  # rows scanned during queries

    # ------------------------------------------------------------------
    # mutation (same contract as WeightedStore)
    # ------------------------------------------------------------------
    def upsert(self, obs_id: Any, value: Any, weight: Any,
               subject: Any = None, timestamp: Any = None) -> None:
        w = Fraction(weight)
        if w < 0:
            raise ValueError("negative weights are rejected")
        if w == 0:
            self._samples.pop(obs_id, None)
            return
        self._samples[obs_id] = (value, w, subject, timestamp)

    def retract(self, obs_id: Any) -> None:
        self._samples.pop(obs_id, None)

    def retract_subject(self, subject: Any) -> None:
        doomed = [k for k, v in self._samples.items() if v[2] == subject]
        for k in doomed:
            del self._samples[k]

    def advance(self, now: Any, window: Any) -> None:
        cutoff = now - window
        doomed = [k for k, v in self._samples.items()
                  if v[3] is not None and v[3] <= cutoff]
        for k in doomed:
            del self._samples[k]

    # ------------------------------------------------------------------
    # queries
    # ------------------------------------------------------------------
    def _sorted_merged(self) -> List[Tuple[Any, Fraction]]:
        merged: Dict[Any, Fraction] = {}
        for value, w, _, _ in self._samples.values():
            merged[value] = merged.get(value, Fraction(0)) + w
        self.sorts += 1
        return sorted((v, w) for v, w in merged.items() if w > 0)

    def total_weight(self) -> Fraction:
        return sum((w for _, w, _, _ in self._samples.values()), Fraction(0))

    def quantile_interval(self, q: Any) -> Tuple[Any, Any]:
        q = Fraction(q)
        rows = self._sorted_merged()
        total = sum((w for _, w in rows), Fraction(0))
        if total == 0:
            raise ValueError("quantile of empty reference")
        if q == 0:
            return rows[0][0], rows[0][0]
        target = q * total
        cum = Fraction(0)
        for i, (value, w) in enumerate(rows):
            self.scanned += 1
            cum += w
            if target < cum:
                return value, value
            if target == cum:
                if i + 1 < len(rows):
                    return value, rows[i + 1][0]
                return value, value
        raise AssertionError("unreachable")

    def cdf(self, x: Any) -> Fraction:
        total = self.total_weight()
        if total == 0:
            raise ValueError("cdf of empty reference")
        acc = sum((w for v, w, _, _ in self._samples.values() if v <= x),
                  Fraction(0))
        self.scanned += len(self._samples)
        return Fraction(acc, total)
