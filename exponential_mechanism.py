"""Exponential mechanism over legal subsets of a finite candidate set.

The distribution is defined over *legal subsets* of candidates (subsets of a
fixed size ``budget`` that respect group quotas and mutual-exclusion pairs),
not over individual items::

    P[S]  proportional to  exp(epsilon * u(S) / (2 * sensitivity)),
    u(S) = sum of the integer scores of the members of S.

Design notes
------------
* Scores are finite integers; epsilon and sensitivity are finite floats.
* All probability computation is done in log-space with a log-sum-exp
  normalization, so extreme scores cannot overflow float64.
* ``sensitivity == 0`` is handled by an explicit deterministic rule (uniform
  over the max-utility legal subsets) instead of dividing by zero.
* The legal domain is enumerated exhaustively.  If enumeration would exceed
  ``max_domain_size`` the mechanism raises :class:`InfeasibleError` rather
  than silently truncating candidates and misreporting the distribution.
* ``exact_probabilities`` recomputes the same distribution with
  ``decimal.Decimal`` at high precision; it serves as an exact reference for
  small domains so the float64 approximation error can be measured.
* ``sample`` draws from a seeded ``random.Random`` and returns a
  :class:`SamplingRecord` (seed, raw uniform draw, cumulative interval) so
  every draw can be replayed and audited.

Synthetic data only.  Nothing in this module is a formal privacy proof.
"""

from __future__ import annotations

import math
import random
import secrets
from dataclasses import dataclass, field
from decimal import Decimal, localcontext
from itertools import combinations
from typing import Dict, Mapping, Optional, Sequence, Tuple

__all__ = [
    "Candidate",
    "Constraints",
    "ExponentialMechanism",
    "InfeasibleError",
    "SamplingRecord",
]


class InfeasibleError(ValueError):
    """No legal subset exists, or the legal domain is too large to enumerate.

    Raised instead of truncating the candidate set, so a caller can never
    mistake a truncated computation for the original distribution.
    """


@dataclass(frozen=True)
class Candidate:
    """A single candidate with a finite integer score."""

    cid: str
    score: int
    group: str = "default"

    def __post_init__(self) -> None:
        if not isinstance(self.cid, str) or not self.cid:
            raise ValueError("cid must be a non-empty string")
        if isinstance(self.score, bool) or not isinstance(self.score, int):
            raise TypeError("score must be an integer (finite integer scores only)")
        if not isinstance(self.group, str) or not self.group:
            raise ValueError("group must be a non-empty string")


@dataclass(frozen=True)
class Constraints:
    """Feasibility rules defining the legal subsets.

    budget:       exact number of candidates per subset.
    group_quotas: group -> maximum number of members of that group per subset.
                  Groups not listed are unconstrained.
    mutex_pairs:  pairs of candidate ids that may not co-occur in a subset.
    """

    budget: int
    group_quotas: Mapping[str, int] = field(default_factory=dict)
    mutex_pairs: frozenset = frozenset()

    def __post_init__(self) -> None:
        if isinstance(self.budget, bool) or not isinstance(self.budget, int):
            raise TypeError("budget must be an integer")
        if self.budget < 0:
            raise ValueError("budget must be >= 0")
        for group, quota in self.group_quotas.items():
            if isinstance(quota, bool) or not isinstance(quota, int) or quota < 0:
                raise ValueError(f"quota for group {group!r} must be an integer >= 0")
        normalized = set()
        for pair in self.mutex_pairs:
            pair = frozenset(pair)
            if len(pair) != 2:
                raise ValueError("each mutex entry must be a pair of two distinct ids")
            normalized.add(pair)
        object.__setattr__(self, "mutex_pairs", frozenset(normalized))


@dataclass(frozen=True)
class SamplingRecord:
    """Auditable evidence of one random draw.

    ``draw`` lies in [cumulative_before, cumulative_after) under the fixed
    enumeration order, which identifies ``subset`` deterministically.
    Re-running ``mechanism.sample(record.seed)`` must reproduce this record.
    """

    seed: int
    draw: float
    subset: Tuple[str, ...]
    subset_probability: float
    cumulative_before: float
    cumulative_after: float
    domain_size: int


