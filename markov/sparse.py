"""Sparse transition matrix with explicit size, indices, and validation."""

from __future__ import annotations

from enum import Enum
from fractions import Fraction

from .errors import (
    IndexOutOfRangeError,
    LineSumError,
    MissingLineError,
    NegativeProbabilityError,
)


class Convention(Enum):
    """Storage convention of a transition matrix.

    ROW:    line i is row i of P, i.e. P[i][j] = Pr(i -> j); rows sum to one.
    COLUMN: line j is column j of P, i.e. P[i][j] = Pr(j -> i); columns sum
            to one.

    In both conventions a line describes, for its source state, the
    distribution over successor states.  Both are normalized to a single
    canonical internal form ``lines[source] = {target: probability}`` so all
    algorithms are convention-independent.
    """

    ROW = "row"
    COLUMN = "column"


class SparseTransitionMatrix:
    """Sparse Markov transition matrix.

    Parameters
    ----------
    n : int
        Number of states.  Explicit and mandatory.
    lines : mapping
        ``{source: {target: probability}}``.  Every state in ``range(n)``
        must have an explicit line; a missing line raises
        :class:`MissingLineError` and is never silently filled.
    convention : Convention
        ROW or COLUMN semantics for ``lines`` (see :class:`Convention`).
    tol : float
        Tolerance for the line-sum check.  A line whose sum deviates from 1
        by more than ``tol`` raises :class:`LineSumError`; lines are never
        silently normalized.
    """

    def __init__(self, n, lines, convention=Convention.ROW, tol=1e-12):
        if not isinstance(n, int) or isinstance(n, bool) or n <= 0:
            raise ValueError("n must be a positive integer")
        self.n = n
        self.convention = Convention(convention)
        self.tol = tol
        lines = dict(lines)
        missing = [i for i in range(n) if i not in lines]
        if missing:
            raise MissingLineError(
                f"missing probability lines for states {missing}; "
                "every state requires an explicit line"
            )
        extra = [i for i in lines if not (isinstance(i, int) and 0 <= i < n)]
        if extra:
            raise IndexOutOfRangeError(
                f"line indices out of range 0..{n - 1}: {extra}"
            )
        self._lines = [None] * n
        for i in range(n):
            self._lines[i] = self._checked_line(i, lines[i])

    def _checked_line(self, i, entries):
        line = {}
        total = 0
        for j, p in dict(entries).items():
            if not (isinstance(j, int) and 0 <= j < self.n):
                raise IndexOutOfRangeError(
                    f"line {i}: target index {j!r} out of range 0..{self.n - 1}"
                )
            if p < 0:
                raise NegativeProbabilityError(
                    f"line {i}: negative probability {p} towards state {j}"
                )
            if p:
                line[j] = p
                total += p
        if abs(total - 1) > self.tol:
            raise LineSumError(
                f"line {i} sums to {total}, not 1 (tol={self.tol}); "
                "refusing to normalize silently"
            )
        return line

    @property
    def lines(self):
        """Canonical lines ``lines[source] = {target: probability}``."""
        return self._lines

    def line(self, i):
        return dict(self._lines[i])

    def probability(self, i, j):
        """Transition probability Pr(i -> j), convention-independent."""
        return self._lines[i].get(j, 0)

    def set_line(self, i, entries):
        """Replace the line of state ``i`` after full validation.

        Invalid input raises and leaves the matrix unchanged.
        """
        if not (isinstance(i, int) and 0 <= i < self.n):
            raise IndexOutOfRangeError(f"state index {i!r} out of range")
        self._lines[i] = self._checked_line(i, entries)

    def to_dense(self):
        dense = [[0] * self.n for _ in range(self.n)]
        for i, line in enumerate(self._lines):
            for j, p in line.items():
                dense[i][j] = p
        return dense

    def to_fractions(self):
        """Exact rational copy (each entry converted to ``Fraction``)."""
        return SparseTransitionMatrix(
            self.n,
            {
                i: {j: Fraction(p) for j, p in line.items()}
                for i, line in enumerate(self._lines)
            },
            convention=self.convention,
            tol=self.tol,
        )

    def __repr__(self):
        return (
            f"SparseTransitionMatrix(n={self.n}, "
            f"convention={self.convention.value!r})"
        )
