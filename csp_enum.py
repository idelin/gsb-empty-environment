"""Finite-domain CSP solution enumerator (pure standard library, Python 3.11+).

Features
--------
- Enumerate all solutions or the first N solutions of a CSP.
- Projection dedup: only distinct projections onto a chosen set of output
  variables are emitted.  Solutions differing only in auxiliary variables
  yield a single output.  An empty projection yields at most one output.
- Blocking clauses: after a projection is emitted, a blocking clause is
  added that excludes exactly that emitted projection -- nothing else.
- Pause / resume: the search uses an explicit stack (no recursion, no
  generators), so a step budget can interrupt the search at any point and
  a later call resumes exactly where it stopped, without duplicate output.
  The state can also be exported to a plain (JSON-able) dict and restored.
- Independent checker: every claimed solution is verified against the
  original constraints by :func:`verify_solution`, a code path that does
  not share logic with the solver's propagation.
- No SAT/SMT libraries; plain backtracking over finite domains.
"""

from __future__ import annotations

from dataclasses import dataclass

# Search statuses returned by SolutionEnumerator.search
EXHAUSTED = "exhausted"  # search space fully explored; nothing more to find
BUDGET = "budget"        # step budget spent; call search() again to resume
LIMIT = "limit"          # max_solutions reached; call search() again to resume

_UNSET = object()


class SolverIntegrityError(RuntimeError):
    """Raised when the solver's own claim fails the independent checker."""


# ---------------------------------------------------------------------------
# Constraints
# ---------------------------------------------------------------------------

class Constraint:
    """A constraint over a fixed scope of variables.

    ``func`` receives a dict mapping every variable of ``scope`` to its
    value and must return True iff the (complete) constraint is satisfied.

    ``partial_func`` (optional) receives a dict with the *assigned* subset
    of the scope and must return False iff that partial assignment already
    violates the constraint (any other return value means "undecided").
    It only affects pruning power, never correctness.
    """

    def __init__(self, scope, func, partial_func=None, name=None):
        self.scope = tuple(scope)
        if not self.scope:
            raise ValueError("constraint scope must be non-empty")
        self.func = func
        self.partial_func = partial_func
        self.name = name or getattr(func, "__name__", "constraint")

    def check_partial(self, assignment):
        """Solver path: False = violated, True = satisfied, None = undecided."""
        if all(v in assignment for v in self.scope):
            return bool(self.func({v: assignment[v] for v in self.scope}))
        if self.partial_func is not None:
            sub = {v: assignment[v] for v in self.scope if v in assignment}
            if sub and self.partial_func(sub) is False:
                return False
        return None

    def check_full(self, assignment):
        """Independent-checker path: evaluate on a complete assignment."""
        return bool(self.func({v: assignment[v] for v in self.scope}))

    def __repr__(self):
        return f"Constraint({self.name}, scope={self.scope!r})"


def constraint(scope, func, name=None):
    """Build a constraint from a full-assignment predicate."""
    return Constraint(scope, func, name=name)


def all_different(*variables, name=None):
    """All variables in the scope must take pairwise distinct values."""
    scope = tuple(variables)

    def full(vals):
        xs = [vals[v] for v in scope]
        return len(set(xs)) == len(xs)

    def partial(sub):
        xs = list(sub.values())
        return len(set(xs)) == len(xs)

    return Constraint(scope, full, partial_func=partial,
                      name=name or "all_different")


def table(scope, allowed, name=None):
    """The scope's tuple of values must be one of the ``allowed`` rows."""
    scope = tuple(scope)
    allowed_set = {tuple(row) for row in allowed}
    for row in allowed_set:
        if len(row) != len(scope):
            raise ValueError("table row length does not match scope")

    def full(vals):
        return tuple(vals[v] for v in scope) in allowed_set

    def partial(sub):
        for row in allowed_set:
            if all(v not in sub or sub[v] == row[i]
                   for i, v in enumerate(scope)):
                return True
        return False

    return Constraint(scope, full, partial_func=partial, name=name or "table")


# ---------------------------------------------------------------------------
# Problem and independent checker
# ---------------------------------------------------------------------------

