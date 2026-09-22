from __future__ import annotations

from dataclasses import dataclass
from typing import Optional, Union

from .errors import Span


@dataclass(frozen=True)
class BuiltinType:
    name: str
    span: Span = Span(0, 0)


@dataclass(frozen=True)
class UserType:
    name: str
    span: Span


@dataclass(frozen=True)
class TupleType:
    elements: list["PType"]
    span: Span = Span(0, 0)


PType = Union[BuiltinType, UserType, TupleType]


@dataclass(frozen=True)
class Constructor:
    name: str
    payload: list[PType]
    span: Span


@dataclass(frozen=True)
class UnionDecl:
    name: str
    constructors: list[Constructor]
    span: Span


@dataclass(frozen=True)
class IntLiteral:
    value: int
    span: Span


@dataclass(frozen=True)
class StringLiteral:
    value: str
    span: Span


@dataclass(frozen=True)
class BoolLiteral:
    value: bool
    span: Span


@dataclass(frozen=True)
class Variable:
    name: str
    span: Span


@dataclass(frozen=True)
class TupleExpr:
    elements: list["Expr"]
    span: Span


@dataclass(frozen=True)
class TagExpr:
    name: str
    payload: list["Expr"]
    span: Span


@dataclass(frozen=True)
class UnaryExpr:
    op: str
    operand: "Expr"
    span: Span


@dataclass(frozen=True)
class BinaryExpr:
    op: str
    left: "Expr"
    right: "Expr"
    span: Span


@dataclass(frozen=True)
class IfExpr:
    condition: "Expr"
    consequent: "Expr"
    alternate: "Expr"
    span: Span


@dataclass(frozen=True)
class WildcardPattern:
    span: Span


@dataclass(frozen=True)
class VariablePattern:
    name: str
    span: Span


@dataclass(frozen=True)
class LiteralPattern:
    value: Union[int, str, bool]
    span: Span


@dataclass(frozen=True)
class TuplePattern:
    elements: list["Pattern"]
    span: Span


@dataclass(frozen=True)
class TagPattern:
    name: str
    payload: list["Pattern"]
    span: Span


Pattern = Union[WildcardPattern, VariablePattern, LiteralPattern, TuplePattern, TagPattern]


@dataclass(frozen=True)
class Clause:
    pattern: Pattern
    guard: Optional["Expr"]
    body: "Expr"
    span: Span


@dataclass(frozen=True)
class MatchExpr:
    scrutinee: "Expr"
    scrutinee_type: PType
    clauses: list[Clause]
    span: Span


Expr = Union[
    IntLiteral,
    StringLiteral,
    BoolLiteral,
    Variable,
    TupleExpr,
    TagExpr,
    UnaryExpr,
    BinaryExpr,
    IfExpr,
    MatchExpr,
]


@dataclass(frozen=True)
class LetStmt:
    name: str
    annotation: Optional[PType]
    value: Expr
    span: Span


Stmt = Union[UnionDecl, LetStmt, Expr]


@dataclass(frozen=True)
class Program:
    statements: list[Stmt]
