"""Typed, vectorized execution plan.

A plan is a tree of :class:`Node` objects.  Every node knows its result
:class:`~vector_expr.types.Type` (assigned at compile time) and evaluates
to a *vector*: a Python list of length ``batch.num_rows`` where ``None``
marks a null slot.

Evaluation is mask-based.  ``node.eval(batch, mask)`` computes values
only for rows where ``mask[i]`` is true; other slots are ``None``.
``IfNode`` exploits this to get short-circuit semantics in a vectorized
engine: the then-branch is evaluated only on rows where the condition
is TRUE, the else-branch only on rows where it is FALSE, so an error
(e.g. division by zero) in an unselected branch is never triggered.
"""

from __future__ import annotations

import math

from .batch import Batch, Column
from .errors import (
    CastError,
    DivisionByZeroError,
    EvaluationError,
    IntegerOverflowError,
)
from .types import INT64_MAX, INT64_MIN, Type, check_int64

Vector = list  # list of values; None == null


def _int_divmod(a: int, b: int, row: int) -> tuple[int, int]:
    """Truncating (C-style) integer division and remainder."""
    if b == 0:
        raise DivisionByZeroError(row, f"integer division by zero ({a} / 0)")
    if a == INT64_MIN and b == -1:
        raise IntegerOverflowError(row, f"{a} / -1 overflows INT64")
    q = abs(a) // abs(b)
    if (a < 0) != (b < 0):
        q = -q
    return q, a - q * b


def _float_div(a: float, b: float) -> float:
    """IEEE-754 division: x/0 -> +-inf, 0/0 -> NaN (no exceptions)."""
    if math.isnan(a) or math.isnan(b):
        return math.nan
    if b == 0.0:
        if a == 0.0:
            return math.nan
        return math.copysign(math.inf, a) * math.copysign(1.0, b)
    return a / b


class Node:
    """A typed plan node."""

    type: Type

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        raise NotImplementedError


class LiteralNode(Node):
    def __init__(self, value, type_: Type):
        self.value = value
        self.type = type_

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        return [self.value if m else None for m in mask]


class NullNode(Node):
    def __init__(self, type_: Type):
        self.type = type_

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        return [None] * batch.num_rows


class ColumnNode(Node):
    def __init__(self, name: str, type_: Type):
        self.name = name
        self.type = type_

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        values = batch.column(self.name).values
        return [values[i] if mask[i] else None for i in range(batch.num_rows)]


class NegateNode(Node):
    def __init__(self, operand: Node):
        self.operand = operand
        self.type = operand.type

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        src = self.operand.eval(batch, mask)
        out: Vector = [None] * batch.num_rows
        for i in range(batch.num_rows):
            if not mask[i] or src[i] is None:
                continue
            v = -src[i]
            if self.type is Type.INT64 and not check_int64(v):
                raise IntegerOverflowError(i, f"-({src[i]}) overflows INT64")
            out[i] = v
        return out


class NotNode(Node):
    def __init__(self, operand: Node):
        self.operand = operand
        self.type = Type.BOOL

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        src = self.operand.eval(batch, mask)
        return [
            (not src[i]) if (mask[i] and src[i] is not None) else None
            for i in range(batch.num_rows)
        ]


_ARITH_OPS = ("+", "-", "*", "/", "%")


class ArithNode(Node):
    """Binary arithmetic; operands share the node's numeric type."""

    def __init__(self, op: str, left: Node, right: Node, type_: Type):
        self.op = op
        self.left = left
        self.right = right
        self.type = type_

    def _int_op(self, a: int, b: int, row: int) -> int:
        if self.op == "+":
            r = a + b
        elif self.op == "-":
            r = a - b
        elif self.op == "*":
            r = a * b
        elif self.op == "/":
            r = _int_divmod(a, b, row)[0]
        else:  # '%'
            r = _int_divmod(a, b, row)[1]
        if not check_int64(r):
            raise IntegerOverflowError(
                row, f"{a} {self.op} {b} = {r} overflows INT64"
            )
        return r

    def _float_op(self, a: float, b: float) -> float:
        if self.op == "+":
            return a + b
        if self.op == "-":
            return a - b
        if self.op == "*":
            return a * b
        if self.op == "/":
            return _float_div(a, b)
        # '%': fmod keeps the sign of the dividend; x % 0 -> NaN
        if b == 0.0 or math.isnan(a) or math.isnan(b):
            return math.nan
        return math.fmod(a, b)

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        left = self.left.eval(batch, mask)
        right = self.right.eval(batch, mask)
        out: Vector = [None] * batch.num_rows
        is_int = self.type is Type.INT64
        for i in range(batch.num_rows):
            if not mask[i]:
                continue
            a, b = left[i], right[i]
            if a is None or b is None:
                continue  # null propagates
            out[i] = self._int_op(a, b, i) if is_int else self._float_op(a, b)
        return out


_COMPARE_OPS = ("=", "!=", "<", "<=", ">", ">=")


