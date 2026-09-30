"""Integer linear bound propagation with congruence checks.

Python 3.11, standard library only. No SAT/SMT solver is used.

Honesty notes (read before trusting results):
  * Propagation is SOUND but INCOMPLETE: a run that finishes without a
    Contradiction does NOT prove satisfiability. The congruence test
    (gcd of coefficients must divide the rhs of an equality) catches some
    infeasible instances that pure interval propagation misses, but not all.
  * ``Solver.solve()`` layers exhaustive branching over the finite domains
    on top of propagation, which IS complete for the declared domains, and
    every claimed solution is re-verified by the independent checker in
    ``checker.py`` against the original constraints.
  * All arithmetic uses Python ints (arbitrary precision), so coefficients
    and bounds of any size work; there is no machine-word overflow.
"""

from __future__ import annotations

from collections import deque
from math import gcd

from checker import check_assignment

LE, GE, EQ = "<=", ">=", "=="
SENSES = (LE, GE, EQ)


def ceil_div(a: int, b: int) -> int:
    """Exact ceil(a / b) for integers, b != 0 (Python ``//`` already floors)."""
    if b == 0:
        raise ZeroDivisionError("ceil_div: division by zero")
    return -((-a) // b)


class Contradiction(Exception):
    """Propagation derived an empty domain or a false constant constraint."""

    def __init__(self, explanation: str) -> None:
        super().__init__(explanation)
        self.explanation = explanation


class _LeForm:
    """Normalised  sum(a_i * x_i) <= rhs  with zero coefficients removed."""

    __slots__ = ("terms", "rhs")

    def __init__(self, terms, rhs):
        self.terms = terms  # list[tuple[var_index, coeff]]
        self.rhs = rhs


class Constraint:
    """A user constraint plus its normalised propagation form(s)."""

    def __init__(self, cid, coeffs, sense, rhs):
        self.cid = cid
        self.coeffs = coeffs   # original spec: var name -> int coeff
        self.sense = sense
        self.rhs = rhs
        self.active = True
        self.forms = []        # list[_LeForm]; EQ splits into LE + GE forms


class Solver:
    """Queue-based bound propagation to a fixpoint, with explanations,
    checkpoint/backtrack, and constraint retraction with domain recovery."""

    def __init__(self):
        self._var_index = {}          # name -> idx
        self._var_names = []          # idx -> name
        self._lo = []                 # current lower bounds
        self._hi = []                 # current upper bounds
        self._init_lo = []            # declared domains (for retract rebuild)
        self._init_hi = []
        self._constraints = []        # list[Constraint], cid == position
        self._watchers = []           # var idx -> list of cids mentioning it
        self._queue = deque()         # cids pending propagation
        self._in_queue = set()
        self._trail = []              # (var_idx, old_lo, old_hi) for undo
        self.explanations = {}        # var name -> latest pruning explanation
        self.prune_log = []           # full history of narrowing events
        self.on_prune = None          # optional callback(name, old_lo, old_hi,
                                      #                  new_lo, new_hi, expl)

    # ------------------------------------------------------------------ vars
    def add_var(self, name, lo, hi):
        if name in self._var_index:
            raise ValueError(f"duplicate variable {name!r}")
        if lo > hi:
            raise ValueError(f"empty initial domain for {name!r}")
        self._var_index[name] = len(self._var_names)
        self._var_names.append(name)
        self._lo.append(lo)
        self._hi.append(hi)
        self._init_lo.append(lo)
        self._init_hi.append(hi)
        self._watchers.append([])

    def domain(self, name):
        idx = self._var_index[name]
        return self._lo[idx], self._hi[idx]

    # ---------------------------------------------------------- constraints
    def add(self, coeffs, sense, rhs):
        """Add ``sum(coeffs[v] * v) <sense> rhs``. Returns the constraint id.

        Raises Contradiction immediately for false constant constraints and
        for equalities whose rhs is not divisible by the gcd of coefficients.
        """
        if sense not in SENSES:
            raise ValueError(f"unknown sense {sense!r}")
        if not isinstance(rhs, int):
            raise TypeError("rhs must be an int")
        terms = []
        for name, c in coeffs.items():
            if name not in self._var_index:
                raise KeyError(f"unknown variable {name!r}")
            if not isinstance(c, int):
                raise TypeError("coefficients must be ints")
            if c == 0:
                continue  # zero coefficients never affect any domain
            terms.append((self._var_index[name], c))

        cid = len(self._constraints)
        con = Constraint(cid, dict(coeffs), sense, rhs)
        self._constraints.append(con)

        if not terms:
            # Constant constraint: decide immediately, no domain involved.
            holds = {LE: 0 <= rhs, GE: 0 >= rhs, EQ: rhs == 0}[sense]
            if not holds:
                con.active = False
                raise Contradiction(
                    f"constant constraint 0 {sense} {rhs} is false")
            return cid

        # Modular congruence necessary condition for equalities:
        # sum(a_i x_i) is always a multiple of g = gcd(a_i), so g must
        # divide rhs. This catches infeasibilities interval bounds miss.
        g = 0
        for _, c in terms:
            g = gcd(g, abs(c))
        if sense == EQ and rhs % g != 0:
            con.active = False
            raise Contradiction(
                f"congruence: gcd of coefficients is {g}, which does not "
                f"divide rhs {rhs}; no integer solution exists")

        # Normalise. EQ is split into both directions (LE + GE forms).
        if sense in (LE, EQ):
            con.forms.append(_LeForm(terms, rhs))
        if sense in (GE, EQ):
            con.forms.append(_LeForm([(i, -c) for i, c in terms], -rhs))

        for idx, _ in terms:
            self._watchers[idx].append(cid)

        cp = self.checkpoint()
        self._enqueue(cid)
        try:
            self.propagate()
        except Contradiction:
            self.backtrack(cp)
            con.active = False
            raise
        return cid

    def add_le(self, coeffs, rhs):
        return self.add(coeffs, LE, rhs)

    def add_ge(self, coeffs, rhs):
        return self.add(coeffs, GE, rhs)

    def add_eq(self, coeffs, rhs):
        return self.add(coeffs, EQ, rhs)

    def retract(self, cid):
        """Deactivate a constraint and recover the domains without it.

        Later prunings may have depended on the retracted constraint, so
        domains are rebuilt from the declared initial domains by replaying
        every remaining active constraint to a fixpoint.
        """
        con = self._constraints[cid]
        if not con.active:
            return
        con.active = False
        self._lo = list(self._init_lo)
        self._hi = list(self._init_hi)
        self._trail.clear()
        self._queue.clear()
        self._in_queue.clear()
        for c in self._constraints:
            if c.active:
                self._enqueue(c.cid)
        self.propagate()

    # ---------------------------------------------------- checkpoint / undo
    def checkpoint(self):
        return (len(self._trail), len(self._constraints))

    def backtrack(self, cp):
        trail_len, con_count = cp
        while len(self._trail) > trail_len:
            idx, old_lo, old_hi = self._trail.pop()
            self._lo[idx], self._hi[idx] = old_lo, old_hi
        for cid in range(con_count, len(self._constraints)):
            self._constraints[cid].active = False

    # ------------------------------------------------------------ propagation
    def propagate(self):
        """Drain the propagation queue: fixpoint over all woken constraints."""
        while self._queue:
            cid = self._queue.popleft()
            self._in_queue.discard(cid)
            con = self._constraints[cid]
            if not con.active:
                continue
            for form in con.forms:
                self._propagate_form(con, form)

    def _propagate_form(self, con, form):
        # For  sum(a_i x_i) <= rhs  compute the minimum achievable sum, then
        # isolate each variable:  a_j x_j <= rhs - (min of the rest).
        lo, hi = self._lo, self._hi
        min_sum = 0
        for idx, a in form.terms:
            min_sum += a * lo[idx] if a > 0 else a * hi[idx]
        if min_sum > form.rhs:
            raise Contradiction(
                f"constraint #{con.cid} ({con.coeffs} {con.sense} {con.rhs}): "
                f"minimum achievable sum {min_sum} exceeds rhs {form.rhs}")
        for idx, a in form.terms:
            own_min = a * lo[idx] if a > 0 else a * hi[idx]
            rest_min = min_sum - own_min
            slack = form.rhs - rest_min
            name = self._var_names[idx]
            if a > 0:
                # a*x <= slack, a > 0  ->  x <= floor(slack / a)
                bound = slack // a
                self._narrow_hi(idx, bound, {
                    "constraint": con.cid,
                    "coeffs": dict(con.coeffs), "sense": con.sense,
                    "rhs": con.rhs,
                    "detail": f"{a}*{name} <= {form.rhs} - (rest_min="
                              f"{rest_min}) => {name} <= floor({slack}/{a})"
                              f" = {bound}",
                })
            else:
                # a*x <= slack, a < 0  ->  x >= ceil(slack / a)
                bound = ceil_div(slack, a)
                self._narrow_lo(idx, bound, {
                    "constraint": con.cid,
                    "coeffs": dict(con.coeffs), "sense": con.sense,
                    "rhs": con.rhs,
                    "detail": f"{a}*{name} <= {form.rhs} - (rest_min="
                              f"{rest_min}) => {name} >= ceil({slack}/{a})"
                              f" = {bound}",
                })

    # ------------------------------------------------------------ narrowing
    def _narrow_lo(self, idx, new_lo, expl):
        old_lo = self._lo[idx]
        if new_lo <= old_lo:
            return
        name = self._var_names[idx]
        entry = dict(expl, var=name, kind="lower", old=old_lo, new=new_lo)
        if new_lo > self._hi[idx]:
            raise Contradiction(
                f"empty domain for {name}: lower bound {new_lo} exceeds "
                f"upper bound {self._hi[idx]} (constraint "
                f"#{expl.get('constraint')})")
        self._trail.append((idx, old_lo, self._hi[idx]))
        self._lo[idx] = new_lo
        self._record(name, old_lo, self._hi[idx], new_lo, self._hi[idx], entry)
        self._wake(idx)

    def _narrow_hi(self, idx, new_hi, expl):
        old_hi = self._hi[idx]
        if new_hi >= old_hi:
            return
        name = self._var_names[idx]
        entry = dict(expl, var=name, kind="upper", old=old_hi, new=new_hi)
        if new_hi < self._lo[idx]:
            raise Contradiction(
                f"empty domain for {name}: upper bound {new_hi} below "
                f"lower bound {self._lo[idx]} (constraint "
                f"#{expl.get('constraint')})")
        self._trail.append((idx, self._lo[idx], old_hi))
        self._hi[idx] = new_hi
        self._record(name, self._lo[idx], old_hi, self._lo[idx], new_hi, entry)
        self._wake(idx)

    def _record(self, name, old_lo, old_hi, new_lo, new_hi, entry):
        self.explanations[name] = entry
        self.prune_log.append(entry)
        if self.on_prune is not None:
            self.on_prune(name, old_lo, old_hi, new_lo, new_hi, entry)

    def _wake(self, idx):
        for cid in self._watchers[idx]:
            self._enqueue(cid)

    def _enqueue(self, cid):
        if cid not in self._in_queue:
            self._in_queue.add(cid)
            self._queue.append(cid)

    # ---------------------------------------------------------------- solving
    def active_specs(self):
        """Original (coeffs, sense, rhs) triples of all active constraints."""
        return [(c.coeffs, c.sense, c.rhs)
                for c in self._constraints if c.active]

    def solve(self):
        """Return a verified satisfying assignment, or None if unsatisfiable.

        Branching over the finite domains makes this complete (unlike bare
        propagation); every returned assignment is re-checked by the
        independent checker against the original constraints.
        """
        try:
            self.propagate()
            return self._search()
        except Contradiction:
            return None

    def _search(self):
        idx = self._branch_var()
        if idx is None:
            assignment = {n: self._lo[i]
                          for i, n in enumerate(self._var_names)}
            if not check_assignment(self.active_specs(), assignment):
                raise AssertionError(
                    "internal error: candidate failed independent check")
            return assignment
        lo, hi = self._lo[idx], self._hi[idx]
        mid = (lo + hi) // 2
        name = self._var_names[idx]
        cp = self.checkpoint()
        try:
            self._narrow_hi(idx, mid,
                            {"constraint": None,
                             "detail": f"branch {name} <= {mid}"})
            self.propagate()
            found = self._search()
            if found is not None:
                return found
        except Contradiction:
            pass
        self.backtrack(cp)
        self._narrow_lo(idx, mid + 1,
                        {"constraint": None,
                         "detail": f"branch {name} >= {mid + 1}"})
        self.propagate()
        return self._search()

    def _branch_var(self):
        best = None
        best_width = None
        for idx in range(len(self._var_names)):
            width = self._hi[idx] - self._lo[idx]
            if width > 0 and (best_width is None or width < best_width):
                best, best_width = idx, width
        return best
