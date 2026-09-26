"""Finite-domain solution enumeration with projected de-duplication.

The solver is an explicit-stack backtracking enumerator.  It does not use or
implement a SAT/SMT library.  Constraints are ordinary Python predicates over
complete finite-domain assignments.
"""

from __future__ import annotations

from collections.abc import Callable, Hashable, Mapping, Sequence
from dataclasses import dataclass
from types import MappingProxyType
from typing import Any

Constraint = Callable[[Mapping[str, Any]], bool]
VariableDomains = Mapping[str, Sequence[Any]]
ProjectionKey = tuple[Hashable, ...]


class VerificationError(RuntimeError):
    """Raised when an independent check rejects a candidate solution."""


@dataclass(frozen=True)
class ProjectedSolution:
    """One projected solution and one complete satisfying witness."""

    values: dict[str, Any]
    witness: dict[str, Any]


@dataclass(frozen=True)
class EnumerationResult:
    """Solutions returned by one run and the state of the search."""

    solutions: tuple[ProjectedSolution, ...]
    complete: bool
    status: str
    nodes_used: int

    @property
    def resumable(self) -> bool:
        return not self.complete


Checker = Callable[[Mapping[str, Any], Sequence[Constraint]], None]


def independent_check(
    assignment: Mapping[str, Any], constraints: Sequence[Constraint]
) -> None:
    """Check a complete assignment against the original constraint predicates.

    This function is intentionally separate from candidate generation.  A
    rejected candidate means the enumerator must not claim it as a solution.
    """

    read_only = MappingProxyType(dict(assignment))
    for index, constraint in enumerate(constraints):
        if not constraint(read_only):
            raise VerificationError(
                f"independent checker rejected constraint {index}: {constraint!r}"
            )


class ProjectingEnumerator:
    """Resume enumeration of projected finite-domain solutions.

    Each emitted projection causes one full-solution blocking clause.  That
    clause forbids only the complete emitted assignment, including auxiliary
    variables.  Repeated projections from other auxiliary assignments are
    suppressed by the emitted-key set and do not generate blocking clauses.
    """

    def __init__(
        self,
        variables: VariableDomains,
        constraints: Sequence[Constraint],
        projection: Sequence[str] | None = None,
        *,
        checker: Checker | None = None,
    ) -> None:
        names = tuple(variables)
        if not names:
            raise ValueError("at least one variable is required")
        if len(set(names)) != len(names):
            raise ValueError("variable names must be unique")

        domains: list[tuple[Hashable, ...]] = []
        for name in names:
            domain = tuple(variables[name])
            if not domain:
                raise ValueError(f"variable {name!r} has an empty domain")
            if any(not isinstance(value, Hashable) for value in domain):
                raise TypeError(f"variable {name!r} contains an unhashable value")
            domains.append(domain)

        projection_tuple = tuple(names if projection is None else projection)
        if len(set(projection_tuple)) != len(projection_tuple):
            raise ValueError("projection contains a repeated variable")
        unknown = sorted(set(projection_tuple) - set(names))
        if unknown:
            raise ValueError(f"projection contains unknown variables: {unknown}")
        if not all(callable(constraint) for constraint in constraints):
            raise TypeError("all constraints must be callable")

        self._names = names
        self._domains = tuple(domains)
        self._constraints = tuple(constraints)
        self._projection = projection_tuple
        self._checker = checker
        if checker is not None and not callable(checker):
            raise TypeError("checker must be callable or None")

        self._assignment: dict[str, Any] = {}
        self._stack: list[int] = [0]
        self._emitted: set[ProjectionKey] = set()
        self._blocking_clauses: list[tuple[tuple[str, str, Hashable], ...]] = []
        self._complete = False

    @property
    def complete(self) -> bool:
        return self._complete

    @property
    def emitted_projection_keys(self) -> frozenset[ProjectionKey]:
        return frozenset(self._emitted)

    @property
    def blocking_clauses(self) -> tuple[tuple[tuple[str, str, Hashable], ...], ...]:
        return tuple(self._blocking_clauses)

    @property
    def search_stack(self) -> tuple[tuple[str, int], ...]:
        """Current frames as ``(variable, next_domain_index)``."""

        return tuple(
            (self._names[index], next_index)
            for index, next_index in enumerate(self._stack)
        )

    def run(
        self,
        *,
        max_solutions: int | None = None,
        node_budget: int | None = None,
    ) -> EnumerationResult:
        """Return up to ``max_solutions`` new projected solutions.

        ``node_budget`` counts one variable assignment node.  When the budget is
        spent, the explicit stack remains in this object and another ``run``
        call continues without repeating an emitted solution.
        """

        if max_solutions is not None and max_solutions <= 0:
            raise ValueError("max_solutions must be positive or None")
        if node_budget is not None and node_budget <= 0:
            raise ValueError("node_budget must be positive or None")

        emitted_now: list[ProjectedSolution] = []
        nodes_used = 0
        status = "complete"

        while not self._complete:
            if max_solutions is not None and len(emitted_now) >= max_solutions:
                status = "solution_limit"
                break
            if node_budget is not None and nodes_used >= node_budget:
                status = "node_budget"
                break

            nodes_used += 1
            frame_index = len(self._stack) - 1
            next_value_index = self._stack[frame_index]

            if next_value_index < len(self._domains[frame_index]):
                name = self._names[frame_index]
                self._assignment[name] = self._domains[frame_index][next_value_index]
                self._stack[frame_index] = next_value_index + 1

                if frame_index == len(self._names) - 1:
                    emitted = self._process_leaf()
                    del self._assignment[name]
                    if emitted is not None:
                        emitted_now.append(emitted)
                else:
                    self._stack.append(0)
                continue

            self._stack.pop()
            self._assignment.pop(self._names[frame_index], None)
            if not self._stack:
                self._complete = True

        return EnumerationResult(
            solutions=tuple(emitted_now),
            complete=self._complete,
            status=status,
            nodes_used=nodes_used,
        )

    def _process_leaf(self) -> ProjectedSolution | None:
        assignment = MappingProxyType(self._assignment)
        if self._is_blocked(assignment):
            return None

        if not all(constraint(assignment) for constraint in self._constraints):
            return None

        independent_check(assignment, self._constraints)
        if self._checker is not None:
            self._checker(assignment, self._constraints)

        key = tuple(assignment[name] for name in self._projection)
        if key in self._emitted:
            return None

        witness = dict(assignment)
        projected = {name: witness[name] for name in self._projection}
        self._emitted.add(key)
        self._blocking_clauses.append(
            tuple((name, "!=", witness[name]) for name in self._names)
        )

        if not self._projection:
            self._complete = True
            self._stack = []

        return ProjectedSolution(values=projected, witness=witness)

    def _is_blocked(self, assignment: Mapping[str, Any]) -> bool:
        for clause in self._blocking_clauses:
            if all(
                operator == "!="
                and name in assignment
                and assignment[name] == value
                for name, operator, value in clause
            ):
                return True
        return False


def enumerate_solutions(
    variables: VariableDomains,
    constraints: Sequence[Constraint],
    projection: Sequence[str] | None = None,
    *,
    checker: Checker | None = None,
    max_solutions: int | None = None,
) -> EnumerationResult:
    """Convenience wrapper for a single complete enumeration run."""

    return ProjectingEnumerator(
        variables, constraints, projection, checker=checker
    ).run(max_solutions=max_solutions)