class CompareNode(Node):
    """Comparison producing BOOL; any null operand yields UNKNOWN (null)."""

    def __init__(self, op: str, left: Node, right: Node):
        self.op = op
        self.left = left
        self.right = right
        self.type = Type.BOOL

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        left = self.left.eval(batch, mask)
        right = self.right.eval(batch, mask)
        out: Vector = [None] * batch.num_rows
        for i in range(batch.num_rows):
            if not mask[i]:
                continue
            a, b = left[i], right[i]
            if a is None or b is None:
                continue  # null comparison -> UNKNOWN, not false
            if self.op == "=":
                out[i] = a == b
            elif self.op == "!=":
                out[i] = a != b
            elif self.op == "<":
                out[i] = a < b
            elif self.op == "<=":
                out[i] = a <= b
            elif self.op == ">":
                out[i] = a > b
            else:
                out[i] = a >= b
        return out


class AndNode(Node):
    """Kleene AND: FALSE wins, else UNKNOWN wins, else TRUE."""

    type = Type.BOOL

    def __init__(self, left: Node, right: Node):
        self.left = left
        self.right = right

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        left = self.left.eval(batch, mask)
        right = self.right.eval(batch, mask)
        out: Vector = [None] * batch.num_rows
        for i in range(batch.num_rows):
            if not mask[i]:
                continue
            a, b = left[i], right[i]
            if a is False or b is False:
                out[i] = False
            elif a is None or b is None:
                out[i] = None
            else:
                out[i] = True
        return out


class OrNode(Node):
    """Kleene OR: TRUE wins, else UNKNOWN wins, else FALSE."""

    type = Type.BOOL

    def __init__(self, left: Node, right: Node):
        self.left = left
        self.right = right

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        left = self.left.eval(batch, mask)
        right = self.right.eval(batch, mask)
        out: Vector = [None] * batch.num_rows
        for i in range(batch.num_rows):
            if not mask[i]:
                continue
            a, b = left[i], right[i]
            if a is True or b is True:
                out[i] = True
            elif a is None or b is None:
                out[i] = None
            else:
                out[i] = False
        return out


class IfNode(Node):
    """Vectorized IF with short-circuit semantics via row masks.

    The then-branch sees only rows where the condition is TRUE, the
    else-branch only rows where it is FALSE.  Rows with UNKNOWN (null)
    condition yield null.  Errors inside an unselected branch (e.g.
    ``if(false, 1/0, 7)``) are never raised.
    """

    def __init__(self, cond: Node, then: Node, otherwise: Node, type_: Type):
        self.cond = cond
        self.then = then
        self.otherwise = otherwise
        self.type = type_

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        n = batch.num_rows
        cond = self.cond.eval(batch, mask)
        then_mask = [mask[i] and cond[i] is True for i in range(n)]
        else_mask = [mask[i] and cond[i] is False for i in range(n)]
        out: Vector = [None] * n
        if any(then_mask):
            then_vals = self.then.eval(batch, then_mask)
            for i in range(n):
                if then_mask[i]:
                    out[i] = then_vals[i]
        if any(else_mask):
            else_vals = self.otherwise.eval(batch, else_mask)
            for i in range(n):
                if else_mask[i]:
                    out[i] = else_vals[i]
        return out


class CastNode(Node):
    """Explicit conversion between INT64 and FLOAT64.

    FLOAT64 -> INT64 truncates toward zero; NaN input is a CastError and
    out-of-range input is an IntegerOverflowError, both tagged with the
    offending row.
    """

    def __init__(self, operand: Node, target: Type):
        self.operand = operand
        self.type = target

    def eval(self, batch: Batch, mask: list[bool]) -> Vector:
        src = self.operand.eval(batch, mask)
        out: Vector = [None] * batch.num_rows
        for i in range(batch.num_rows):
            if not mask[i] or src[i] is None:
                continue
            v = src[i]
            if self.type is Type.FLOAT64:  # INT64 -> FLOAT64
                out[i] = float(v)
            else:  # FLOAT64 -> INT64
                if math.isnan(v):
                    raise CastError(i, "cannot CAST NaN to INT64")
                if math.isinf(v):
                    raise IntegerOverflowError(
                        i, f"cannot CAST {v} to INT64: outside range"
                    )
                t = math.trunc(v)
                if not check_int64(t):
                    raise IntegerOverflowError(
                        i, f"CAST({v} AS INT64) = {t} overflows INT64"
                    )
                out[i] = t
        return out


class Plan:
    """A compiled expression ready to execute against batches."""

    def __init__(self, root: Node, schema: dict[str, Type], source: str):
        self.root = root
        self.schema = dict(schema)  # columns the plan reads
        self.source = source

    @property
    def result_type(self) -> Type:
        return self.root.type

    def execute(self, batch: Batch) -> Column:
        """Run the plan over one batch and return the result column."""
        for name, type_ in self.schema.items():
            if name not in batch.columns:
                raise EvaluationError(
                    -1, f"batch is missing column {name!r} required by plan"
                )
            actual = batch.columns[name].type
            if actual is not type_:
                raise EvaluationError(
                    -1,
                    f"column {name!r} has type {actual}, plan expects {type_}",
                )
        mask = [True] * batch.num_rows
        return Column(self.root.type, self.root.eval(batch, mask))
