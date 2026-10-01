"""Factor graph container with a shared variable/domain registry."""

from __future__ import annotations

from .errors import DomainConflictError, UnknownVariableError
from .factors import Factor


class FactorGraph:
    """A finite discrete factor graph.

    Variables are declared once with a fixed domain; redeclaring a
    variable with a *different* domain is rejected.
    """

    def __init__(self):
        self.domains = {}
        self.factors = []

    def add_variable(self, name, domain):
        domain = tuple(domain)
        if not domain:
            raise ValueError(f"domain of {name!r} must be non-empty")
        if name in self.domains:
            if self.domains[name] != domain:
                raise DomainConflictError(
                    f"variable {name!r} already has domain "
                    f"{self.domains[name]}, cannot redeclare as {domain}"
                )
            return self
        self.domains[name] = domain
        return self

    def add_factor(self, variables, table):
        """Create a factor over previously declared variables."""
        missing = [v for v in variables if v not in self.domains]
        if missing:
            raise UnknownVariableError(
                f"undeclared variable(s) {missing}; call add_variable first"
            )
        factor = Factor(tuple(variables), self.domains, table)
        self.factors.append(factor)
        return factor

    @property
    def variables(self):
        return tuple(self.domains)

    def joint_weight(self, assignment):
        """Product of all factor values at a complete assignment."""
        w = 1.0
        for f in self.factors:
            w *= f.value(assignment)
            if w == 0.0:
                break
        return w
