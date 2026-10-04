"""Finite-state Hidden Markov Model definition and parameter validation."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Hashable, Mapping

#: Sentinel marking a missing observation.  ``None`` is reserved for this
#: purpose and may not be used as an emission symbol.
MISSING = None

_TOL = 1e-9


class HMMValidationError(ValueError):
    """Raised when model parameters are not valid probability distributions."""


def _check_distribution(weights, label: str, tol: float = _TOL) -> None:
    total = 0.0
    for i, w in enumerate(weights):
        if isinstance(w, bool) or not isinstance(w, (int, float)):
            raise HMMValidationError(f"{label}[{i}] is not a real number: {w!r}")
        if w < 0.0:
            raise HMMValidationError(f"{label}[{i}] is negative: {w!r}")
        total += w
    if abs(total - 1.0) > tol:
        raise HMMValidationError(f"{label} does not sum to 1 (sum={total!r})")


def validate_transition_matrix(matrix, n_states: int, label: str = "transition"):
    """Validate a row-stochastic matrix and return it as a tuple of tuples."""
    if len(matrix) != n_states:
        raise HMMValidationError(
            f"{label} must have {n_states} rows, got {len(matrix)}"
        )
    rows = []
    for i, row in enumerate(matrix):
        if len(row) != n_states:
            raise HMMValidationError(
                f"{label}[{i}] must have {n_states} columns, got {len(row)}"
            )
        row = tuple(float(v) for v in row)
        _check_distribution(row, f"{label}[{i}]")
        rows.append(row)
    return tuple(rows)


@dataclass(frozen=True)
class HMM:
    """A finite HMM with states ``0 .. n_states - 1``.

    - ``initial``: initial state distribution, length ``n_states``.
    - ``transition``: row-stochastic ``n_states x n_states`` matrix.
    - ``emission``: per-state mapping ``observation -> probability``.  Each
      mapping must be a valid distribution over its own support; symbols
      outside the support have emission probability exactly 0.
    """

    n_states: int
    initial: tuple
    transition: tuple
    emission: tuple
    name: str = "hmm"

    def __post_init__(self) -> None:
        if isinstance(self.n_states, bool) or not isinstance(self.n_states, int):
            raise HMMValidationError(f"n_states must be an int, got {self.n_states!r}")
        if self.n_states < 1:
            raise HMMValidationError("n_states must be >= 1")
        if len(self.initial) != self.n_states:
            raise HMMValidationError(
                f"initial must have {self.n_states} entries, got {len(self.initial)}"
            )
        object.__setattr__(self, "initial", tuple(float(v) for v in self.initial))
        _check_distribution(self.initial, "initial")
        if len(self.transition) != self.n_states:
            raise HMMValidationError(
                f"transition must have {self.n_states} rows, got {len(self.transition)}"
            )
        object.__setattr__(
            self, "transition", validate_transition_matrix(self.transition, self.n_states)
        )
        if len(self.emission) != self.n_states:
            raise HMMValidationError(
                f"emission must have {self.n_states} entries, got {len(self.emission)}"
            )
        normed = []
        for i, dist in enumerate(self.emission):
            if not isinstance(dist, Mapping):
                raise HMMValidationError(f"emission[{i}] is not a mapping: {dist!r}")
            if MISSING in dist:
                raise HMMValidationError(
                    f"emission[{i}] uses None as a symbol; None is reserved for "
                    "missing observations (hmm.MISSING)"
                )
            d = {k: float(v) for k, v in dist.items()}
            _check_distribution(tuple(d.values()), f"emission[{i}]")
            normed.append(d)
        object.__setattr__(self, "emission", tuple(normed))

    def emission_prob(self, state: int, obs: Hashable) -> float:
        """Emission probability; a missing observation carries no information."""
        if obs is MISSING:
            return 1.0
        return self.emission[state].get(obs, 0.0)
