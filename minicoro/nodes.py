"""AST node definitions. Every node carries a UTF-8 byte ``span``."""

from dataclasses import dataclass


@dataclass
class Program:
    defs: list
    span: tuple


@dataclass
class FnDef:
    name: str
    params: list
    body: list
    span: tuple


@dataclass
class Let:
    name: str
    expr: object
    span: tuple


@dataclass
class Assign:
    name: str
    expr: object
    span: tuple


@dataclass
class If:
    cond: object
    then_body: list
    else_body: object
    span: tuple


@dataclass
class While:
    cond: object
    body: list
    span: tuple


@dataclass
class Return:
    expr: object
    span: tuple


@dataclass
class Throw:
    expr: object
    span: tuple


@dataclass
class Try:
    body: list
    catch_name: object
    catch_body: object
    finally_body: object
    span: tuple


@dataclass
class ExprStmt:
    expr: object
    span: tuple


@dataclass
class Literal:
    value: object
    span: tuple


@dataclass
class Var:
    name: str
    span: tuple


@dataclass
class YieldExpr:
    expr: object
    span: tuple


@dataclass
class Unary:
    op: str
    operand: object
    span: tuple


@dataclass
class Binary:
    op: str
    left: object
    right: object
    span: tuple


@dataclass
class Call:
    func: object
    args: list
    span: tuple
