"""Core CNF infrastructure: variable pool, CNF formulas, unit propagation
and a small self-contained DPLL solver (no SAT/SMT libraries involved).
"""

from __future__ import annotations


class VarPool:
    """Allocates globally unique variable ids.

    All encodings sharing one pool are guaranteed to receive non-conflicting
    variable numbers.  Optional human-readable names are also kept unique.
    """

    def __init__(self):
        self._next = 1
        self.names = {}
        self._used_names = set()
        self._tag_counter = 0

    def new_var(self, name=None):
        vid = self._next
        self._next += 1
        if name is not None:
            if name in self._used_names:
                raise ValueError(f"duplicate variable name: {name!r}")
            self._used_names.add(name)
            self.names[vid] = name
        return vid

    def reserve(self, count, prefix="x"):
        """Reserve ``count`` fresh variables, typically the original ones."""
        return [self.new_var(f"{prefix}{i + 1}") for i in range(count)]

    def fresh_tag(self, prefix):
        """Return a pool-unique namespace tag for auxiliary variables."""
        self._tag_counter += 1
        return f"{prefix}#{self._tag_counter}"

    @property
    def max_var(self):
        return self._next - 1


class CNF:
    """A CNF formula: a list of clauses, each a list of signed literals."""

    def __init__(self):
        self.clauses = []

    def add_clause(self, clause):
        self.clauses.append(list(clause))

    def add(self, *lits):
        self.add_clause(lits)

    def extend(self, other):
        self.clauses.extend(other.clauses)

    @property
    def num_clauses(self):
        return len(self.clauses)

    def vars(self):
        return {abs(lit) for clause in self.clauses for lit in clause}

    def is_trivially_unsat(self):
        return any(len(clause) == 0 for clause in self.clauses)

    def evaluate(self, assignment):
        """Evaluate under a (possibly partial) assignment.

        Returns True/False when decided, None when some clause is undecided.
        """
        undecided = False
        for clause in self.clauses:
            satisfied = False
            open_lit = False
            for lit in clause:
                var = abs(lit)
                if var not in assignment:
                    open_lit = True
                    continue
                if assignment[var] == (lit > 0):
                    satisfied = True
                    break
            if satisfied:
                continue
            if not open_lit:
                return False
            undecided = True
        return None if undecided else True

    def __repr__(self):
        return f"CNF({self.clauses!r})"


def unit_propagate(clauses, assignment):
    """Close ``assignment`` under unit propagation.

    Returns ``(assignment, ok)``; ``ok`` is False when a clause is falsified.
    """
    assign = dict(assignment)
    changed = True
    while changed:
        changed = False
        for clause in clauses:
            satisfied = False
            open_lits = []
            for lit in clause:
                var = abs(lit)
                if var in assign:
                    if assign[var] == (lit > 0):
                        satisfied = True
                        break
                else:
                    open_lits.append(lit)
            if satisfied:
                continue
            if not open_lits:
                return assign, False
            if len(open_lits) == 1:
                lit = open_lits[0]
                assign[abs(lit)] = lit > 0
                changed = True
    return assign, True


def _clause_status(clause, assign):
    open_lits = []
    for lit in clause:
        var = abs(lit)
        if var in assign:
            if assign[var] == (lit > 0):
                return True, open_lits
        else:
            open_lits.append(lit)
    return False, open_lits


def _dpll(clauses, assign, num_vars):
    assign, ok = unit_propagate(clauses, assign)
    if not ok:
        return None
    branch_var = None
    for clause in clauses:
        satisfied, open_lits = _clause_status(clause, assign)
        if not satisfied:
            branch_var = abs(open_lits[0])
            break
    if branch_var is None:
        full = dict(assign)
        for var in range(1, num_vars + 1):
            full.setdefault(var, False)
        return full
    for value in (True, False):
        child = dict(assign)
        child[branch_var] = value
        result = _dpll(clauses, child, num_vars)
        if result is not None:
            return result
    return None


def solve(cnf, num_vars=None, assumptions=()):
    """Tiny DPLL solver with assumption literals (held fixed for this call).

    Returns a full satisfying assignment dict, or None when unsatisfiable.
    """
    if num_vars is None:
        num_vars = max(cnf.vars(), default=0)
        for lit in assumptions:
            num_vars = max(num_vars, abs(lit))
    assign = {}
    for lit in assumptions:
        var, value = abs(lit), lit > 0
        if var in assign and assign[var] != value:
            return None
        assign[var] = value
    return _dpll(cnf.clauses, assign, num_vars)
