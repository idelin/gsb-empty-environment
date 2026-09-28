from __future__ import annotations

from collections import deque
from collections.abc import Iterable, Sequence
from itertools import product
from types import TracebackType

from .assumptions import Assumption
from .checker import validate_assignment
from .constraints import Constraint, Predicate
from .domain import DomainStore


class AssumptionScope:
    def __init__(self, solver: "Solver", assumptions: Sequence[Assumption | tuple[str, Iterable[object] | object]]):
        self._solver = solver
        self._assumptions = assumptions

    def __enter__(self) -> "Solver":
        self._solver.push_scope(self._assumptions)
        return self._solver

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self._solver.cancel_scope()


class Solver:
    def __init__(self, domains: dict[str, Iterable[object]]):
        self.domains_store = DomainStore(domains)
        self._constraints: list[Constraint] = []
        self._assumption_constraints: list[list[Constraint]] = []
        self._assumptions: list[list[Assumption]] = []
        self._assumption_count = 0

    @property
    def scope_depth(self) -> int:
        return len(self._assumption_constraints)

    def domain(self, variable: str) -> frozenset[object]:
        return self.domains_store.domain(variable)

    def domains(self) -> dict[str, frozenset[object]]:
        return self.domains_store.domains()

    def initial_domains(self) -> dict[str, frozenset[object]]:
        return self._initial_domains()

    def constraints(self) -> tuple[Constraint, ...]:
        return tuple(self._constraints)

    def add_constraint(
        self,
        variables: Sequence[str],
        predicate: Predicate,
        name: str | None = None,
    ) -> None:
        if self.scope_depth:
            raise RuntimeError("permanent constraints cannot be added inside an assumption scope")
        variables = tuple(variables)
        if not variables:
            raise ValueError("constraint requires at least one variable")
        if len(set(variables)) != len(variables):
            raise ValueError("constraint variables must be unique")
        missing = [variable for variable in variables if variable not in self.domains_store.variables()]
        if missing:
            raise KeyError(f"unknown variables: {', '.join(missing)}")
        constraint_name = name or f"constraint_{len(self._constraints)}"
        self._constraints.append(Constraint(variables, predicate, constraint_name))

    def assume(self, variable: str, values: Iterable[object] | object) -> AssumptionScope:
        return self.assuming(((variable, values),))

    def assuming(
        self,
        assumptions: Sequence[Assumption | tuple[str, Iterable[object] | object]],
    ) -> AssumptionScope:
        return AssumptionScope(self, assumptions)

    def push_scope(
        self,
        assumptions: Sequence[Assumption | tuple[str, Iterable[object] | object]] = (),
    ) -> int:
        normalized: list[Assumption] = []
        constraints: list[Constraint] = []
        for item in assumptions:
            assumption = item if isinstance(item, Assumption) else Assumption(item[0], item[1])
            if assumption.variable not in self.domains_store.variables():
                raise KeyError(f"unknown variable: {assumption.variable}")
            normalized.append(assumption)
            self._assumption_count += 1
            constraint = Constraint(
                (assumption.variable,),
                assumption.restriction,
                f"assumption_{self._assumption_count}",
            )
            constraints.append(constraint)

        self.domains_store.push_level()
        self._assumptions.append(normalized)
        self._assumption_constraints.append(constraints)
        return self.scope_depth

    def cancel_scope(self) -> None:
        if not self.scope_depth:
            raise RuntimeError("no assumption scope to cancel")
        self.domains_store.pop_level()
        self._assumption_constraints.pop()
        self._assumptions.pop()

    pop_scope = cancel_scope

    def active_constraints(self) -> list[Constraint]:
        return self._constraints + [
            constraint for layer in self._assumption_constraints for constraint in layer
        ]

    def active_assumptions(self) -> list[Assumption]:
        return [assumption for layer in self._assumptions for assumption in layer]

    def propagate(self) -> bool:
        return self._gac()

    def solve(self) -> dict[str, object] | None:
        self.domains_store.push_level()
        try:
            if not self._gac():
                return None
            assignment = self._search()
            if assignment is None:
                return None
            if not validate_assignment(
                assignment,
                self._initial_domains(),
                self._constraints,
                self.active_assumptions(),
            ):
                raise RuntimeError("internal check failed: candidate violates original constraints")
            return assignment
        finally:
            self.domains_store.pop_level()

    def _initial_domains(self) -> dict[str, frozenset[object]]:
        return {
            variable: self.domains_store.initial_domain(variable)
            for variable in self.domains_store.variables()
        }

    def _has_support(self, constraint: Constraint, variable: str, value: object) -> bool:
        others = [name for name in constraint.variables if name != variable]
        if not others:
            return bool(constraint.predicate(value))
        for support_values in product(*(self.domains_store.domain(name) for name in others)):
            values = dict(zip(others, support_values, strict=True))
            values[variable] = value
            try:
                if constraint.holds(values):
                    return True
            except (KeyError, TypeError):
                continue
        return False

    def _gac(self) -> bool:
        constraints = self.active_constraints()
        queue = deque(
            (constraint, variable)
            for constraint in constraints
            for variable in dict.fromkeys(constraint.variables)
        )
        queued = set(queue)

        while queue:
            constraint, variable = queue.popleft()
            queued.discard((constraint, variable))
            pruned = [
                value
                for value in self.domains_store.domain(variable)
                if not self._has_support(constraint, variable, value)
            ]
            for value in pruned:
                self.domains_store.remove(variable, value)
            if not self.domains_store.domain(variable):
                return False
            if pruned:
                for related in constraints:
                    if variable in related.variables:
                        for neighbor in dict.fromkeys(related.variables):
                            item = (related, neighbor)
                            if neighbor != variable and item not in queued:
                                queue.append(item)
                                queued.add(item)
        return True

    def _search(self) -> dict[str, object] | None:
        domains = self.domains_store.domains()
        if all(len(domain) == 1 for domain in domains.values()):
            return {variable: next(iter(domain)) for variable, domain in domains.items()}

        variable = min(
            (name for name, domain in domains.items() if len(domain) > 1),
            key=lambda name: (len(domains[name]), name),
        )
        for value in sorted(domains[variable], key=repr):
            self.domains_store.push_level()
            try:
                for unsupported in self.domains_store.domain(variable) - {value}:
                    self.domains_store.remove(variable, unsupported)
                if self._gac():
                    assignment = self._search()
                    if assignment is not None:
                        return assignment
            finally:
                self.domains_store.pop_level()
        return None
