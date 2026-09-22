"""Attribute conditions on node property maps.

A condition is a small predicate evaluated against the attribute mapping of a
node.  Conditions are parsed from plain data (None / tuple / dict), so whole
automata can be specified declaratively without importing this module.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping

_OPS = {
    "==": lambda a, b: a == b,
    "!=": lambda a, b: a != b,
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
}


class ConditionError(ValueError):
    """Raised when an attribute condition cannot be parsed."""


class Condition:
    __slots__ = ()

    def holds(self, attrs: Mapping[str, Any]) -> bool:
        raise NotImplementedError

    def __and__(self, other: "Condition") -> "And":
        return And(self, other)


@dataclass(frozen=True, slots=True)
class Always(Condition):
    def holds(self, attrs: Mapping[str, Any]) -> bool:
        return True


@dataclass(frozen=True, slots=True)
class Has(Condition):
    key: str

    def holds(self, attrs: Mapping[str, Any]) -> bool:
        return self.key in attrs


@dataclass(frozen=True, slots=True)
class Compare(Condition):
    key: str
    op: str
    value: Any

    def holds(self, attrs: Mapping[str, Any]) -> bool:
        if self.key not in attrs:
            return False
        return _OPS[self.op](attrs[self.key], self.value)


@dataclass(frozen=True, slots=True)
class InSet(Condition):
    key: str
    values: frozenset

    def holds(self, attrs: Mapping[str, Any]) -> bool:
        return self.key in attrs and attrs[self.key] in self.values


@dataclass(frozen=True, slots=True)
class And(Condition):
    parts: tuple[Condition, ...]

    def __init__(self, *parts: Condition) -> None:
        object.__setattr__(self, "parts", tuple(parts))

    def holds(self, attrs: Mapping[str, Any]) -> bool:
        return all(p.holds(attrs) for p in self.parts)


@dataclass(frozen=True, slots=True)
class Or(Condition):
    parts: tuple[Condition, ...]

    def __init__(self, *parts: Condition) -> None:
        object.__setattr__(self, "parts", tuple(parts))

    def holds(self, attrs: Mapping[str, Any]) -> bool:
        return any(p.holds(attrs) for p in self.parts)


@dataclass(frozen=True, slots=True)
class Not(Condition):
    part: Condition

    def holds(self, attrs: Mapping[str, Any]) -> bool:
        return not self.part.holds(attrs)


def parse_condition(spec: Any) -> Condition:
    """Convert a declarative condition specification into a :class:`Condition`.

    Accepted forms::

        None / {"all": [...]} / {"any": [...]} / {"not": <cond>}
        ("has", key)
        (key, op, value)           op in == != < <= > >=
        (key, "in", [values])
        {"op": "...", "key": "...", "value": ...}
    """
    if spec is None:
        return Always()
    if isinstance(spec, Condition):
        return spec
    if isinstance(spec, dict):
        if "all" in spec:
            return And(*(parse_condition(c) for c in spec["all"]))
        if "any" in spec:
            return Or(*(parse_condition(c) for c in spec["any"]))
        if "not" in spec:
            return Not(parse_condition(spec["not"]))
        try:
            key = spec["key"]
            op = spec["op"]
        except KeyError as exc:
            raise ConditionError(f"dict condition needs key/op: {spec!r}") from exc
        if op == "in":
            return InSet(key, frozenset(spec["values"]))
        return Compare(key, op, spec.get("value"))
    if isinstance(spec, (tuple, list)):
        if len(spec) == 2 and spec[0] == "has":
            return Has(str(spec[1]))
        if len(spec) == 3:
            key, op, value = spec
            if op == "in":
                if not isinstance(value, (tuple, list, set, frozenset)):
                    raise ConditionError("'in' needs an iterable of values")
                return InSet(str(key), frozenset(value))
            if op not in _OPS:
                raise ConditionError(f"unknown operator {op!r}")
            return Compare(str(key), op, value)
    raise ConditionError(f"cannot parse condition: {spec!r}")
