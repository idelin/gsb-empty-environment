from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable

from .relations import Column, NULL, Row


class Expression:
    def references(self) -> frozenset[Column]:
        raise NotImplementedError

    def evaluate(self, row: Row) -> Any:
        raise NotImplementedError


@dataclass(frozen=True)
class Literal(Expression):
    value: Any

    def references(self) -> frozenset[Column]:
        return frozenset()

    def evaluate(self, row: Row) -> Any:
        return self.value


@dataclass(frozen=True)
class ColRef(Expression):
    column: Column

    def references(self) -> frozenset[Column]:
        return frozenset({self.column})

    def evaluate(self, row: Row) -> Any:
        return row[self.column]


@dataclass(frozen=True)
class IsNull(Expression):
    value: Expression

    def references(self) -> frozenset[Column]:
        return self.value.references()

    def evaluate(self, row: Row) -> bool:
        return self.value.evaluate(row) is None


@dataclass(frozen=True)
class And(Expression):
    left: Expression
    right: Expression

    def references(self) -> frozenset[Column]:
        return self.left.references() | self.right.references()

    def evaluate(self, row: Row) -> Any:
        left = self.left.evaluate(row)
        right = self.right.evaluate(row)
        if left is False or right is False:
            return False
        if left is None or right is None:
            return NULL
        return bool(left and right)


@dataclass(frozen=True)
class Or(Expression):
    left: Expression
    right: Expression

    def references(self) -> frozenset[Column]:
        return self.left.references() | self.right.references()

    def evaluate(self, row: Row) -> Any:
        left = self.left.evaluate(row)
        right = self.right.evaluate(row)
        if left is True or right is True:
            return True
        if left is None or right is None:
            return NULL
        return bool(left or right)


@dataclass(frozen=True)
class Not(Expression):
    value: Expression

    def references(self) -> frozenset[Column]:
        return self.value.references()

    def evaluate(self, row: Row) -> Any:
        value = self.value.evaluate(row)
        if value is None:
            return NULL
        return not value


COMPARATORS: dict[str, Callable[[Any, Any], Any]] = {
    "=": lambda left, right: None if left is None or right is None else left == right,
    "<>": lambda left, right: None if left is None or right is None else left != right,
    "<": lambda left, right: None if left is None or right is None else left < right,
    "<=": lambda left, right: None if left is None or right is None else left <= right,
    ">": lambda left, right: None if left is None or right is None else left > right,
    ">=": lambda left, right: None if left is None or right is None else left >= right,
}


@dataclass(frozen=True)
class Comparison(Expression):
    operator: str
    left: Expression
    right: Expression

    def __post_init__(self) -> None:
        if self.operator not in COMPARATORS:
            raise ValueError(f"unsupported comparison: {self.operator}")

    def references(self) -> frozenset[Column]:
        return self.left.references() | self.right.references()

    def evaluate(self, row: Row) -> Any:
        return COMPARATORS[self.operator](self.left.evaluate(row), self.right.evaluate(row))


def split_conjunction(expression: Expression) -> tuple[Expression, ...]:
    if isinstance(expression, And):
        return split_conjunction(expression.left) + split_conjunction(expression.right)
    return (expression,)


def combine_conjunction(expressions: tuple[Expression, ...]) -> Expression | None:
    if not expressions:
        return None
    result = expressions[0]
    for expression in expressions[1:]:
        result = And(result, expression)
    return result


def is_null_rejecting(expression: Expression, nullable_columns: frozenset[Column]) -> bool:
    """Return True only when the predicate cannot be true after those columns become NULL."""
    if isinstance(expression, IsNull):
        return False
    if isinstance(expression, Comparison):
        return bool(expression.references() & nullable_columns)
    if isinstance(expression, And):
        return is_null_rejecting(expression.left, nullable_columns) or is_null_rejecting(
            expression.right, nullable_columns
        )
    if isinstance(expression, Or):
        return is_null_rejecting(expression.left, nullable_columns) and is_null_rejecting(
            expression.right, nullable_columns
        )
    return False
