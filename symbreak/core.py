"""Core CSP model: finite-domain variables, constraints, optional objective.

No SAT/SMT libraries are used anywhere in this package; everything is
plain brute-force enumeration over finite domains (Python stdlib only).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product
from typing import Callable, Hashable, Mapping, Sequence

Value = Hashable
Assignment = dict


@dataclass(frozen=True)
class Constraint:
    """A constraint over a fixed scope; predicate takes values in scope order."""

    name: str
    scope: tuple
    predicate: Callable[..., bool]

    def holds(self, assignment: Mapping) -> bool:
        return bool(self.predicate(*(assignment[v] for v in self.scope)))


@dataclass
class Problem:
    """A finite-domain CSP.

    ``constraints`` are the original, user-supplied constraints.
    ``symmetry_constraints`` are lexicographic-representative constraints
    added later by the symmetry-breaking API -- and only after the
    underlying permutation has been *proven* to be an automorphism of the
    original constraints and objective.
    """

    domains: dict
    constraints: list = field(default_factory=list)
    objective: Callable[[Mapping], float] | None = None
    objective_name: str = "objective"
    special: frozenset = frozenset()
    symmetry_constraints: list = field(default_factory=list)

    def __post_init__(self):
        if not self.domains:
            raise ValueError("problem needs at least one variable")
        for var, dom in self.domains.items():
            if not dom:
                raise ValueError(f"empty domain for variable {var!r}")
        for con in self.constraints + self.symmetry_constraints:
            for var in con.scope:
                if var not in self.domains:
                    raise ValueError(
                        f"constraint {con.name!r} mentions unknown variable {var!r}"
                    )
        unknown_special = set(self.special) - set(self.domains)
        if unknown_special:
            raise ValueError(f"special variables not in problem: {sorted(unknown_special)}")

    @property
    def variables(self) -> list:
        return list(self.domains)

    def assignments(self, variables: Sequence | None = None):
        """Yield (partial) assignments over ``variables`` in canonical order."""
        vs = list(variables) if variables is not None else self.variables
        for values in product(*(sorted(self.domains[v]) for v in vs)):
            yield dict(zip(vs, values))