def _validate_probability_parameters(epsilon: float, sensitivity: float) -> None:
    for name, value in (("epsilon", epsilon), ("sensitivity", sensitivity)):
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError(f"{name} must be a real number")
        if math.isnan(value):
            raise ValueError(f"{name} must not be NaN")
        if value < 0:
            raise ValueError(f"{name} must be >= 0")
    if math.isinf(epsilon):
        raise ValueError("epsilon must be finite")
    if math.isinf(sensitivity):
        raise ValueError("sensitivity must be finite")


def _log_weights(epsilon: float, sensitivity: float, utilities: Sequence[int]) -> list:
    if sensitivity == 0:
        # Explicit deterministic rule: uniform over the max-utility subsets.
        best = max(utilities)
        return [0.0 if u == best else -math.inf for u in utilities]
    alpha = epsilon / (2.0 * sensitivity)  # may overflow to +inf; handled below
    weights = []
    for u in utilities:
        if u == 0:
            weights.append(0.0)  # avoid inf * 0 -> nan
        else:
            weights.append(alpha * u)  # may be +-inf, never nan
    return weights


def _normalize(log_weights: Sequence[float], utilities: Sequence[int]) -> list:
    """Log-sum-exp normalization; robust to +-inf log-weights."""
    n = len(log_weights)
    pos_inf = {i for i, lw in enumerate(log_weights) if lw == math.inf}
    if pos_inf:
        share = 1.0 / len(pos_inf)
        return [share if i in pos_inf else 0.0 for i in range(n)]
    m = max(log_weights)
    if m == -math.inf:
        # Every weight underflowed (e.g. alpha = +inf with all-negative
        # utilities).  The limit is uniform over the max-utility subsets.
        best = max(utilities)
        share = 1.0 / sum(1 for u in utilities if u == best)
        return [share if u == best else 0.0 for u in utilities]
    ws = [math.exp(lw - m) for lw in log_weights]
    z = math.fsum(ws)
    return [w / z for w in ws]


