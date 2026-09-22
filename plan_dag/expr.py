"""Typed scalar expressions used by filter/project/join/aggregate plans.

The expression language is a closed, allow-listed algebraic data type:

* ``lit``     typed literal (type is required for NULL)
* ``col``     column reference by name
* ``bin``     comparison, arithmetic or logical binary operator
* ``not``     boolean negation (three-valued)
* ``isnull``  NULL test (never yields NULL)
* ``coalesce`` first non-NULL operand

Expressions are decoded from plain JSON data only; there is no string
parsing, no ``eval``/``exec``, and unknown tags are rejected.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Any, Callable

from .errors import (
    BindingError,
    DocumentFormatError,
    ExecutionError,
    TypeMismatchError,
)
from .types import (
    BOOL,
    DataType,
    FLOAT,
    INT,
    NULL,
    TEXT,
    check_literal,
    infer_literal_type,
    normalize_value,
)
from .util import as_str, parse_object

COMPARISON_OPS = frozenset({"=", "<>", "<", "<=", ">", ">="})
ORDERING_OPS = frozenset({"<", "<=", ">", ">="})
LOGICAL_OPS = frozenset({"and", "or"})
ARITHMETIC_OPS = frozenset({"+", "-", "*", "/"})
BINARY_OPS = COMPARISON_OPS | LOGICAL_OPS | ARITHMETIC_OPS


class BoundExpr(ABC):
    output_type: str

    @abstractmethod
    def eval(self, row: tuple):
        raise NotImplementedError


@dataclass
class BoundLiteral(BoundExpr):
    value: object
    output_type: str

    def eval(self, row: tuple):
        return self.value


@dataclass
class BoundColumn(BoundExpr):
    index: int
    output_type: str

    def eval(self, row: tuple):
        return row[self.index]


@dataclass
class BoundNot(BoundExpr):
    operand: BoundExpr
    output_type: str = BOOL

    def eval(self, row: tuple):
        value = self.operand.eval(row)
        if value is NULL:
            return NULL
        return not value


@dataclass
class BoundIsNull(BoundExpr):
    operand: BoundExpr
    negated: bool
    output_type: str = BOOL

    def eval(self, row: tuple):
        value = self.operand.eval(row)
        result = value is NULL
        return not result if self.negated else result


@dataclass
class BoundCoalesce(BoundExpr):
    operands: tuple[BoundExpr, ...]
    output_type: str

    def eval(self, row: tuple):
        for operand in self.operands:
            value = operand.eval(row)
            if value is not NULL:
                return value
        return NULL


@dataclass
class BoundBinOp(BoundExpr):
    op: str
    left: BoundExpr
    right: BoundExpr
    output_type: str
    impl: Callable[[object, object], object]

    def eval(self, row: tuple):
        return self.impl(self.left.eval(row), self.right.eval(row))


def _logic_and(left, right):
    if left is False or right is False:
        return False
    if left is NULL or right is NULL:
        return NULL
    if left is True and right is True:
        return True
    raise ExecutionError(f"invalid boolean operand {left!r}, {right!r}")


def _logic_or(left, right):
    if left is True or right is True:
        return True
    if left is NULL or right is NULL:
        return NULL
    if left is False and right is False:
        return False
    raise ExecutionError(f"invalid boolean operand {left!r}, {right!r}")


def _compare(op: str) -> Callable[[object, object], object]:
    table = {
        "=": lambda a, b: a == b,
        "<>": lambda a, b: a != b,
        "<": lambda a, b: a < b,
        "<=": lambda a, b: a <= b,
        ">": lambda a, b: a > b,
        ">=": lambda a, b: a >= b,
    }
    python_op = table[op]

    def impl(left, right):
        if left is NULL or right is NULL:
            return NULL
        return bool(python_op(left, right))

    return impl


def _arithmetic(op: str) -> Callable[[object, object], object]:
    def impl(left, right):
        if left is NULL or right is NULL:
            return NULL
        if op == "+":
            return left + right
        if op == "-":
            return left - right
        if op == "*":
            return left * right
        if right == 0:
            raise ExecutionError("division by zero")
        return left / right

    return impl


class Expr(ABC):
    tag: str = ""

    @abstractmethod
    def to_dict(self) -> dict[str, Any]:
        raise NotImplementedError

    @abstractmethod
    def bind(self, schema, where: str) -> BoundExpr:
        raise NotImplementedError


@dataclass(frozen=True)
class LiteralExpr(Expr):
    value: object
    data_type: str
    tag: str = "lit"

    def to_dict(self) -> dict[str, Any]:
        return {"e": self.tag, "value": _json_value(self.value), "type": self.data_type}

    def bind(self, schema, where: str) -> BoundExpr:
        if self.data_type not in DataType.ALL:
            raise TypeMismatchError(f"{where}: unknown literal type {self.data_type!r}")
        check_literal(self.value, self.data_type, where)
        return BoundLiteral(self.value, self.data_type)


@dataclass(frozen=True)
class ColumnExpr(Expr):
    name: str
    tag: str = "col"

    def to_dict(self) -> dict[str, Any]:
        return {"e": self.tag, "name": self.name}

    def bind(self, schema, where: str) -> BoundExpr:
        index = schema.index_of(self.name)
        if index == -1:
            raise BindingError(f"{where}: unknown column {self.name!r}")
        if index == -2:
            raise BindingError(f"{where}: ambiguous column {self.name!r}")
        return BoundColumn(index, schema.columns[index].data_type)


@dataclass(frozen=True)
class BinOpExpr(Expr):
    op: str
    left: Expr
    right: Expr
    tag: str = "bin"

    def to_dict(self) -> dict[str, Any]:
        return {
            "e": self.tag,
            "op": self.op,
            "left": self.left.to_dict(),
            "right": self.right.to_dict(),
        }

    def bind(self, schema, where: str) -> BoundExpr:
        left = self.left.bind(schema, f"{where}.left")
        right = self.right.bind(schema, f"{where}.right")
        left_type, right_type = left.output_type, right.output_type
        op = self.op
        if op in COMPARISON_OPS:
            if left_type != right_type:
                raise TypeMismatchError(
                    f"{where}: cannot compare {left_type} with {right_type}"
                )
            if op in ORDERING_OPS and left_type == BOOL:
                raise TypeMismatchError(f"{where}: boolean values have no ordering ({op})")
            return BoundBinOp(op, left, right, BOOL, _compare(op))
        if op in LOGICAL_OPS:
            if left_type != BOOL or right_type != BOOL:
                raise TypeMismatchError(
                    f"{where}: operator {op!r} requires BOOLEAN operands, "
                    f"got {left_type} and {right_type}"
                )
            impl = _logic_and if op == "and" else _logic_or
            return BoundBinOp(op, left, right, BOOL, impl)
        if op in ARITHMETIC_OPS:
            if left_type not in (INT, FLOAT) or left_type != right_type:
                raise TypeMismatchError(
                    f"{where}: operator {op!r} requires matching numeric "
                    f"operands, got {left_type} and {right_type}"
                )
            output_type = FLOAT if op == "/" else left_type
            return BoundBinOp(op, left, right, output_type, _arithmetic(op))
        raise DocumentFormatError(f"{where}: unknown binary operator {op!r}")


@dataclass(frozen=True)
class NotExpr(Expr):
    operand: Expr
    tag: str = "not"

    def to_dict(self) -> dict[str, Any]:
        return {"e": self.tag, "operand": self.operand.to_dict()}

    def bind(self, schema, where: str) -> BoundExpr:
        bound = self.operand.bind(schema, f"{where}.operand")
        if bound.output_type != BOOL:
            raise TypeMismatchError(
                f"{where}: NOT requires BOOLEAN, got {bound.output_type}"
            )
        return BoundNot(bound)


@dataclass(frozen=True)
class IsNullExpr(Expr):
    operand: Expr
    negated: bool = False
    tag: str = "isnull"

    def to_dict(self) -> dict[str, Any]:
        result = {"e": self.tag, "operand": self.operand.to_dict()}
        if self.negated:
            result["negated"] = True
        return result

    def bind(self, schema, where: str) -> BoundExpr:
        bound = self.operand.bind(schema, f"{where}.operand")
        return BoundIsNull(bound, self.negated)


@dataclass(frozen=True)
class CoalesceExpr(Expr):
    operands: tuple[Expr, ...]
    tag: str = "coalesce"

    def to_dict(self) -> dict[str, Any]:
        return {"e": self.tag, "operands": [item.to_dict() for item in self.operands]}

    def bind(self, schema, where: str) -> BoundExpr:
        if not self.operands:
            raise DocumentFormatError(f"{where}: coalesce needs at least one operand")
        bound = tuple(item.bind(schema, f"{where}.operand") for item in self.operands)
        first_type = bound[0].output_type
        for item in bound[1:]:
            if item.output_type != first_type:
                raise TypeMismatchError(
                    f"{where}: coalesce mixes {first_type} and {item.output_type}"
                )
        return BoundCoalesce(bound, first_type)


def _json_value(value):
    return None if value is NULL else value


_EXPR_TAGS: dict[str, Callable[..., Expr]] = {}


def _register(cls: Callable[..., Expr]) -> Callable[..., Expr]:
    _EXPR_TAGS[cls.tag] = cls
    return cls


for _cls in (LiteralExpr, ColumnExpr, BinOpExpr, NotExpr, IsNullExpr, CoalesceExpr):
    _register(_cls)


def expr_from_dict(data: Any, where: str = "expression") -> Expr:
    if not isinstance(data, dict):
        raise DocumentFormatError(f"{where}: expected an object")
    tag = as_str(data["e"], f"{where}.e")

    if tag == "lit":
        obj = parse_object(data, where, required=("e", "value", "type"))
        data_type = as_str(obj["type"], f"{where}.type")
        value = normalize_value(obj["value"])
        return LiteralExpr(value, data_type)
    if tag == "col":
        obj = parse_object(data, where, required=("e", "name"))
        return ColumnExpr(as_str(obj["name"], f"{where}.name"))
    if tag == "bin":
        obj = parse_object(data, where, required=("e", "op", "left", "right"))
        op = as_str(obj["op"], f"{where}.op")
        if op not in BINARY_OPS:
            raise DocumentFormatError(f"{where}: unknown binary operator {op!r}")
        return BinOpExpr(
            op,
            expr_from_dict(obj["left"], f"{where}.left"),
            expr_from_dict(obj["right"], f"{where}.right"),
        )
    if tag == "not":
        obj = parse_object(data, where, required=("e", "operand"))
        return NotExpr(expr_from_dict(obj["operand"], f"{where}.operand"))
    if tag == "isnull":
        obj = parse_object(
            data, where, required=("e", "operand"), optional=("negated",)
        )
        negated = bool(obj.get("negated", False))
        if "negated" in obj and not isinstance(obj["negated"], bool):
            raise DocumentFormatError(f"{where}.negated: expected bool")
        return IsNullExpr(
            expr_from_dict(obj["operand"], f"{where}.operand"), negated
        )
    obj = parse_object(data, where, required=("e", "operands"))
    operands = obj["operands"]
    if not isinstance(operands, list) or not operands:
        raise DocumentFormatError(f"{where}.operands: expected a non-empty list")
    return CoalesceExpr(
        tuple(expr_from_dict(item, f"{where}.operand") for item in operands)
    )


def col(name: str) -> ColumnExpr:
    return ColumnExpr(name)


def lit(value, data_type: str | None = None) -> LiteralExpr:
    value = normalize_value(value)
    if data_type is None:
        data_type = infer_literal_type(value)
    return LiteralExpr(value, data_type)


def binop(op: str, left: Expr, right: Expr) -> BinOpExpr:
    if op not in BINARY_OPS:
        raise DocumentFormatError(f"unknown binary operator {op!r}")
    return BinOpExpr(op, left, right)


def coalesce(*operands: Expr) -> CoalesceExpr:
    return CoalesceExpr(tuple(operands))
