"""Versioned domain model: facts, conditions and rules.

All conditions are finite (finite enumerations or inclusive integer
intervals), so every rule condition space is finite and analysis evidence
can be replayed by deterministic re-evaluation.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable


class Condition:
    """A finite set of admissible values for one fact field."""

    kind = "?"

    def intersect(self, other: "Condition") -> "Condition | None":
        raise NotImplementedError

    def covers(self, other: "Condition") -> bool:
        """True iff every value of ``other`` is also a value of self."""
        raise NotImplementedError

    def contains(self, value: Any) -> bool:
        raise NotImplementedError

    def pick(self) -> Any:
        """Deterministic witness value (smallest value)."""
        raise NotImplementedError

    def size(self) -> int:
        raise NotImplementedError

    def sorted_values(self) -> Iterable[Any]:
        raise NotImplementedError


class EnumSet(Condition):
    """Finite enumeration of allowed values."""

    kind = "enum"

    def __init__(self, options: Iterable[Any]):
        self.options = frozenset(options)
        if not self.options:
            raise ValueError("EnumSet requires at least one option")

    def intersect(self, other: "Condition") -> "Condition | None":
        if not isinstance(other, EnumSet):
            return None
        common = self.options & other.options
        return EnumSet(common) if common else None

    def covers(self, other: "Condition") -> bool:
        return isinstance(other, EnumSet) and other.options <= self.options

    def contains(self, value: Any) -> bool:
        return value in self.options

    def pick(self) -> Any:
        return next(iter(self.sorted_values()))

    def size(self) -> int:
        return len(self.options)

    def sorted_values(self) -> tuple[Any, ...]:
        return tuple(sorted(self.options))

    def __eq__(self, other: object) -> bool:
        return isinstance(other, EnumSet) and self.options == other.options

    def __hash__(self) -> int:
        return hash(("EnumSet", self.options))

    def __repr__(self) -> str:
        return f"EnumSet({sorted(self.options)!r})"


class IntRange(Condition):
    """Inclusive integer interval [lo, hi]."""

    kind = "int"

    def __init__(self, lo: int, hi: int):
        self.lo = int(lo)
        self.hi = int(hi)
        if self.lo > self.hi:
            raise ValueError(f"empty interval [{lo}, {hi}]")

    def intersect(self, other: "Condition") -> "Condition | None":
        if not isinstance(other, IntRange):
            return None
        lo = max(self.lo, other.lo)
        hi = min(self.hi, other.hi)
        return IntRange(lo, hi) if lo <= hi else None

    def covers(self, other: "Condition") -> bool:
        return (
            isinstance(other, IntRange)
            and self.lo <= other.lo
            and other.hi <= self.hi
        )

    def contains(self, value: Any) -> bool:
        return (
            isinstance(value, int)
            and not isinstance(value, bool)
            and self.lo <= value <= self.hi
        )

    def pick(self) -> int:
        return self.lo

    def size(self) -> int:
        return self.hi - self.lo + 1

    def sorted_values(self) -> range:
        return range(self.lo, self.hi + 1)

    def __eq__(self, other: object) -> bool:
        return (
            isinstance(other, IntRange)
            and (self.lo, self.hi) == (other.lo, other.hi)
        )

    def __hash__(self) -> int:
        return hash(("IntRange", self.lo, self.hi))

    def __repr__(self) -> str:
        return f"IntRange({self.lo}, {self.hi})"


@dataclass(frozen=True)
class FactSchema:
    """Versioned fact type: a set of field domains."""

    fact_id: str
    version: int
    domains: dict[str, Condition]

    @property
    def ref(self) -> str:
        return f"{self.fact_id}@v{self.version}"


@dataclass(frozen=True)
class Rule:
    """A versioned rule. A field missing from ``conditions`` is a wildcard."""

    rule_id: str
    version: int
    priority: int  # larger value = higher priority
    effect: str
    conditions: dict[str, Condition]


def rule_ref(rule: Rule) -> str:
    return f"{rule.rule_id}@v{rule.version}"


def rule_matches(rule: Rule, fact_values: dict[str, Any]) -> bool:
    return all(
        cond.contains(fact_values.get(field))
        for field, cond in rule.conditions.items()
    )


@dataclass(frozen=True)
class Decision:
    winners: tuple[Rule, ...]
    conflict: bool


def decide(rules: Iterable[Rule], fact_values: dict[str, Any]) -> Decision | None:
    """Evaluate rules on a concrete input. A conflict means that the rules
    with the highest matching priority disagree on the effect."""

    matching = [rule for rule in rules if rule_matches(rule, fact_values)]
    if not matching:
        return None
    top_priority = max(rule.priority for rule in matching)
    winners = tuple(rule for rule in matching if rule.priority == top_priority)
    return Decision(winners, len({rule.effect for rule in winners}) > 1)
