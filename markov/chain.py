"""High-level Markov chain analysis on a SparseTransitionMatrix."""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from fractions import Fraction

from .classify import classify
from .dense import lu_factorize, lu_solve
from .errors import DistributionError, NotConvergedError
from .solvers import LUSolver, assess_convergence, solve_additive_sparse
from .sparse import SparseTransitionMatrix


@dataclass
class PropagationRecord:
    """One step of distribution propagation."""

    step: int
    distribution: list
    mass_error: float   # sum(distribution) - 1; must stay near zero
    increment: float    # max |d_k - d_{k-1}|


@dataclass
class StationaryResult:
    """Outcome of iterative stationary-distribution search."""

    distribution: list
    converged: bool
    iterations: int
    residual_norm: float    # ||d P - d||_inf at the returned iterate
    increment_norm: float
    message: str


@dataclass
class AbsorptionResult:
    """Closed-class hitting probabilities and expected hitting steps.

    ``probabilities[state][class_index]`` is the probability that the chain
    started at ``state`` ends up in the closed class ``class_index`` (an
    index into ``closed_classes``).  ``expected_steps[state]`` is the
    expected number of steps until absorption into an absorbing *state*;
    it is ``math.inf`` whenever a non-absorbing closed class is reachable,
    because the chain may then never be absorbed.
    """

    method: str
    transient_states: tuple
    closed_classes: tuple
    probabilities: dict
    expected_steps: dict
    converged: bool
    diagnostics: dict = field(default_factory=dict)


