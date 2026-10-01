"""Independent reference implementations used to cross-validate ac3.CSP.

Deliberately written in the simplest possible style (full sweeps, brute
force) so it shares no logic with the incremental solver. Standard library
only; no SAT/SMT solvers.
"""


def reference_ac(universe, constraints):
    """From-scratch arc consistency via naive full sweeps to a fixpoint.

    universe: {var: iterable of values}
    constraints: list of (a, b, pred) where pred(a_value, b_value) -> bool
    returns {var: set of surviving values}
    """
    domains = {var: set(values) for var, values in universe.items()}
    changed = True
    while changed:
        changed = False
        for a, b, pred in constraints:
            for var, other, flip in ((a, b, False), (b, a, True)):
                for value in list(domains[var]):
                    if flip:
                        ok = any(pred(w, value) for w in domains[other])
                    else:
                        ok = any(pred(value, w) for w in domains[other])
                    if not ok:
                        domains[var].discard(value)
                        changed = True
    return domains


def enumerate_solutions(universe, constraints, limit=None):
    """Complete backtracking enumeration of all satisfying assignments."""
    variables = sorted(universe)
    solutions = []
    assignment = {}

    def consistent(var, value):
        for a, b, pred in constraints:
            if a == var and b in assignment and not pred(value, assignment[b]):
                return False
            if b == var and a in assignment and not pred(assignment[a], value):
                return False
        return True

    def backtrack(index):
        if limit is not None and len(solutions) >= limit:
            return
        if index == len(variables):
            solutions.append(dict(assignment))
            return
        var = variables[index]
        for value in sorted(universe[var]):
            if consistent(var, value):
                assignment[var] = value
                backtrack(index + 1)
                del assignment[var]

    backtrack(0)
    return solutions


def check_assignment(universe, constraints, assignment):
    """Independent checker: assignment satisfies every original constraint."""
    if set(assignment) != set(universe):
        return False
    for var, value in assignment.items():
        if value not in universe[var]:
            return False
    for a, b, pred in constraints:
        if not pred(assignment[a], assignment[b]):
            return False
    return True
