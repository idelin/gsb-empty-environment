"""Finite Markov chain with exact rational (Fraction) transition probabilities."""

from fractions import Fraction


class Chain:
    """A finite-state Markov chain whose transition matrix holds Fractions.

    States are integers 0..n-1.  The matrix is validated on construction:
    every entry must be a non-negative rational and every row must sum to 1.
    """

    def __init__(self, matrix):
        if not matrix:
            raise ValueError("transition matrix must be non-empty")
        n = len(matrix)
        rows = []
        for i, row in enumerate(matrix):
            if len(row) != n:
                raise ValueError(f"row {i} has length {len(row)}, expected {n}")
            frac_row = [self._as_fraction(x, i, j) for j, x in enumerate(row)]
            for j, p in enumerate(frac_row):
                if p < 0:
                    raise ValueError(f"P[{i}][{j}] = {p} is negative")
            if sum(frac_row, Fraction(0)) != 1:
                raise ValueError(f"row {i} sums to {sum(frac_row, Fraction(0))}, not 1")
            rows.append(frac_row)
        self.n = n
        self.matrix = rows

    @staticmethod
    def _as_fraction(x, i, j):
        if isinstance(x, Fraction):
            return x
        if isinstance(x, int):
            return Fraction(x)
        if isinstance(x, tuple) and len(x) == 2:
            return Fraction(x[0], x[1])
        raise TypeError(f"P[{i}][{j}] = {x!r} is not an int, Fraction or (num, den) pair")

    def successors(self, i):
        """States reachable from i in one step (positive probability)."""
        return [j for j in range(self.n) if self.matrix[i][j] > 0]

    def reachable(self, sources):
        """Set of states reachable from any state in `sources` (inclusive)."""
        seen = set(sources)
        stack = list(sources)
        while stack:
            i = stack.pop()
            for j in self.successors(i):
                if j not in seen:
                    seen.add(j)
                    stack.append(j)
        return seen

    def communication_classes(self):
        """Partition of all states into communication (SCC) classes.

        Returns a list of frozensets.  Two states are in the same class iff
        each can reach the other.
        """
        reach = {i: self.reachable({i}) for i in range(self.n)}
        classes = []
        assigned = set()
        for i in range(self.n):
            if i in assigned:
                continue
            cls = frozenset(j for j in range(self.n)
                            if j in reach[i] and i in reach[j])
            classes.append(cls)
            assigned |= cls
        return classes

    def closed_classes(self):
        """Communication classes with no outgoing edge (once entered, never left)."""
        result = []
        for cls in self.communication_classes():
            if all(self.matrix[i][j] == 0
                   for i in cls for j in range(self.n) if j not in cls):
                result.append(cls)
        return result

    def absorbing_states(self):
        """States i with P[i][i] == 1 (self-loop probability 1)."""
        return [i for i in range(self.n) if self.matrix[i][i] == 1]

    def transient_states(self):
        """States not belonging to any closed class."""
        closed = set().union(*self.closed_classes()) if self.closed_classes() else set()
        return [i for i in range(self.n) if i not in closed]
