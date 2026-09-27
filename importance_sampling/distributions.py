"""Sampleable distributions over finite discrete domains.

All sampling goes through a caller-injected ``random.Random`` instance so
that every run is reproducible from a caller-controlled seed.
"""

from __future__ import annotations

import bisect
import math
from typing import Dict, Hashable, List, Optional, Sequence


class DiscreteDistribution:
    """A normalized probability mass function over a finite domain.

    ``pmf`` maps domain points to non-negative weights; weights are
    normalized internally, so unnormalized input is accepted.
    """

    def __init__(self, pmf: Dict[Hashable, float], name: Optional[str] = None):
        if not pmf:
            raise ValueError("pmf must not be empty")
        cleaned = {x: float(w) for x, w in pmf.items()}
        for x, w in cleaned.items():
            if w < 0.0:
                raise ValueError(f"negative probability at {x!r}: {w}")
        total = sum(cleaned.values())
        if total <= 0.0:
            raise ValueError("pmf weights must have a positive sum")
        self._pmf = {x: w / total for x, w in cleaned.items()}
        self.name = name or "discrete"
        positive = [(x, p) for x, p in self._pmf.items() if p > 0.0]
        self._points: List[Hashable] = [x for x, _ in positive]
        cumulative: List[float] = []
        running = 0.0
        for _, p in positive:
            running += p
            cumulative.append(running)
        cumulative[-1] = 1.0  # guard against float drift
        self._cumulative = cumulative

    def pdf(self, x: Hashable) -> float:
        return self._pmf.get(x, 0.0)

    def log_pdf(self, x: Hashable) -> float:
        p = self.pdf(x)
        return math.log(p) if p > 0.0 else -math.inf

    def support(self) -> List[Hashable]:
        """Points with strictly positive probability."""
        return list(self._points)

    def sample(self, rng) -> Hashable:
        u = rng.random()
        index = bisect.bisect_left(self._cumulative, u)
        return self._points[min(index, len(self._points) - 1)]

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"DiscreteDistribution({self._pmf!r}, name={self.name!r})"


class MixtureProposal:
    """A finite mixture of proposal distributions.

    The generating density of a draw is the *mixture* density
    ``sum_k alpha_k * q_k(x)``, regardless of which component produced the
    point, so importance weights must use that mixture density as the
    denominator.  This class makes that automatic: ``log_pdf`` is the
    mixture log-density.
    """

    def __init__(
        self,
        components: Sequence[DiscreteDistribution],
        weights: Sequence[float],
        name: Optional[str] = None,
    ):
        if not components:
            raise ValueError("a mixture needs at least one component")
        if len(components) != len(weights):
            raise ValueError("components and weights must have equal length")
        if any(w < 0.0 for w in weights):
            raise ValueError("mixture weights must be non-negative")
        total = float(sum(weights))
        if total <= 0.0:
            raise ValueError("mixture weights must have a positive sum")
        self.components = list(components)
        self.weights = [float(w) / total for w in weights]
        self.name = name or "mixture"
        cumulative: List[float] = []
        running = 0.0
        for w in self.weights:
            running += w
            cumulative.append(running)
        cumulative[-1] = 1.0
        self._cumulative = cumulative

    def pdf(self, x: Hashable) -> float:
        return sum(a * q.pdf(x) for a, q in zip(self.weights, self.components))

    def log_pdf(self, x: Hashable) -> float:
        p = self.pdf(x)
        return math.log(p) if p > 0.0 else -math.inf

    def support(self) -> List[Hashable]:
        seen: Dict[Hashable, None] = {}
        for q in self.components:
            for x in q.support():
                seen.setdefault(x)
        return list(seen)

    def sample(self, rng) -> Hashable:
        u = rng.random()
        index = bisect.bisect_left(self._cumulative, u)
        component = self.components[min(index, len(self.components) - 1)]
        return component.sample(rng)