class MarkovChain:
    """Validated sparse Markov chain with structural and numerical analysis."""

    def __init__(self, matrix, tol=1e-10):
        if not isinstance(matrix, SparseTransitionMatrix):
            raise TypeError("matrix must be a SparseTransitionMatrix")
        self.matrix = matrix
        self.tol = tol
        self._solver = None
        self._solver_transient = None
        self._reclassify()

    def _reclassify(self):
        self.structure = classify(self.matrix)

    # ------------------------------------------------------------------
    # diagnostics
    # ------------------------------------------------------------------

    def diagnostics(self):
        """Structural diagnostics, including irreducibility."""
        return self.structure.summary()

    @property
    def irreducible(self):
        return self.structure.irreducible

    # ------------------------------------------------------------------
    # distribution handling
    # ------------------------------------------------------------------

    def check_distribution(self, dist, tol=None):
        """Validate a distribution: non-negative and summing to 1 within tol."""
        tol = self.tol if tol is None else tol
        if len(dist) != self.matrix.n:
            raise DistributionError(
                f"distribution has length {len(dist)}, expected {self.matrix.n}"
            )
        total = 0.0
        for k, v in enumerate(dist):
            if v < -tol:
                raise DistributionError(
                    f"distribution has negative entry at index {k}: {v}"
                )
            total += v
        if abs(total - 1.0) > tol:
            raise DistributionError(
                f"distribution sums to {total}, not 1 (tol={tol})"
            )

    def apply(self, dist):
        """One transition step applied to a distribution."""
        n = self.matrix.n
        out = [0.0] * n
        lines = self.matrix.lines
        for i, di in enumerate(dist):
            if di == 0:
                continue
            for j, p in lines[i].items():
                out[j] += di * p
        return out

    def propagate(self, dist, steps):
        """Yield a :class:`PropagationRecord` per step.

        Each record carries the mass-conservation error
        ``sum(distribution) - 1`` so leaks (e.g. at absorbing states) would
        be visible immediately.
        """
        dist = [float(v) for v in dist]
        self.check_distribution(dist)
        for step in range(1, steps + 1):
            new = self.apply(dist)
            mass_error = sum(new) - 1.0
            increment = max(abs(a - b) for a, b in zip(new, dist))
            dist = new
            yield PropagationRecord(step, list(new), mass_error, increment)

    def stationary_iterative(self, dist0, tol=1e-12, max_iter=10000):
        """Power iteration towards a stationary distribution.

        Convergence is certified by the stationarity residual
        ``||d P - d||_inf`` only; a small change between iterates is never
        accepted as proof (see :func:`markov.solvers.assess_convergence`).
        """
        dist = [float(v) for v in dist0]
        self.check_distribution(dist)
        residual = float("inf")
        increment = float("inf")
        for iteration in range(1, max_iter + 1):
            new = self.apply(dist)
            increment = max(abs(a - b) for a, b in zip(new, dist))
            nxt = self.apply(new)
            residual = max(abs(a - b) for a, b in zip(nxt, new))
            dist = new
            if residual <= tol:
                return StationaryResult(
                    dist, True, iteration, residual, increment,
                    "converged: stationarity residual within tolerance",
                )
        _, warning = assess_convergence(residual, increment, tol)
        message = f"maximum iterations ({max_iter}) reached"
        if warning:
            message += "; " + warning
        return StationaryResult(dist, False, max_iter, residual, increment, message)

    # ------------------------------------------------------------------
    # row revision
    # ------------------------------------------------------------------

    def revise_line(self, i, entries):
        """Revise the transition line of state ``i``.

        The new line is fully validated (non-negative entries, sums to one);
        invalid input raises and leaves the chain unchanged.  If a dense
        factorization of ``I - Q`` is cached and the transient set is
        unchanged, the revision is pushed into the cached solver, which then
        reuses its factorization via iterative refinement on the next solve.
        """
        old_transient = self._solver_transient
        self.matrix.set_line(i, entries)  # validates first
        self._reclassify()
        if self._solver is None:
            return
        transient = self.structure.transient_states
        if (
            old_transient is None
            or transient != old_transient
            or i not in old_transient
        ):
            self._solver = None
            self._solver_transient = None
            return
        pos = {s: k for k, s in enumerate(old_transient)}
        row = [0.0] * len(old_transient)
        row[pos[i]] = 1.0
        for j, p in self.matrix.lines[i].items():
            k = pos.get(j)
            if k is not None:
                row[k] -= p
        self._solver.update_row(pos[i], row)

    # ------------------------------------------------------------------
    # absorption analysis
    # ------------------------------------------------------------------

    def absorption(self, method="auto", tol=None, max_iter=100000, omega=1.0):
        """Closed-class hitting probabilities and expected absorption steps.

        method: "dense" (self-written LU), "iterative" (sparse SOR with true
        residual certification), "exact" (rational Fraction LU), or "auto".
        """
        tol = self.tol if tol is None else tol
        structure = self.structure
        n = self.matrix.n
        closed = list(structure.closed_classes)
        probabilities = {}
        expected = {}
        for cc in closed:
            for s in cc.states:
                probabilities[s] = {cc.index: 1}
                expected[s] = 0 if cc.is_absorbing_state else math.inf
        transient = list(structure.transient_states)
        diagnostics = {
            "non_absorbing_closed_classes": [
                list(cc.states) for cc in closed if not cc.is_absorbing_state
            ]
        }
        if transient:
            bad = set()
            for cc in closed:
                if not cc.is_absorbing_state:
                    bad.update(cc.states)
            bad_reaching = self._states_reaching(bad) if bad else set()
            if method == "auto":
                method = "dense" if len(transient) <= 256 else "iterative"
            if method == "exact":
                probs, exp_map, diag = self._absorption_exact(
                    transient, closed, bad_reaching
                )
            elif method == "dense":
                probs, exp_map, diag = self._absorption_dense(
                    transient, closed, bad_reaching
                )
            elif method == "iterative":
                probs, exp_map, diag = self._absorption_iterative(
                    transient, closed, bad_reaching, tol, max_iter, omega
                )
            else:
                raise ValueError(f"unknown method {method!r}")
            diagnostics.update(diag)
            for s in transient:
                probabilities[s] = probs.get(s, {})
                expected[s] = exp_map[s]
        return AbsorptionResult(
            method=method,
            transient_states=tuple(transient),
            closed_classes=tuple(closed),
            probabilities=probabilities,
            expected_steps=expected,
            converged=True,
            diagnostics=diagnostics,
        )

    # -- helpers --------------------------------------------------------

    def _states_reaching(self, targets):
        """All states that can reach any state of ``targets`` (positive prob)."""
        reverse = [[] for _ in range(self.matrix.n)]
        for i, line in enumerate(self.matrix.lines):
            for j in line:
                reverse[j].append(i)
        seen = set(targets)
        stack = list(targets)
        while stack:
            s = stack.pop()
            for pre in reverse[s]:
                if pre not in seen:
                    seen.add(pre)
                    stack.append(pre)
        return seen

    def _q_rows(self, states):
        """Rows of Q (transient-to-transient block) for the given states."""
        pos = {s: k for k, s in enumerate(states)}
        q_rows = []
        for i in states:
            row = {}
            for j, p in self.matrix.lines[i].items():
                k = pos.get(j)
                if k is not None:
                    row[k] = p
            q_rows.append(row)
        return pos, q_rows

    def _class_rhs(self, states, cc):
        """One-step probabilities of entering closed class ``cc``."""
        members = set(cc.states)
        return [
            sum(p for j, p in self.matrix.lines[i].items() if j in members)
            for i in states
        ]

    @staticmethod
    def _dense_system(q_rows, m, one):
        return [
            [(one if r == c else 0) - q_rows[r].get(c, 0) for c in range(m)]
            for r in range(m)
        ]

    def _dense_solver(self, transient, a):
        if self._solver is not None and self._solver_transient == tuple(transient):
            return self._solver  # revisions already pushed via revise_line
        self._solver = LUSolver(a)
        self._solver_transient = tuple(transient)
        return self._solver

    def _absorption_dense(self, transient, closed, bad_reaching):
        m = len(transient)
        _, q_rows = self._q_rows(transient)
        a = self._dense_system(q_rows, m, 1.0)
        solver = self._dense_solver(transient, a)
        diagnostics = {"solver": "dense LU", "reused_factorization": []}
        probs = {}
        for cc in closed:
            x = solver.solve(self._class_rhs(transient, cc))
            diagnostics["reused_factorization"].append(solver.last_solve_reused)
            for s, xs in zip(transient, x):
                probs.setdefault(s, {})[cc.index] = xs
        expected = self._expected_solve(
            transient, closed, bad_reaching, q_rows, solver, diagnostics
        )
        return probs, expected, diagnostics

    def _expected_solve(self, transient, closed, bad_reaching, q_rows, solver,
                        diagnostics):
        expected = {}
        good = [s for s in transient if s not in bad_reaching]
        for s in transient:
            if s in bad_reaching:
                expected[s] = math.inf
        if not good:
            return expected
        if len(good) == len(transient):
            x = solver.solve([1.0] * len(transient))
            diagnostics["expected_steps_solver"] = "dense LU (shared factorization)"
            diagnostics["reused_factorization"].append(solver.last_solve_reused)
            for s, xs in zip(transient, x):
                expected[s] = xs
            return expected
        _, sub_q = self._q_rows(good)
        a = self._dense_system(sub_q, len(good), 1.0)
        lu, perm = lu_factorize(a)
        x = lu_solve(lu, perm, [1.0] * len(good))
        diagnostics["expected_steps_solver"] = (
            f"dense LU on {len(good)} states unable to reach a "
            "non-absorbing closed class"
        )
        for s, xs in zip(good, x):
            expected[s] = xs
        return expected

    def _absorption_iterative(self, transient, closed, bad_reaching, tol,
                              max_iter, omega):
        _, q_rows = self._q_rows(transient)
        diagnostics = {"solver": f"sparse SOR (omega={omega})", "rhs": {}}
        probs = {}
        for cc in closed:
            result = solve_additive_sparse(
                q_rows, self._class_rhs(transient, cc),
                tol=tol, max_iter=max_iter, omega=omega,
            )
            diagnostics["rhs"][f"class_{cc.index}"] = {
                "iterations": result.iterations,
                "residual_norm": result.residual_norm,
                "increment_norm": result.increment_norm,
                "converged": result.converged,
            }
            if not result.converged:
                raise NotConvergedError(
                    f"absorption probabilities for closed class {cc.index} did "
                    f"not converge: residual={result.residual_norm:.3e} "
                    f"(tol={tol:.1e}); a small increment is not accepted as "
                    "proof of convergence"
                )
            for s, xs in zip(transient, result.x):
                probs.setdefault(s, {})[cc.index] = xs
        expected = {}
        good = [s for s in transient if s not in bad_reaching]
        for s in transient:
            if s in bad_reaching:
                expected[s] = math.inf
        if good:
            _, sub_q = self._q_rows(good)
            result = solve_additive_sparse(
                sub_q, [1.0] * len(good), tol=tol, max_iter=max_iter,
                omega=omega,
            )
            diagnostics["rhs"]["expected_steps"] = {
                "iterations": result.iterations,
                "residual_norm": result.residual_norm,
                "increment_norm": result.increment_norm,
                "converged": result.converged,
            }
            if not result.converged:
                raise NotConvergedError(
                    "expected-steps solve did not converge: "
                    f"residual={result.residual_norm:.3e} (tol={tol:.1e})"
                )
            for s, xs in zip(good, result.x):
                expected[s] = xs
        return probs, expected, diagnostics

    def _absorption_exact(self, transient, closed, bad_reaching):
        flines = self.matrix.to_fractions().lines
        pos = {s: k for k, s in enumerate(transient)}
        m = len(transient)
        a = []
        for r, i in enumerate(transient):
            row = [Fraction(1) if r == c else Fraction(0) for c in range(m)]
            for j, p in flines[i].items():
                c = pos.get(j)
                if c is not None:
                    row[c] -= p
            a.append(row)
        lu, perm = lu_factorize(a)
        probs = {}
        for cc in closed:
            members = set(cc.states)
            b = [
                sum((p for j, p in flines[i].items() if j in members),
                    Fraction(0))
                for i in transient
            ]
            x = lu_solve(lu, perm, b)
            for s, xs in zip(transient, x):
                probs.setdefault(s, {})[cc.index] = xs
        expected = {}
        good = [s for s in transient if s not in bad_reaching]
        for s in transient:
            if s in bad_reaching:
                expected[s] = math.inf
        if good:
            gpos = {s: k for k, s in enumerate(good)}
            g = []
            for r, i in enumerate(good):
                row = [Fraction(1) if r == c else Fraction(0)
                       for c in range(len(good))]
                for j, p in flines[i].items():
                    c = gpos.get(j)
                    if c is not None:
                        row[c] -= p
                g.append(row)
            glu, gperm = lu_factorize(g)
            x = lu_solve(glu, gperm, [Fraction(1)] * len(good))
            for s, xs in zip(good, x):
                expected[s] = xs
        return probs, expected, {"solver": "exact rational LU"}