class ExponentialMechanism:
    """Exponential mechanism over the legal subsets of ``candidates``."""

    def __init__(
        self,
        candidates: Sequence[Candidate],
        constraints: Constraints,
        epsilon: float,
        sensitivity: float,
        max_domain_size: int = 200_000,
    ) -> None:
        candidates = list(candidates)
        ids = [c.cid for c in candidates]
        if len(set(ids)) != len(ids):
            raise ValueError("duplicate candidate ids are not allowed")
        _validate_probability_parameters(epsilon, sensitivity)
        if max_domain_size < 1:
            raise ValueError("max_domain_size must be >= 1")

        self.candidates: Tuple[Candidate, ...] = tuple(
            sorted(candidates, key=lambda c: c.cid)
        )
        self.constraints = constraints
        self.epsilon = float(epsilon)
        self.sensitivity = float(sensitivity)

        self._subsets = self._enumerate_legal_subsets(constraints, max_domain_size)
        if not self._subsets:
            raise InfeasibleError(
                "no legal subset exists under the given budget/quotas/mutex "
                "constraints; refusing to relax constraints or truncate candidates"
            )
        self._utilities = [
            sum(self._by_id[cid].score for cid in subset) for subset in self._subsets
        ]
        self._log_weights = _log_weights(self.epsilon, self.sensitivity, self._utilities)
        self._probabilities = _normalize(self._log_weights, self._utilities)

    # -- enumeration ----------------------------------------------------

    def _enumerate_legal_subsets(self, constraints: Constraints, max_domain_size: int):
        self._by_id: Dict[str, Candidate] = {c.cid: c for c in self.candidates}
        for pair in constraints.mutex_pairs:
            unknown = [cid for cid in pair if cid not in self._by_id]
            if unknown:
                raise ValueError(f"mutex pair references unknown candidate ids: {unknown}")

        n = len(self.candidates)
        k = constraints.budget
        if k > n:
            raise InfeasibleError(
                f"budget {k} exceeds candidate count {n}; "
                "refusing to silently reduce the budget"
            )
        upper = math.comb(n, k)
        if upper > max_domain_size:
            raise InfeasibleError(
                f"legal domain may contain up to {upper} subsets, exceeding "
                f"max_domain_size={max_domain_size}; refusing to truncate "
                "candidates and misreport the original distribution"
            )

        quotas = constraints.group_quotas
        mutex = constraints.mutex_pairs
        legal = []
        for combo in combinations(self.candidates, k):
            counts: Dict[str, int] = {}
            for c in combo:
                counts[c.group] = counts.get(c.group, 0) + 1
            if any(counts.get(g, 0) > q for g, q in quotas.items()):
                continue
            ids = frozenset(c.cid for c in combo)
            if any(pair <= ids for pair in mutex):
                continue
            legal.append(tuple(sorted(ids)))
        return legal

    # -- probabilities ---------------------------------------------------

    @property
    def subsets(self) -> Tuple[Tuple[str, ...], ...]:
        """All legal subsets, in the fixed (lexicographic) sampling order."""
        return tuple(self._subsets)

    @property
    def utilities(self) -> Dict[Tuple[str, ...], int]:
        return dict(zip(self._subsets, self._utilities))

    def probabilities(self) -> Dict[Tuple[str, ...], float]:
        """Normalized float64 probabilities over legal subsets (sum to 1)."""
        return dict(zip(self._subsets, self._probabilities))

    def exact_probabilities(self, precision: int = 50) -> Dict[Tuple[str, ...], Decimal]:
        """High-precision reference probabilities computed with Decimal.

        Uses the exact binary values of the float64 parameters.  With the
        default 50 significant digits this is effectively exact next to
        float64 and is used to measure the float approximation error.
        """
        with localcontext() as ctx:
            ctx.prec = precision
            zero, one = Decimal(0), Decimal(1)
            if self.sensitivity == 0:
                best = max(self._utilities)
                share = one / sum(1 for u in self._utilities if u == best)
                return {
                    s: share if u == best else zero
                    for s, u in zip(self._subsets, self._utilities)
                }
            alpha = Decimal(self.epsilon) / (Decimal(2) * Decimal(self.sensitivity))
            weights = [
                (alpha * u).exp() if u != 0 else one for u in self._utilities
            ]
            total = sum(weights, zero)
            return {s: w / total for s, w in zip(self._subsets, weights)}

    def max_probability_error(self, precision: int = 50) -> float:
        """Max |p_float - p_exact| over the domain (approximation error)."""
        exact = self.exact_probabilities(precision)
        return max(
            abs(p - float(exact[s])) for s, p in self.probabilities().items()
        )

    # -- sampling ---------------------------------------------------------

    def sample(self, seed: Optional[int] = None) -> SamplingRecord:
        """Draw one subset; return auditable evidence of the draw.

        The same seed always reproduces the same record (replayable audit).
        With ``seed=None`` a random seed is drawn from ``secrets`` and still
        recorded, so even unseeded draws remain auditable after the fact.
        """
        if seed is None:
            seed = secrets.randbelow(1 << 63)
        rng = random.Random(seed)
        draw = rng.random()
        cumulative = 0.0
        chosen = None
        chosen_index = -1
        before = after = 0.0
        for index, (subset, p) in enumerate(zip(self._subsets, self._probabilities)):
            lo, hi = cumulative, cumulative + p
            cumulative = hi
            if chosen is None and draw < hi:
                chosen = subset
                chosen_index = index
                before, after = lo, hi
        if chosen is None:
            # float64 rounding: draw >= every cumulative sum; take the last.
            chosen = self._subsets[-1]
            chosen_index = len(self._subsets) - 1
            before = cumulative - self._probabilities[-1]
            after = cumulative
        return SamplingRecord(
            seed=seed,
            draw=draw,
            subset=chosen,
            subset_probability=self._probabilities[chosen_index],
            cumulative_before=before,
            cumulative_after=after,
            domain_size=len(self._subsets),
        )

    def verify(self, record: SamplingRecord) -> bool:
        """Replay the draw from ``record.seed`` and check it matches exactly."""
        return self.sample(record.seed) == record
