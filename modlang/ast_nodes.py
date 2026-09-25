from dataclasses import dataclass


# ---- statements ----

@dataclass
class Program:
    stmts: list


@dataclass
class Import:
    names: list
    module: str
    start: int
    end: int


@dataclass
class ConstDecl:
    name: str
    init: object
    exported: bool
    mutable: bool
    start: int
    end: int


@dataclass
class FnDecl:
    name: str
    params: list
    body: list
    exported: bool
    start: int
    end: int


@dataclass
class IfStmt:
    cond: object
    then: list
    other: list
    start: int
    end: int


@dataclass
class WhileStmt:
    cond: object
    body: list
    start: int
    end: int


@dataclass
class ReturnStmt:
    value: object
    start: int
    end: int


@dataclass
class PrintStmt:
    args: list
    start: int
    end: int


@dataclass
class ExprStmt:
    expr: object
    start: int
    end: int


# ---- expressions ----

@dataclass
class Num:
    value: object
    start: int
    end: int


@dataclass
class Str:
    value: str
    start: int
    end: int


@dataclass
class Bool:
    value: bool
    start: int
    end: int


@dataclass
class Nil:
    start: int
    end: int


@dataclass
class Var:
    name: str
    start: int
    end: int


@dataclass
class Assign:
    name: str
    value: object
    start: int
    end: int


@dataclass
class Binary:
    op: str
    left: object
    right: object
    start: int
    end: int


@dataclass
class Unary:
    op: str
    operand: object
    start: int
    end: int


@dataclass
class Call:
    callee: object
    args: list
    start: int
    end: int
