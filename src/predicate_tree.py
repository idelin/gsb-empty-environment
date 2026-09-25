from __future__ import annotations

import math
from dataclasses import dataclass
from enum import Enum
from typing import Any, Iterable, Mapping


class ColumnType(str, Enum):
    INTEGER = "integer"
    FLOAT = "float"
    BOOLEAN = "boolean"
    TEXT = "text"


class TriBool(str, Enum):
    TRUE = "true"
    FALSE = "false"
    UNKNOWN = "unknown"

    def __str__(self) -> str:
        return self.value


class PredicateError(ValueError):
    pass


class ComparisonOp(str, Enum):
    EQ = "="
    NE = "<>"
    LT = "<"
    LE = "<="
    GT = ">"
    GE = ">="


@dataclass(frozen=True)
class Column:
    name: str
    type: ColumnType


@dataclass(frozen=True)
class Literal:
    value: Any
    type: ColumnType | None = None


@dataclass(frozen=True)
class Comparison:
    column: Column
    op: ComparisonOp
    literal: Literal


@dataclass(frozen=True)
class IsNull:
    column: Column


@dataclass(frozen=True)
class IsNotNull:
    column: Column


@dataclass(frozen=True)
class Not:
    operand: "Predicate"


@dataclass(frozen=True)
class And:
    left: "Predicate"
    right: "Predicate"


@dataclass(frozen=True)
class Or:
    left: "Predicate"
    right: "Predicate"


Predicate = Comparison | IsNull | IsNotNull | Not | And | Or


@dataclass(frozen=True)
class ColumnStats:
    null_count: int
    nan_count: int = 0
    value_count: int = 0
    minimum: Any = None
    maximum: Any = None

    def __post_init__(self) -> None:
        if self.null_count < 0 or self.nan_count < 0 or self.value_count < 0:
            raise PredicateError("counts cannot be negative")
        if self.nan_count > 0 and (
            isinstance(self.minimum, float) and math.isnan(self.minimum)
            or isinstance(self.maximum, float) and math.isnan(self.maximum)
        ):
            raise PredicateError("NaN must be reported in nan_count, not min/max")
        if self.value_count > 0 and (self.minimum is None or self.maximum is None):
            raise PredicateError("value_count requires minimum and maximum")
        if self.value_count == 0 and (self.minimum is not None or self.maximum is not None):
            raise PredicateError("minimum and maximum require value_count")
        for bound in (self.minimum, self.maximum):
            if isinstance(bound, float) and math.isnan(bound):
                raise PredicateError("NaN must be reported in nan_count, not min/max")


@dataclass(frozen=True)
class Block:
    row_count: int
    stats: Mapping[Column, ColumnStats]

    @classmethod
    def from_batch(cls, columns: Iterable[Column], rows: Iterable[Mapping[str, Any]]) -> "Block":
        column_list = list(columns)
        if len({column.name for column in column_list}) != len(column_list):
            raise PredicateError("duplicate column names are not allowed")
        accum: dict[Column, list[Any]] = {
            column: [0, 0, 0, None, None] for column in column_list
        }
        row_count = 0
        for row in rows:
            row_count += 1
            for column in column_list:
                if column.name not in row:
                    raise PredicateError(f"missing column {column.name!r}")
                if isinstance(row, Mapping) and len(row) != len(column_list):
                    unexpected = set(row) - {column.name for column in column_list}
                    if unexpected:
                        raise PredicateError(f"unexpected columns: {sorted(unexpected)}")
                value = row[column.name]
                entry = accum[column]
                if value is None:
                    entry[0] += 1
                    continue
                _validate_value(value, column.type)
                if column.type is ColumnType.FLOAT and math.isnan(value):
                    entry[1] += 1
                    continue
                entry[2] += 1
                entry[3] = value if entry[3] is None else min(entry[3], value)
                entry[4] = value if entry[4] is None else max(entry[4], value)
        stats = {
            column: ColumnStats(entry[0], entry[1], entry[2], entry[3], entry[4])
            for column, entry in accum.items()
        }
        return cls(row_count, stats)


def _validate_value(value: Any, column_type: ColumnType) -> None:
    if column_type is ColumnType.INTEGER:
        if isinstance(value, bool) or not isinstance(value, int):
            raise PredicateError(f"expected integer, got {value!r}")
    elif column_type is ColumnType.FLOAT:
        if isinstance(value, bool) or not isinstance(value, float):
            raise PredicateError(f"expected float, got {value!r}")
    elif column_type is ColumnType.BOOLEAN:
        if not isinstance(value, bool):
            raise PredicateError(f"expected boolean, got {value!r}")
    elif column_type is ColumnType.TEXT:
        if not isinstance(value, str):
            raise PredicateError(f"expected text, got {value!r}")


