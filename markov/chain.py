"""Markov chain analysis on validated sparse transition matrices.

Closed-class identification (Tarjan SCC), absorption probabilities and
expected absorption steps (exact rational reference, LU, or iterative
solvers with residuals), row revision with factorization reuse, and
distribution propagation with per-step mass conservation error.
"""

import math
from dataclasses import dataclass, field
from fractions import Fraction

from .dense import LUSolver, exact_solve
from .iterative import gauss_seidel, multiply_distribution, power_iteration
from .sparse import SparseTransitionMatrix, ValidationError


@dataclass
class AbsorptionReport:
    method: str
    closed_classes: list
    absorbing: list          # per closed class: True iff singleton (absorbing)
    transient: list
    probabilities: dict      # state -> {class_index: probability}
    expected_steps: dict     # state -> number of steps, or math.inf
    systems: dict = field(default_factory=dict)   # iterative IterativeResults
    warnings: list = field(default_factory=list)


def _tarjan_scc(out):
    """Iterative Tarjan; returns the list of strongly connected components."""
    n = len(out)
    index_of = [None] * n
    low = [0] * n
    on_stack = [False] * n
    stack = []
    sccs = []
    counter = 0
    for root in range(n):
        if index_of[root] is not None:
            continue
        index_of[root] = low[root] = counter
        counter += 1
        stack.append(root)
        on_stack[root] = True
        work = [(root, iter(sorted(out[root])))]
        while work:
            node, it = work[-1]
            descended = False
            for nxt in it:
                if index_of[nxt] is None:
                    index_of[nxt] = low[nxt] = counter
                    counter += 1
                    stack.append(nxt)
                    on_stack[nxt] = True
                    work.append((nxt, iter(sorted(out[nxt]))))
                    descended = True
                    break
                elif on_stack[nxt]:
                    low[node] = min(low[node], index_of[nxt])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                low[parent] = min(low[parent], low[node])
            if low[node] == index_of[node]:
                comp = []
                while True:
                    w = stack.pop()
                    on_stack[w] = False
                    comp.append(w)
                    if w == node:
                        break
                sccs.append(comp)
    return sccs


