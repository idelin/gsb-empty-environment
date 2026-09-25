"""Sparse transition matrix with explicit size, indices and orientation.

No NumPy/SciPy: entries live in a plain dict keyed by (row, column).
Validation never normalizes silently and never auto-fills missing lines.
"""

from fractions import Fraction


class ValidationError(ValueError):
    """Raised when a transition matrix or a distribution is invalid."""


class SparseTransitionMatrix:
    """Sparse Markov transition matrix.

    convention="row":    stored[i][j] = P(i -> j); every *row* must sum to 1.
    convention="column": stored[i][j] = P(j -> i); every *column* must sum to 1.

    Entries may be int, float or fractions.Fraction.  Pass Fraction values
    (e.g. Fraction(1, 3)) to build exact rational reference models.
    """

    def __init__(self, size, convention="row"):
        if not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise ValidationError("size must be a positive integer")
        if convention not in ("row", "column"):
            raise ValidationError("convention must be 'row' or 'column'")
        self.size = size
        self.convention = convention
        self._entries = {}

    def _check_index(self, index):
        ok = isinstance(index, int) and not isinstance(index, bool)
        if not ok or not 0 <= index < self.size:
            raise ValidationError(
                "index %r out of range for size %d" % (index, self.size))

    def set(self, i, j, value):
        """Set stored entry (i, j); its meaning depends on the convention."""
        self._check_index(i)
        self._check_index(j)
        number = value if isinstance(value, Fraction) else float(value)
        if number != number:  # NaN
            raise ValidationError("NaN probability at (%d, %d)" % (i, j))
        if number < 0:
            raise ValidationError(
                "negative probability at (%d, %d): %r" % (i, j, number))
        if number > 1:
            raise ValidationError(
                "probability above 1 at (%d, %d): %r" % (i, j, number))
        if number == 0:
            self._entries.pop((i, j), None)
        else:
            self._entries[(i, j)] = number

    @classmethod
    def from_rows(cls, size, rows, convention="row"):
        """Build from an explicit {i: {j: p}} mapping of stored entries."""
        matrix = cls(size, convention)
        for i, row in rows.items():
            for j, p in row.items():
                matrix.set(i, j, p)
        return matrix

    def entries(self):
        return dict(self._entries)

    def line_present(self, index):
        """True if the row (or column, per convention) has any entry."""
        self._check_index(index)
        if self.convention == "row":
            return any(i == index for (i, _) in self._entries)
        return any(j == index for (_, j) in self._entries)

    def line_sum(self, index):
        """Sum of the row (row convention) or column (column convention)."""
        self._check_index(index)
        total = 0
        if self.convention == "row":
            for (i, _), p in self._entries.items():
                if i == index:
                    total += p
        else:
            for (_, j), p in self._entries.items():
                if j == index:
                    total += p
        return total

    def validate(self, tol=1e-9):
        """Check every line sums to 1 within tol.

        A missing probability line is an error, not a zero-fill; a line
        whose sum deviates from 1 is an error, never silently normalized.
        """
        errors = []
        for index in range(self.size):
            if not self.line_present(index):
                errors.append("missing probability line %d" % index)
                continue
            total = self.line_sum(index)
            if abs(total - 1) > tol:
                errors.append(
                    "line %d sums to %r, not 1 (tol=%g); "
                    "refusing to normalize silently" % (index, total, tol))
        if errors:
            raise ValidationError("; ".join(errors))
        return True

    def outgoing(self):
        """Canonical adjacency: out[i] = {j: P(i -> j)} for either convention."""
        out = [dict() for _ in range(self.size)]
        if self.convention == "row":
            for (i, j), p in self._entries.items():
                out[i][j] = p
        else:
            for (i, j), p in self._entries.items():
                out[j][i] = p
        return out

    def to_dense(self):
        """Dense stored matrix (self-written, for small-scale verification)."""
        dense = [[0.0] * self.size for _ in range(self.size)]
        for (i, j), p in self._entries.items():
            dense[i][j] = float(p)
        return dense
