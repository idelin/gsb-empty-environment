"""Response channel definitions for binary and k-ary randomized response (RR).

Definitions
-----------
A *response channel* is a known column-stochastic matrix ``A`` with

    A[y][x] = P(report y | true value x),   x, y in {0, ..., k-1}

so each column sums to 1.  For k-ary randomized response with privacy
parameter ``epsilon >= 0`` we use the standard symmetric mechanism

    p_stay = e^eps / (e^eps + k - 1)        (report the true value)
    q      = 1 / (e^eps + k - 1)            (report each other value)

i.e. ``A = (p_stay - q) * I + q * J`` with ``J`` the all-ones matrix.

Special cases
-------------
* ``k = 2`` is the classical binary (Warner-style) randomized response.
* ``k = 1`` is a degenerate special case: the alphabet has a single value,
  the channel is the 1x1 matrix [[1]], the output carries no information,
  and ``epsilon`` is meaningless (accepted but ignored).
* ``epsilon = 0`` gives ``p_stay = q = 1/k``: every output is uniform and
  independent of the input.  The channel has rank 1 and the input
  distribution is *not identifiable* from the outputs.

These mechanisms are widely known to satisfy epsilon-differential privacy;
this library only implements the statistics and does NOT attempt any formal
privacy proof.
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from .linalg import mat_vec


def validate_epsilon(epsilon: float) -> float:
    """Validate the privacy parameter; returns it as a float."""
    if isinstance(epsilon, bool) or not isinstance(epsilon, (int, float)):
        raise TypeError(f"epsilon must be a real number, got {type(epsilon).__name__}")
    epsilon = float(epsilon)
    if not math.isfinite(epsilon):
        raise ValueError(f"epsilon must be finite, got {epsilon}")
    if epsilon < 0.0:
        raise ValueError(f"epsilon must be >= 0, got {epsilon}")
    return epsilon


def validate_k(k: int) -> int:
    """Validate the alphabet size; returns it as an int."""
    if isinstance(k, bool) or not isinstance(k, int):
        raise TypeError(f"k must be an integer, got {type(k).__name__}")
    if k < 1:
        raise ValueError(f"k must be >= 1, got {k}")
    return k


@dataclass(frozen=True)
class RandomizedResponseChannel:
    """Symmetric k-ary randomized-response channel.

    Attributes
    ----------
    k:        alphabet size (k >= 1; k = 2 is binary RR).
    epsilon:  privacy parameter (epsilon >= 0).  epsilon = 0 makes the
              output independent of the input (unidentifiable channel).
    """

    k: int
    epsilon: float

    def __post_init__(self) -> None:
        object.__setattr__(self, "k", validate_k(self.k))
        object.__setattr__(self, "epsilon", validate_epsilon(self.epsilon))

    @property
    def is_degenerate(self) -> bool:
        """True when the output is independent of the input (eps = 0 or k = 1)."""
        return self.k == 1 or self.epsilon == 0.0

    @property
    def p_stay(self) -> float:
        """Probability of reporting the true value (1.0 when k = 1)."""
        if self.k == 1:
            return 1.0
        e = math.exp(self.epsilon)
        return e / (e + self.k - 1.0)

    @property
    def q_off(self) -> float:
        """Probability of reporting any specific other value (0.0 when k = 1)."""
        if self.k == 1:
            return 0.0
        e = math.exp(self.epsilon)
        return 1.0 / (e + self.k - 1.0)

    @property
    def matrix(self) -> list[list[float]]:
        """Channel matrix A with A[y][x] = P(output y | input x)."""
        if self.k == 1:
            return [[1.0]]
        p, q = self.p_stay, self.q_off
        return [[p if y == x else q for x in range(self.k)] for y in range(self.k)]

    def output_distribution(self, input_dist: list[float]) -> list[float]:
        """Theoretical output distribution A x for an input distribution x."""
        validate_distribution(input_dist, self.k)
        return mat_vec(self.matrix, input_dist)

    def sample(self, true_value: int, rng) -> int:
        """Draw one synthetic randomized response for a given true value."""
        if not 0 <= true_value < self.k:
            raise ValueError(f"true_value must be in [0, {self.k}), got {true_value}")
        if self.k == 1:
            return 0
        u = rng.random()
        if u < self.p_stay:
            return true_value
        # Uniform over the k-1 other values.
        offset = 1 + int((u - self.p_stay) / (1.0 - self.p_stay) * (self.k - 1))
        offset = min(offset, self.k - 1)
        return (true_value + offset) % self.k


def validate_distribution(x: list[float], k: int, tol: float = 1e-9) -> None:
    """Check that x is a valid probability distribution over k values."""
    if len(x) != k:
        raise ValueError(f"distribution must have {k} entries, got {len(x)}")
    if any((not math.isfinite(xi)) or xi < 0.0 for xi in x):
        raise ValueError(f"distribution entries must be finite and >= 0, got {x}")
    if abs(sum(x) - 1.0) > tol:
        raise ValueError(f"distribution must sum to 1, got sum={sum(x)}")


def validate_counts(counts: list[int], k: int) -> int:
    """Check that counts are non-negative integers of length k; returns total."""
    if len(counts) != k:
        raise ValueError(f"counts must have {k} entries, got {len(counts)}")
    for c in counts:
        if isinstance(c, bool) or not isinstance(c, int):
            raise TypeError(f"counts must be integers, got {type(c).__name__}")
        if c < 0:
            raise ValueError(f"counts must be >= 0, got {counts}")
    total = sum(counts)
    if total <= 0:
        raise ValueError("total count must be positive")
    return total
