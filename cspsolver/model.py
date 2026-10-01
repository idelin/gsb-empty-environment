"""Model definitions: finite-domain variables, hard and soft constraints."""
from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Dict, Iterable, List, Tuple

Predicate = Callable[..., bool]


def _check_int(value: object, what: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise TypeError(f"{what} must be an int, got {type(value).__name__}")
    return value


class Variable:
    """A variable with a finite domain of integers."""

    def __init__(self, name: str, domain: Iterable[int]):
        if not isinstance(name, str) or not name:
            raise ValueError("variable name must be a non-empty string")
        values = tuple(sorted(set(domain)))
        if not values:
            raise ValueError(f"variable {name!r} has an empty domain")
        for v in values:
            _check_int(v, f"domain value of {name!r}")
        self.name = name
        self.domain: Tuple[int, ...] = values

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return f"Variable({self.name!r}, {self.domain!r})"


@dataclass(frozen=True)
class HardConstraint:
    """A constraint that must hold in every returned solution.

    Hard constraints are never penalized: if they conflict, the model is
    UNSAT, period. There is no way to "buy" through a hard conflict.
    """

    name: str
    var_names: Tuple[str, ...]
    predicate: Predicate


@dataclass(frozen=True)
class SoftConstraint:
    """A constraint whose violation adds ``weight`` to the penalty of ``level``.

    ``weight`` must be a non-negative integer; negative weights are rejected
    (they would break lexicographic branch & bound). ``level`` 0 is the
    highest priority.
    """

    name: str
    var_names: Tuple[str, ...]
    predicate: Predicate
    weight: int
    level: int = 0


class Model:
    def __init__(self) -> None:
        self.variables: Dict[str, Variable] = {}
        self.hard: List[HardConstraint] = []
        self.soft: List[SoftConstraint] = []

    def add_variable(self, var: Variable) -> Variable:
        if var.name in self.variables:
            raise ValueError(f"duplicate variable name {var.name!r}")
        self.variables[var.name] = var
        return var

    def add_hard(
        self, name: str, var_names: Iterable[str], predicate: Predicate
    ) -> HardConstraint:
        c = HardConstraint(name=name, var_names=tuple(var_names), predicate=predicate)
        self._check_constraint(c)
        self.hard.append(c)
        return c

    def add_soft(
        self,
        name: str,
        var_names: Iterable[str],
        predicate: Predicate,
        weight: int,
        level: int = 0,
    ) -> SoftConstraint:
        _check_int(weight, f"weight of soft constraint {name!r}")
        if weight < 0:
            raise ValueError(
                f"soft constraint {name!r}: negative weights are rejected "
                f"(got {weight}); penalties must be non-negative integers"
            )
        _check_int(level, f"level of soft constraint {name!r}")
        if level < 0:
            raise ValueError(f"soft constraint {name!r}: level must be >= 0")
        c = SoftConstraint(
            name=name,
            var_names=tuple(var_names),
            predicate=predicate,
            weight=weight,
            level=level,
        )
        self._check_constraint(c)
        self.soft.append(c)
        return c

    def _check_constraint(self, c) -> None:
        if not c.name:
            raise ValueError("constraint name must be non-empty")
        if not c.var_names:
            raise ValueError(f"constraint {c.name!r} must reference >= 1 variable")
        unknown = [v for v in c.var_names if v not in self.variables]
        if unknown:
            raise ValueError(
                f"constraint {c.name!r} references unknown variables: {unknown}"
            )

    @property
    def num_levels(self) -> int:
        if not self.soft:
            return 0
        return max(c.level for c in self.soft) + 1
