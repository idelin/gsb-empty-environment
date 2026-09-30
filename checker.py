"""Independent solution checker and brute-force enumerator.

The checker deliberately ignores the trie / support machinery: it tests a
candidate assignment directly against the *original* tuple data kept on
each constraint, so a bug in the compressed table cannot validate itself.
"""

from __future__ import annotations

import itertools

from tuple_table import TupleTable


def verify_solution(csp, solution) -> bool:
    """True iff `solution` satisfies every original constraint of `csp`."""
    if not isinstance(solution, dict):
        return False
    if set(solution.keys()) != set(csp.domains.keys()):
        return False
    for var, value in solution.items():
        if value not in csp.domains[var]:
            return False
    for con in csp.constraints:
        proj = tuple(solution[var] for var in con.scope)
        member = proj in con.original
        if con.polarity == TupleTable.POSITIVE and not member:
            return False
        if con.polarity == TupleTable.NEGATIVE and member:
            return False
    return True


def brute_force_solutions(csp):
    """Yield every assignment satisfying all constraints (slow, exact)."""
    variables = list(csp.domains)
    domains = [sorted(csp.domains[v]) for v in variables]
    for combo in itertools.product(*domains):
        assignment = dict(zip(variables, combo))
        if verify_solution(csp, assignment):
            yield assignment


def is_satisfiable_brute_force(csp) -> bool:
    return next(brute_force_solutions(csp), None) is not None
