"""Demo instances with a known, enumeration-checkable core structure.

Flagship instance ``order_sensitive_problem`` (a, b in {0, 1}):

  k1: a == 1     k2: b == 0     k3: a == 0     k4: b == 1     k5: a == b

Its MUSes are {k1, k3}, {k2, k4} (size 2) and {k1, k2, k5}, {k3, k4, k5}
(size 3). Deletion-based extraction in pool order lands on the size-3
subset-minimal core {k3, k4, k5}, while hitting-set search proves the
minimum-cardinality cores have size 2: subset-minimal != minimum-cardinality.
"""

from __future__ import annotations

from .solver import Constraint, Problem


def order_sensitive_problem() -> Problem:
    domains = {"a": (0, 1), "b": (0, 1)}
    constraints = (
        Constraint("k1: a==1", ("a",), lambda a: a == 1),
        Constraint("k2: b==0", ("b",), lambda b: b == 0),
        Constraint("k3: a==0", ("a",), lambda a: a == 0),
        Constraint("k4: b==1", ("b",), lambda b: b == 1),
        Constraint("k5: a==b", ("a", "b"), lambda a, b: a == b),
    )
    return Problem(domains, constraints)


def tiny_sat_problem() -> Problem:
    domains = {"x": (0, 1, 2)}
    constraints = (
        Constraint("c1: x>=1", ("x",), lambda x: x >= 1),
        Constraint("c2: x<=2", ("x",), lambda x: x <= 2),
    )
    return Problem(domains, constraints)


def tiny_unsat_problem() -> Problem:
    domains = {"x": (0, 1, 2)}
    constraints = (
        Constraint("c1: x==0", ("x",), lambda x: x == 0),
        Constraint("c2: x>=1", ("x",), lambda x: x >= 1),
    )
    return Problem(domains, constraints)
