from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Any


NULL = None


class Tri(Enum):
    TRUE = "true"
    FALSE = "false"
    UNKNOWN = "unknown"

    def __bool__(self) -> bool:
        return self is Tri.TRUE


@dataclass(frozen=True)
class Column:
    name: str


@dataclass(frozen=True)
class Literal:
    value: Any


@dataclass(frozen=True)
class Param:
    name: str


@dataclass(frozen=True)
class Comparison:
    left: Any
    operator: str
    right: Any


@dataclass(frozen=True)
class And:
    left: Any
    right: Any


@dataclass(frozen=True)
class Or:
    left: Any
    right: Any


@dataclass(frozen=True)
class Not:
    operand: Any


@dataclass(frozen=True)
class StaticBool:
    value: Tri


@dataclass(frozen=True)
class Select:
    table: str
    predicate: Any = StaticBool(Tri.TRUE)
    outputs: tuple[Column, ...] = ()


@dataclass(frozen=True)
class UnionAll:
    left: Any
    right: Any


def node_key(node: Any) -> tuple:
    if isinstance(node, Column):
        return ("col", node.name)
    if isinstance(node, Literal):
        return ("lit", type(node.value).__name__, repr(node.value))
    if isinstance(node, Param):
        return ("param", node.name)
    if isinstance(node, StaticBool):
        return ("static", node.value.value)
    if isinstance(node, Comparison):
        return ("cmp", node_key(node.left), node.operator, node_key(node.right))
    if isinstance(node, And):
        return ("and", node_key(node.left), node_key(node.right))
    if isinstance(node, Or):
        return ("or", node_key(node.left), node_key(node.right))
    if isinstance(node, Not):
        return ("not", node_key(node.operand))
    if isinstance(node, Select):
        return ("select", node.table, tuple(node_key(c) for c in node.outputs), node_key(node.predicate))
    if isinstance(node, UnionAll):
        return ("union_all", node_key(node.left), node_key(node.right))
    raise TypeError(f"unsupported query node: {type(node)!r}")
