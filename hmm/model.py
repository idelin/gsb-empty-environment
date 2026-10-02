"""Finite-state hidden Markov model definition and parameter validation."""
from __future__ import annotations

from bisect import bisect_right

SUM_TOLERANCE = 1e-9


class ModelValidationError(ValueError):
    """Raised when HMM parameters fail validation."""


def _is_number(x: object) -> bool:
    return isinstance(x, (int, float)) and not isinstance(x, bool)


def _check_probability_vector(vec, name: str) -> list[float]:
    if not vec:
        raise ModelValidationError(f"{name} must not be empty")
    out = []
    for x in vec:
        if not _is_number(x):
            raise ModelValidationError(f"{name} contains a non-numeric entry: {x!r}")
        if x < 0.0:
            raise ModelValidationError(f"{name} contains a negative probability: {x!r}")
        out.append(float(x))
    total = sum(out)
    if abs(total - 1.0) > SUM_TOLERANCE:
        raise ModelValidationError(
            f"{name} must sum to 1.0 (got {total!r}); a zero or unnormalised "
            "row cannot be silently renormalised"
        )
    return out


def _normalize_emission_row(row, state: int) -> dict:
    if isinstance(row, dict):
        items = list(row.items())
    else:
        items = list(enumerate(row))
    out = {}
    for symbol, p in items:
        if not _is_number(p):
            raise ModelValidationError(
                f"emission[{state}][{symbol!r}] is not numeric: {p!r}"
            )
        if p < 0.0:
            raise ModelValidationError(
                f"emission[{state}][{symbol!r}] is negative: {p!r}"
            )
        out[symbol] = float(p)
    total = sum(out.values())
    if abs(total - 1.0) > SUM_TOLERANCE:
        raise ModelValidationError(
            f"emission row for state {state} must sum to 1.0 (got {total!r})"
        )
    return out


class HMM:
    """Finite HMM with a homogeneous transition matrix.

    initial:    sequence of N initial-state probabilities.
    transition: N x N matrix, row i = P(next | current=i).
    emission:   length-N sequence; each row is either a dict {symbol: prob}
                or a sequence of probabilities indexed by integer symbols.
    An observation equal to None is treated as missing (no information).
    """

    def __init__(self, initial, transition, emission):
        self.initial = list(initial)
        self.n_states = len(self.initial)
        self._transitions = [[[float(p) for p in row] for row in transition]]
        self._boundaries: list[int] = []
        self.emission = list(emission)
        self._validate()

    def _validate(self) -> None:
        n = self.n_states
        self.initial = _check_probability_vector(self.initial, "initial")
        if len(self._transitions) < 1:
            raise ModelValidationError("at least one transition matrix is required")
        for k, matrix in enumerate(self._transitions):
            if len(matrix) != n:
                raise ModelValidationError(
                    f"transition matrix {k} has {len(matrix)} rows, expected {n}"
                )
            for i, row in enumerate(matrix):
                self._transitions[k][i] = _check_probability_vector(
                    row, f"transition[{k}][{i}]"
                )
        if len(self.emission) != n:
            raise ModelValidationError(
                f"emission has {len(self.emission)} rows, expected {n}"
            )
        self.emission = [
            _normalize_emission_row(row, s) for s, row in enumerate(self.emission)
        ]
        if len(self._boundaries) != len(self._transitions) - 1:
            raise ModelValidationError(
                "need exactly len(transitions) - 1 segment boundaries, got "
                f"{len(self._boundaries)} for {len(self._transitions)} matrices"
            )
        for a, b in zip(self._boundaries, self._boundaries[1:]):
            if not a < b:
                raise ModelValidationError(
                    f"segment boundaries must be strictly increasing: {self._boundaries}"
                )

    def transition_matrix(self, t: int) -> list[list[float]]:
        """Transition matrix used for the step t -> t+1."""
        return self._transitions[0]

    def emission_prob(self, state: int, obs) -> float:
        """P(obs | state); a missing observation (None) carries no information."""
        if obs is None:
            return 1.0
        return self.emission[state].get(obs, 0.0)

    @property
    def n_segments(self) -> int:
        return len(self._transitions)


class PiecewiseHMM(HMM):
    """HMM whose transition matrix changes at fixed segment boundaries.

    boundaries: strictly increasing step indices; step t -> t+1 uses
    transitions[k] where k = #{boundaries <= t}.
    """

    def __init__(self, initial, transitions, boundaries, emission):
        self.initial = list(initial)
        self.n_states = len(self.initial)
        self._transitions = [
            [[float(p) for p in row] for row in matrix] for matrix in transitions
        ]
        self._boundaries = [int(b) for b in boundaries]
        self.emission = list(emission)
        self._validate()

    def transition_matrix(self, t: int) -> list[list[float]]:
        return self._transitions[bisect_right(self._boundaries, t)]
