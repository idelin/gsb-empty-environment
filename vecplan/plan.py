"""Vectorized execution plans produced by the compiler."""
from __future__ import annotations

import math

from .errors import ErrorPolicy, ExecutionError
from .types import BOOL


class Vector:
    """One evaluated column: a logical type plus one value per row.

    null is None. NaN is an ordinary float value, never confused with null.
    """
    __slots__ = ("type", "values")

    def __init__(self, type, values):
        self.type = type
        self.values = values

    def __repr__(self):
        return f"Vector({self.type!r}, {self.values!r})"


class _RowError(Exception):
    """Internal per-row failure; converted according to the ErrorPolicy."""

    def __init__(self, kind):
        super().__init__(kind)
        self.kind = kind


def _apply_policy(errors, policy):
    if errors and policy is ErrorPolicy.STRICT:
        parts = []
        rows = []
        for kind, kind_rows in errors.items():
            parts.append(f"{kind} at row(s) {kind_rows}")
            rows.extend(kind_rows)
        raise ExecutionError("; ".join(parts), rows=sorted(rows))


def _trunc_div(a, b):
    q = abs(a) // abs(b)
    return q if (a < 0) == (b < 0) else -q


def _trunc_mod(a, b):
    return a - _trunc_div(a, b) * b


class Plan:
    """A compiled, type-checked vector plan.

    ``evaluate`` takes an optional ``selection``: the absolute batch row
    indices to produce values for. This is how IF evaluates a branch only
    on the rows that select it.
    """

    type = None

    def evaluate(self, batch, policy, selection=None):
        raise NotImplementedError

    def run(self, batch, policy=ErrorPolicy.STRICT):
        return self.evaluate(batch, policy)


class LiteralPlan(Plan):
    def __init__(self, value, type):
        self.value = value
        self.type = type

    def evaluate(self, batch, policy, selection=None):
        n = batch.num_rows if selection is None else len(selection)
        return Vector(self.type, [self.value] * n)


class ColumnPlan(Plan):
    def __init__(self, name, type):
        self.name = name
        self.type = type

    def evaluate(self, batch, policy, selection=None):
        column = batch.columns[self.name]
        if selection is None:
            return Vector(self.type, list(column))
        return Vector(self.type, [column[i] for i in selection])


class NotPlan(Plan):
    type = BOOL

    def __init__(self, operand):
        self.operand = operand

    def evaluate(self, batch, policy, selection=None):
        operand = self.operand.evaluate(batch, policy, selection)
        return Vector(BOOL, [None if v is None else not v for v in operand.values])


class IsNullPlan(Plan):
    type = BOOL

    def __init__(self, operand):
        self.operand = operand

    def evaluate(self, batch, policy, selection=None):
        operand = self.operand.evaluate(batch, policy, selection)
        return Vector(BOOL, [v is None for v in operand.values])


class BinaryPlan(Plan):
    def __init__(self, op, left, right, type):
        self.op = op
        self.left = left
        self.right = right
        self.type = type

    def evaluate(self, batch, policy, selection=None):
        left = self.left.evaluate(batch, policy, selection)
        right = self.right.evaluate(batch, policy, selection)
        n = len(left.values)
        rows = range(n) if selection is None else selection
        if self.op in ("and", "or"):
            return Vector(self.type, [self._logic(a, b)
                                      for a, b in zip(left.values, right.values)])
        out = [None] * n
        errors = {}
        for i in range(n):
            a, b = left.values[i], right.values[i]
            if a is None or b is None:
                continue  # null propagates
            try:
                out[i] = self._apply(a, b)
            except _RowError as exc:
                errors.setdefault(exc.kind, []).append(rows[i])
        _apply_policy(errors, policy)
        return Vector(self.type, out)

    def _logic(self, a, b):
        # Three-valued logic: null means unknown, never false.
        if self.op == "and":
            if a is False or b is False:
                return False
            if a is None or b is None:
                return None
            return True
        if a is True or b is True:
            return True
        if a is None or b is None:
            return None
        return False

    def _apply(self, a, b):
        op = self.op
        if op == "add":
            result = a + b
        elif op == "sub":
            result = a - b
        elif op == "mul":
            result = a * b
        elif op == "div":
            if b == 0:
                raise _RowError("division by zero")
            result = a / b if self.type.kind == "float" else _trunc_div(a, b)
        elif op == "mod":
            if b == 0:
                raise _RowError("division by zero")
            result = _trunc_mod(a, b)
        elif op == "eq":
            return a == b
        elif op == "ne":
            return a != b
        elif op == "lt":
            return a < b
        elif op == "le":
            return a <= b
        elif op == "gt":
            return a > b
        elif op == "ge":
            return a >= b
        else:
            raise AssertionError(f"unknown op {op}")
        if self.type.is_integer and not (self.type.min_value <= result <= self.type.max_value):
            raise _RowError("integer overflow")
        return result