class Problem:
    """A finite-domain CSP: variables with finite domains + constraints."""

    def __init__(self):
        self.domains = {}        # name -> tuple of allowed values
        self.constraints = []    # list of Constraint

    def add_variable(self, name, domain):
        domain = tuple(domain)
        if not domain:
            raise ValueError(f"empty domain for variable {name!r}")
        if name in self.domains:
            raise ValueError(f"duplicate variable {name!r}")
        self.domains[name] = domain
        return self

    def add_constraint(self, con):
        for v in con.scope:
            if v not in self.domains:
                raise ValueError(f"constraint on unknown variable {v!r}")
        self.constraints.append(con)
        return self


def verify_solution(problem, assignment):
    """Independent checker: validate a claimed solution against the
    *original* constraints of the problem.

    This shares no logic with the solver's propagation: it requires a
    complete assignment, checks every value against its declared domain,
    and evaluates every original constraint directly.
    """
    if not isinstance(assignment, dict):
        return False
    if set(assignment) != set(problem.domains):
        return False
    for var, val in assignment.items():
        if val not in problem.domains[var]:
            return False
    for con in problem.constraints:
        if not con.check_full(assignment):
            return False
    return True


# ---------------------------------------------------------------------------
# Resumable enumerator
# ---------------------------------------------------------------------------

@dataclass
class SearchResult:
    solutions: list   # projections (dicts) emitted during this call
    status: str       # EXHAUSTED | BUDGET | LIMIT
    steps: int        # search steps consumed by this call


class _Frame:
    """One level of the explicit search stack."""
    __slots__ = ("var", "values", "pos")

    def __init__(self, var, values):
        self.var = var
        self.values = values
        self.pos = 0


