"""Independent verifier and exact reference enumeration (stdlib only).

This module deliberately shares no state with ``solver.py``: it re-evaluates
the ORIGINAL constraint specifications, so a claimed solution is checked
against what the user actually asked for, not against normalised forms.
"""

from itertools import product

LE, GE, EQ = "<=", ">=", "=="


def check_assignment(specs, assignment):
    """True iff ``assignment`` satisfies every (coeffs, sense, rhs) spec."""
    for coeffs, sense, rhs in specs:
        total = 0
        for var, c in coeffs.items():
            if var not in assignment:
                raise KeyError(f"assignment missing variable {var!r}")
            total += c * assignment[var]
        if sense == LE and not total <= rhs:
            return False
        if sense == GE and not total >= rhs:
            return False
        if sense == EQ and not total == rhs:
            return False
    return True


def enumerate_solutions(domains, specs):
    """Exact reference: brute-force every point of the finite domains.

    ``domains`` maps var -> (lo, hi). Returns the list of satisfying
    assignments. Only suitable for small domains (used as the test oracle).
    """
    names = list(domains)
    ranges = [range(domains[n][0], domains[n][1] + 1) for n in names]
    solutions = []
    for values in product(*ranges):
        assignment = dict(zip(names, values))
        if check_assignment(specs, assignment):
            solutions.append(assignment)
    return solutions