def typecheck_predicate(predicate: Predicate) -> None:
    _typecheck(predicate)


def _typecheck(predicate: Predicate) -> None:
    if isinstance(predicate, (IsNull, IsNotNull)):
        return
    if isinstance(predicate, Not):
        _typecheck(predicate.operand)
        return
    if isinstance(predicate, (And, Or)):
        _typecheck(predicate.left)
        _typecheck(predicate.right)
        return
    if isinstance(predicate, Comparison):
        if not isinstance(predicate.literal, Literal):
            raise PredicateError("comparison literal must be a Literal value")
        literal_type = predicate.literal.type
        if literal_type is None:
            literal_type = _infer_literal_type(predicate.literal.value)
        if literal_type is not predicate.column.type:
            raise PredicateError("literal type does not match column type")
        value = predicate.literal.value
        if value is None:
            raise PredicateError("comparisons require a non-null literal")
        if literal_type is ColumnType.BOOLEAN and predicate.op not in (
            ComparisonOp.EQ,
            ComparisonOp.NE,
        ):
            raise PredicateError("booleans support only = and <>")
        _validate_value(value, literal_type)
        return
    raise PredicateError("unsupported predicate")


def _infer_literal_type(value: Any) -> ColumnType:
    if value is None:
        raise PredicateError("NULL literals require an explicit type in comparisons")
    if isinstance(value, bool):
        return ColumnType.BOOLEAN
    if isinstance(value, int):
        return ColumnType.INTEGER
    if isinstance(value, float):
        return ColumnType.FLOAT
    if isinstance(value, str):
        return ColumnType.TEXT
    raise PredicateError(f"unsupported literal {value!r}")


def evaluate_row(predicate: Predicate, row: Mapping[str, Any]) -> TriBool:
    typecheck_predicate(predicate)
    return _eval(predicate, row)


def _eval(predicate: Predicate, row: Mapping[str, Any]) -> TriBool:
    if isinstance(predicate, IsNull):
        return TriBool.TRUE if row[predicate.column.name] is None else TriBool.FALSE
    if isinstance(predicate, IsNotNull):
        return TriBool.FALSE if row[predicate.column.name] is None else TriBool.TRUE
    if isinstance(predicate, Not):
        return not3(_eval(predicate.operand, row))
    if isinstance(predicate, And):
        return and3(_eval(predicate.left, row), _eval(predicate.right, row))
    if isinstance(predicate, Or):
        return or3(_eval(predicate.left, row), _eval(predicate.right, row))

    value = row[predicate.column.name]
    if value is None:
        return TriBool.UNKNOWN
    _validate_value(value, predicate.column.type)
    literal = predicate.literal.value
    if isinstance(value, float) and math.isnan(value):
        if predicate.op is ComparisonOp.NE:
            return TriBool.TRUE
        return TriBool.FALSE
    if isinstance(literal, float) and math.isnan(literal):
        if predicate.op is ComparisonOp.NE:
            return TriBool.TRUE
        return TriBool.FALSE

    if predicate.op is ComparisonOp.EQ:
        return TriBool.TRUE if value == literal else TriBool.FALSE
    if predicate.op is ComparisonOp.NE:
        return TriBool.TRUE if value != literal else TriBool.FALSE
    if predicate.op is ComparisonOp.LT:
        return TriBool.TRUE if value < literal else TriBool.FALSE
    if predicate.op is ComparisonOp.LE:
        return TriBool.TRUE if value <= literal else TriBool.FALSE
    if predicate.op is ComparisonOp.GT:
        return TriBool.TRUE if value > literal else TriBool.FALSE
    return TriBool.TRUE if value >= literal else TriBool.FALSE


def not3(value: TriBool) -> TriBool:
    if value is TriBool.TRUE:
        return TriBool.FALSE
    if value is TriBool.FALSE:
        return TriBool.TRUE
    return TriBool.UNKNOWN


def and3(left: TriBool, right: TriBool) -> TriBool:
    values = {left, right}
    if TriBool.FALSE in values:
        return TriBool.FALSE
    if TriBool.UNKNOWN in values:
        return TriBool.UNKNOWN
    return TriBool.TRUE


def or3(left: TriBool, right: TriBool) -> TriBool:
    values = {left, right}
    if TriBool.TRUE in values:
        return TriBool.TRUE
    if TriBool.UNKNOWN in values:
        return TriBool.UNKNOWN
    return TriBool.FALSE