class SolutionEnumerator:
    """Resumable solution enumerator with projection dedup.

    ``projection`` lists the output variables; only distinct projections
    are emitted.  ``projection=()`` means the empty projection: at most
    one (empty) solution is emitted, answering pure satisfiability.
    ``projection=None`` (default) projects onto all variables, i.e.
    enumerates complete assignments.
    """

    def __init__(self, problem, projection=None, verify=True):
        self.problem = problem
        if projection is None:
            projection = tuple(problem.domains)
        self.projection = tuple(projection)
        for v in self.projection:
            if v not in problem.domains:
                raise ValueError(f"projection of unknown variable {v!r}")
        self.verify = verify
        # Branch on projection variables first so blocking clauses prune
        # as early as possible.
        proj_set = set(self.projection)
        self._order = ([v for v in problem.domains if v in proj_set] +
                       [v for v in problem.domains if v not in proj_set])
        self._blocking = []   # blocking clauses: projections already emitted
        self._emitted = []    # projection tuples emitted so far
        self._stack = []
        self._assignment = {}
        self._done = False
        self._pending_backtrack = False
        self._trivial_pending = not self._order
        self.total_steps = 0
        if self._order:
            first = self._order[0]
            self._stack.append(_Frame(first, list(problem.domains[first])))

    # -- internal search ----------------------------------------------------

    def _consistent(self):
        a = self._assignment
        for con in self.problem.constraints:
            if con.check_partial(a) is False:
                return False
        for blocked in self._blocking:
            if all(a.get(v, _UNSET) == val for v, val in blocked.items()):
                return False
        return True

    def _step_search(self, budget):
        """Advance until the next solution / budget end / exhaustion.

        Returns (event, payload, steps_used) with event in
        {"solution", "suspend", "done"}.
        """
        if self._done:
            return ("done", None, 0)
        if self._trivial_pending:
            # Degenerate problem without variables: the empty assignment is
            # the unique candidate solution.
            self._trivial_pending = False
            self._done = True
            return ("solution", {}, 0)
        if self._pending_backtrack:
            self._pending_backtrack = False
            self._assignment.pop(self._order[-1], None)
        steps = 0
        while True:
            frame = self._stack[-1]
            if frame.pos >= len(frame.values):
                self._stack.pop()
                self._assignment.pop(frame.var, None)
                if not self._stack:
                    self._done = True
                    return ("done", None, steps)
                continue
            if budget is not None and steps >= budget:
                return ("suspend", None, steps)
            value = frame.values[frame.pos]
            frame.pos += 1
            steps += 1
            self._assignment[frame.var] = value
            if not self._consistent():
                del self._assignment[frame.var]
                continue
            if len(self._stack) == len(self._order):
                # Complete consistent assignment found.  The stack and the
                # pending-backtrack flag let us resume right after it.
                self._pending_backtrack = True
                return ("solution", dict(self._assignment), steps)
            nxt = self._order[len(self._stack)]
            self._stack.append(_Frame(nxt, list(self.problem.domains[nxt])))

    # -- public API ---------------------------------------------------------

    @property
    def done(self):
        """True once the search space is fully explored."""
        return self._done

    @property
    def emitted(self):
        """Projection tuples emitted so far (in emission order)."""
        return list(self._emitted)

    def search(self, budget=None, max_solutions=None):
        """Run the search; stops on budget, solution limit, or exhaustion.

        State is kept between calls, so calling again resumes exactly
        where the previous call stopped, without duplicate output.
        """
        solutions = []
        steps_used = 0
        while True:
            if max_solutions is not None and len(solutions) >= max_solutions:
                return SearchResult(solutions, LIMIT, steps_used)
            remaining = None if budget is None else budget - steps_used
            if remaining is not None and remaining <= 0:
                return SearchResult(solutions, BUDGET, steps_used)
            event, payload, used = self._step_search(remaining)
            steps_used += used
            self.total_steps += used
            if event == "solution":
                if self.verify and not verify_solution(self.problem, payload):
                    raise SolverIntegrityError(
                        f"solver emitted assignment that fails the "
                        f"independent checker: {payload!r}")
                proj = {v: payload[v] for v in self.projection}
                key = tuple(proj[v] for v in self.projection)
                if key in self._emitted:
                    raise SolverIntegrityError(
                        f"duplicate projection emitted: {proj!r}")
                # Blocking clause: exclude exactly this emitted projection.
                self._blocking.append(proj)
                self._emitted.append(key)
                solutions.append(proj)
            elif event == "suspend":
                return SearchResult(solutions, BUDGET, steps_used)
            else:  # "done"
                return SearchResult(solutions, EXHAUSTED, steps_used)

    def enumerate_all(self):
        """Enumerate all remaining solutions (unlimited budget)."""
        out = []
        while True:
            res = self.search()
            out.extend(res.solutions)
            if res.status == EXHAUSTED:
                return out

    def first(self, n):
        """Return up to the next ``n`` solutions."""
        return self.search(max_solutions=n).solutions

    # -- serialisable pause / resume ----------------------------------------

    def export_state(self):
        """Export the resumable search state as a plain dict (JSON-able
        whenever the domain values are)."""
        return {
            "projection": list(self.projection),
            "blocking": [dict(b) for b in self._blocking],
            "emitted": [list(k) for k in self._emitted],
            "stack": [(f.var, f.pos) for f in self._stack],
            "assignment": dict(self._assignment),
            "done": self._done,
            "pending_backtrack": self._pending_backtrack,
            "total_steps": self.total_steps,
        }

    @classmethod
    def restore(cls, problem, state, verify=True):
        """Rebuild an enumerator from :meth:`export_state` output."""
        self = cls.__new__(cls)
        self.problem = problem
        self.projection = tuple(state["projection"])
        self.verify = verify
        proj_set = set(self.projection)
        self._order = ([v for v in problem.domains if v in proj_set] +
                       [v for v in problem.domains if v not in proj_set])
        self._blocking = [dict(b) for b in state["blocking"]]
        self._emitted = [tuple(k) for k in state["emitted"]]
        self._stack = []
        for var, pos in state["stack"]:
            frame = _Frame(var, list(problem.domains[var]))
            frame.pos = pos
            self._stack.append(frame)
        if [f.var for f in self._stack] != self._order[:len(self._stack)]:
            raise ValueError("state does not match problem variable order")
        self._assignment = dict(state["assignment"])
        self._done = bool(state["done"])
        self._pending_backtrack = bool(state["pending_backtrack"])
        self._trivial_pending = False
        self.total_steps = int(state["total_steps"])
        return self