class IfPlan(Plan):
    """Lazy conditional: each branch is evaluated only for the rows that select it."""

    def __init__(self, cond, then, otherwise, type):
        self.cond = cond
        self.then = then
        self.otherwise = otherwise
        self.type = type

    def evaluate(self, batch, policy, selection=None):
        cond = self.cond.evaluate(batch, policy, selection)
        n = len(cond.values)
        rows = range(n) if selection is None else selection
        out = [None] * n
        then_rows, then_pos, else_rows, else_pos = [], [], [], []
        for i in range(n):
            flag = cond.values[i]
            if flag is None:
                continue  # unknown condition -> unknown result, no branch runs
            if flag:
                then_rows.append(rows[i])
                then_pos.append(i)
            else:
                else_rows.append(rows[i])
                else_pos.append(i)
        if then_rows:
            taken = self.then.evaluate(batch, policy, then_rows)
            for pos, value in zip(then_pos, taken.values):
                out[pos] = value
        if else_rows:
            taken = self.otherwise.evaluate(batch, policy, else_rows)
            for pos, value in zip(else_pos, taken.values):
                out[pos] = value
        return Vector(self.type, out)


class CastPlan(Plan):
    def __init__(self, operand, target):
        self.operand = operand
        self.type = target

    def evaluate(self, batch, policy, selection=None):
        operand = self.operand.evaluate(batch, policy, selection)
        n = len(operand.values)
        rows = range(n) if selection is None else selection
        out = [None] * n
        errors = {}
        for i in range(n):
            value = operand.values[i]
            if value is None:
                continue
            try:
                out[i] = self._convert(value)
            except _RowError as exc:
                errors.setdefault(exc.kind, []).append(rows[i])
        _apply_policy(errors, policy)
        return Vector(self.type, out)

    def _convert(self, value):
        target = self.type
        if target.kind == "string":
            if isinstance(value, bool):
                return "true" if value else "false"
            if isinstance(value, float) and math.isnan(value):
                return "NaN"
            return str(value)
        if target.kind == "bool":
            if isinstance(value, str):
                text = value.strip().lower()
                if text == "true":
                    return True
                if text == "false":
                    return False
                raise _RowError("invalid cast")
            return bool(value)
        if target.kind == "float":
            if isinstance(value, str):
                try:
                    return float(value.strip())
                except ValueError:
                    raise _RowError("invalid cast") from None
            return float(value)
        # integer target
        if isinstance(value, str):
            try:
                result = int(value.strip())
            except ValueError:
                raise _RowError("invalid cast") from None
        elif isinstance(value, bool):
            result = 1 if value else 0
        elif isinstance(value, float):
            if math.isnan(value) or math.isinf(value):
                raise _RowError("invalid cast")
            result = math.trunc(value)
        else:
            result = value
        if not (target.min_value <= result <= target.max_value):
            raise _RowError("integer overflow")
        return result