class MarkovChain:
    """A validated sparse Markov chain (row- or column-stochastic input)."""

    def __init__(self, matrix, tol=1e-9):
        matrix.validate(tol)
        self.matrix = matrix
        self.size = matrix.size
        self.out = matrix.outgoing()
        self.tol = tol

    @classmethod
    def from_rows(cls, size, rows, convention="row", tol=1e-9):
        matrix = SparseTransitionMatrix.from_rows(size, rows, convention)
        return cls(matrix, tol)

    # -- distributions ---------------------------------------------------

    def check_distribution(self, dist, tol=1e-9):
        """Validate a distribution; never silently renormalize it."""
        if len(dist) != self.size:
            raise ValidationError(
                "distribution has length %d, expected %d"
                % (len(dist), self.size))
        for v in dist:
            if not (v >= 0):
                raise ValidationError(
                    "distribution has a negative entry: %r" % (v,))
        total = sum(dist)
        if abs(total - 1) > tol:
            raise ValidationError(
                "distribution sums to %r, not 1 (tol=%g); "
                "refusing to normalize silently" % (total, tol))
        return list(dist)

    def propagate(self, dist, steps, tol=1e-6):
        """Yield (step, distribution, mass_error) for each of `steps` steps.

        Mass conservation is checked every step: with a validated
        stochastic matrix the error stays at round-off level, and any
        drift beyond `tol` raises instead of being absorbed silently.
        """
        current = self.check_distribution(dist)
        for step in range(1, steps + 1):
            new = multiply_distribution(self.out, current)
            mass_error = sum(new) - sum(current)
            if abs(sum(new) - 1) > tol:
                raise ValidationError(
                    "mass conservation violated at step %d: sum=%r"
                    % (step, sum(new)))
            yield step, new, mass_error
            current = new

    def stationary(self, dist0=None, tol=1e-12, max_iter=100000):
        """Power iteration toward a stationary distribution (residual-based)."""
        if dist0 is None:
            dist0 = [1.0 / self.size] * self.size
        else:
            dist0 = self.check_distribution(dist0)
        return power_iteration(self.out, dist0, tol, max_iter)

    # -- structure ---------------------------------------------------------

    def closed_classes(self):
        """SCCs with no outgoing edge: the closed communication classes."""
        classes = []
        for comp in _tarjan_scc(self.out):
            member = set(comp)
            if all(j in member for i in comp for j in self.out[i]):
                classes.append(sorted(comp))
        classes.sort()
        return classes

    def transient_states(self):
        in_closed = set()
        for comp in self.closed_classes():
            in_closed.update(comp)
        return [s for s in range(self.size) if s not in in_closed]

    def is_irreducible(self):
        """True iff the whole chain is one single closed class."""
        classes = self.closed_classes()
        return len(classes) == 1 and len(classes[0]) == self.size

    # -- revision ------------------------------------------------------------

    def revised(self, row_updates, tol=None):
        """Return a new chain with the given outgoing rows replaced.

        row_updates: {state: {dst: prob}}.  The full matrix is
        re-validated: a revised row whose sum deviates from 1 raises
        ValidationError instead of being silently normalized.
        """
        out = [dict(r) for r in self.out]
        for i, new_row in row_updates.items():
            if not isinstance(i, int) or not 0 <= i < self.size:
                raise ValidationError("row index %r out of range" % (i,))
            out[i] = dict(new_row)
        matrix = SparseTransitionMatrix(self.size, "row")
        for i, row in enumerate(out):
            for j, p in row.items():
                matrix.set(i, j, p)
        return MarkovChain(matrix, self.tol if tol is None else tol)

    def transient_solver(self, base=None):
        """Dense (I - Q) solver over transient states; see TransientSolver."""
        return TransientSolver(self, base=base)

    # -- absorption analysis -------------------------------------------------

    def absorption_analysis(self, method="exact", tol=1e-10, max_iter=100000,
                            solver=None, prob_tol=1e-9):
        """Absorption probabilities and expected steps until absorption.

        method="exact":     exact fractions.Fraction solve (small models).
        method="iterative": Gauss-Seidel with residuals and error bounds.
        method="lu":        dense LU, optionally a reused TransientSolver.

        States that can reach a non-absorbing closed class with positive
        probability get that probability reported and expected steps inf.
        """
        classes = self.closed_classes()
        class_of = {}
        for ci, comp in enumerate(classes):
            for s in comp:
                class_of[s] = ci
        transient = [s for s in range(self.size) if s not in class_of]
        pos = {s: k for k, s in enumerate(transient)}
        absorbing = [len(comp) == 1 for comp in classes]
        n = len(transient)
        exact = (method == "exact")
        one = Fraction(1) if exact else 1.0
        zero = Fraction(0) if exact else 0.0

        probabilities = {}
        expected = {}
        for ci, comp in enumerate(classes):
            for s in comp:
                probabilities[s] = {ci: one}
                expected[s] = zero if absorbing[ci] else math.inf

        systems = {}
        warnings = []
        if n:
            a_frac = [[Fraction(1 if r == c else 0) for c in range(n)]
                      for r in range(n)]
            for r, s in enumerate(transient):
                for j, p in self.out[s].items():
                    if j in pos:
                        a_frac[r][pos[j]] -= Fraction(p)
            rhs = {}
            for ci, comp in enumerate(classes):
                b = []
                for s in transient:
                    total = sum((Fraction(self.out[s][j]) for j in comp
                                 if j in self.out[s]), Fraction(0))
                    b.append(total)
                rhs["absorb_into_%d" % ci] = b
            rhs["expected_steps"] = [Fraction(1)] * n

            a_float = [[float(v) for v in row] for row in a_frac]
            solutions = {}
            for label, b_frac in rhs.items():
                if exact:
                    solutions[label] = exact_solve(a_frac, b_frac)
                elif method == "iterative":
                    result = gauss_seidel(
                        a_float, [float(v) for v in b_frac],
                        tol=tol, max_iter=max_iter)
                    systems[label] = result
                    warnings.extend(result.warnings)
                    if not result.converged:
                        warnings.append(
                            "system %s did not converge: residual %.3e "
                            "after %d iterations"
                            % (label, result.residuals[-1], result.iterations))
                    solutions[label] = result.x
                elif method == "lu":
                    lu = solver if solver is not None else TransientSolver(self)
                    solutions[label] = lu.solve([float(v) for v in b_frac])
                else:
                    raise ValueError("unknown method %r" % (method,))

            for r, s in enumerate(transient):
                probabilities[s] = {
                    ci: solutions["absorb_into_%d" % ci][r]
                    for ci in range(len(classes))}
            for r, s in enumerate(transient):
                reaches_open = any(
                    not absorbing[ci] and probabilities[s][ci] > prob_tol
                    for ci in range(len(classes)))
                expected[s] = (math.inf if reaches_open
                               else solutions["expected_steps"][r])

        return AbsorptionReport(method, classes, absorbing, transient,
                                probabilities, expected, systems, warnings)


class TransientSolver:
    """Dense solver for (I - Q) x = b over the transient states.

    When built with base=<TransientSolver of an earlier revision> and
    the transient set is unchanged, the base LU factorization is reused
    through a Woodbury rank-k update instead of re-factoring.
    """

    def __init__(self, chain, base=None):
        self.chain = chain
        self.transient = chain.transient_states()
        pos = {s: k for k, s in enumerate(self.transient)}
        n = len(self.transient)
        a = [[0.0] * n for _ in range(n)]
        for r, s in enumerate(self.transient):
            a[r][r] = 1.0
            for j, p in chain.out[s].items():
                if j in pos:
                    a[r][pos[j]] -= float(p)
        self._a = a
        self._revisions = None
        if base is not None and base.transient == self.transient:
            self._lu = base._lu
            self._revisions = {
                r: list(a[r]) for r in range(n) if a[r] != self._lu.matrix[r]}
        else:
            self._lu = LUSolver(a)

    @property
    def reuses_factorization(self):
        return self._revisions is not None

    def solve(self, b):
        if len(b) != len(self.transient):
            raise ValueError("right-hand side has wrong length")
        if self._revisions is None:
            return self._lu.solve(b)
        return self._lu.solve_with_row_revisions(self._revisions, b)
