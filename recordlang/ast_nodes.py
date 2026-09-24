"""AST nodes and static types. Every node carries a UTF-8 byte-range span."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

Span = tuple[int, int]  # [start, end) UTF-8 byte offsets


# ---------- types ----------

class Type:
    """Base class for static types."""


@dataclass(frozen=True)
class TInt(Type):
    pass


@dataclass(frozen=True)
class TBool(Type):
    pass


@dataclass(frozen=True)
class TRecord(Type):
    # Canonical form: sorted by field name, so equality is order independent.
    fields: tuple[tuple[str, "Type"], ...]


@dataclass(frozen=True)
class TFn(Type):
    params: tuple[tuple["Type", str], ...]  # (parameter type, mode "ro"|"rw")
    ret: Type


def record_type(fields: dict[str, Type]) -> TRecord:
    return TRecord(tuple(sorted(fields.items())))


def show_type(t: Type) -> str:
    if isinstance(t, TInt):
        return "Int"
    if isinstance(t, TBool):
        return "Bool"
    if isinstance(t, TRecord):
        inner = ", ".join(f"{name}: {show_type(ft)}" for name, ft in t.fields)
        return "{" + inner + "}"
    if isinstance(t, TFn):
        params = ", ".join(f"{show_type(pt)} @{mode}" for pt, mode in t.params)
        return f"fn({params}) -> {show_type(t.ret)}"
    raise AssertionError(f"unknown type {t!r}")


# ---------- expressions ----------

@dataclass
class IntLit:
    value: int
    span: Span


@dataclass
class BoolLit:
    value: bool
    span: Span


@dataclass
class Var:
    name: str
    span: Span


@dataclass
class RecordLit:
    fields: list[tuple[str, Any, Span]]  # (field name, value expr, name span)
    span: Span


@dataclass
class FieldAccess:
    record: Any
    name: str
    name_span: Span
    span: Span


@dataclass
class Update:  # `record with name = value` -- requires the field to exist
    record: Any
    name: str
    name_span: Span
    value: Any
    span: Span


@dataclass
class Extend:  # `extend record with name = value` -- requires the field to be new
    record: Any
    name: str
    name_span: Span
    value: Any
    span: Span


@dataclass
class Let:
    name: str
    value: Any
    body: Any
    span: Span


@dataclass
class If:
    cond: Any
    then: Any
    other: Any
    span: Span


@dataclass
class UnaryOp:
    op: str
    operand: Any
    span: Span


@dataclass
class BinOp:
    op: str
    left: Any
    right: Any
    span: Span


@dataclass
class Param:
    name: str
    type: Type
    mode: str  # "ro" (read-only, covariant) or "rw" (writable, invariant)
    span: Span


@dataclass
class Fn:
    params: list[Param]
    ret: Type
    body: Any
    span: Span


@dataclass
class App:
    func: Any
    args: list[Any]
    span: Span


# ---------- statements ----------

@dataclass
class LetStmt:
    name: str
    name_span: Span
    expr: Any
    span: Span


@dataclass
class ExprStmt:
    expr: Any
    span: Span
