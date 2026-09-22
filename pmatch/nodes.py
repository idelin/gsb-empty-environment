"""AST nodes, runtime values and diagnostics.

All source positions are half-open UTF-8 byte ranges ``(start, end)``.
"""
from __future__ import annotations

from dataclasses import dataclass


# ---------------------------------------------------------------------------
# runtime values
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Variant:
    """A tagged-union value: ``Tag(arg1, arg2, ...)``."""
    tag: str
    args: tuple = ()


def lit_key(value):
    """Normalize a literal value into a hashable, type-exact key.

    Keeps ``True`` distinct from ``1`` (Python's ``==`` would conflate them).
    """
    if isinstance(value, bool):
        return ("lit", "bool", value)
    if isinstance(value, int):
        return ("lit", "int", value)
    if isinstance(value, str):
        return ("lit", "str", value)
    if value is None:
        return ("lit", "none", None)
    raise TypeError(f"not a literal value: {value!r}")


# ---------------------------------------------------------------------------
# diagnostics
# ---------------------------------------------------------------------------

@dataclass
class Diag:
    severity: str          # "error" | "warning"
    message: str
    span: tuple            # (start, end) UTF-8 byte offsets

    def __str__(self):
        return f"{self.severity}: {self.message} [bytes {self.span[0]}..{self.span[1]}]"


# ---------------------------------------------------------------------------
# patterns
# ---------------------------------------------------------------------------

@dataclass
class PWild:
    span: tuple


@dataclass
class PVar:
    name: str
    span: tuple


@dataclass
class PLit:
    value: object          # int | str | bool | None
    span: tuple


@dataclass
class PCtor:
    tag: str
    args: list
    span: tuple


@dataclass
class PTuple:
    items: list
    span: tuple


# ---------------------------------------------------------------------------
# expressions
# ---------------------------------------------------------------------------

@dataclass
class ELit:
    value: object
    span: tuple


@dataclass
class EVar:
    name: str
    span: tuple


@dataclass
class ECtor:
    tag: str
    args: list
    span: tuple


@dataclass
class ETuple:
    items: list
    span: tuple


@dataclass
class EBin:
    op: str
    left: object
    right: object
    span: tuple


@dataclass
class EUn:
    op: str
    operand: object
    span: tuple


@dataclass
class ECall:
    name: str
    args: list
    span: tuple


# ---------------------------------------------------------------------------
# top level
# ---------------------------------------------------------------------------

@dataclass
class Case:
    pattern: object
    guard: object          # expr or None
    body: object           # expr
    span: tuple


@dataclass
class Program:
    unions: dict           # union name -> [(tag, arity), ...]
    subject: object        # expr
    cases: list            # list[Case]
    subject_span: tuple
