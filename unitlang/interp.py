"""Static dimensional analysis (runs before any evaluation) and the evaluator.

All numeric values are exact rationals (fractions.Fraction). Quantities are
stored in base SI units; absolute (affine) temperatures are stored in kelvin
and kept distinct from temperature differences at the type level.
"""

from __future__ import annotations

import operator
from dataclasses import dataclass
from fractions import Fraction

from .errors import DimError, EvalError
from .parser import Assign, Binary, Compare, Convert, ExprStmt, Number, Unary, Var
from .units import (
    DIMENSIONLESS,
    KELVIN,
    TEMPERATURE,
    Dim,
    Unit,
    describe_dim,
    dim_unit_name,
)


@dataclass(frozen=True)
class Type:
    dim: Dim | None  # None means boolean
    affine: bool = False


BOOL = Type(None)
NUMBER_TYPE = Type(DIMENSIONLESS)


def describe_type(t: Type) -> str:
    if t.dim is None:
        return "boolean"
    if t.affine:
        return "absolute temperature"
    return describe_dim(t.dim)


class Analyzer:
    """First pass: checks dimensions and resolves variables, no evaluation."""

    def __init__(self):
        self.env: dict[str, Type] = {}

    def analyze_stmt(self, stmt):
        if isinstance(stmt, Assign):
            self.env[stmt.name] = self.analyze(stmt.expr)
        else:
            self.analyze(stmt.expr)

    def analyze(self, node) -> Type:
        if isinstance(node, Number):
            if node.unit is None:
                return NUMBER_TYPE
            return Type(node.unit.dim, node.unit.affine)
        if isinstance(node, Var):
            t = self.env.get(node.name)
            if t is None:
                raise DimError(f"undefined variable '{node.name}'", node.span)
            return t
        if isinstance(node, Unary):
            return self.analyze(node.operand)
        if isinstance(node, Binary):
            lt = self.analyze(node.left)
            rt = self.analyze(node.right)
            self._check_not_bool(node, lt, rt)
            if node.op in "+-":
                return self._addsub(node, lt, rt)
            return self._muldiv(node, lt, rt)
        if isinstance(node, Compare):
            lt = self.analyze(node.left)
            rt = self.analyze(node.right)
            if lt.dim is None or rt.dim is None:
                raise DimError("cannot compare boolean values", node.op_span)
            if lt.affine != rt.affine or lt.dim != rt.dim:
                raise DimError(
                    f"cannot compare {describe_type(lt)} with {describe_type(rt)}",
                    node.op_span,
                )
            return BOOL
        if isinstance(node, Convert):
            t = self.analyze(node.expr)
            unit = node.unit
            if t.dim is None:
                raise DimError("cannot convert a boolean", node.unit_span)
            if t.affine != unit.affine or t.dim != unit.dim:
                raise DimError(
                    f"cannot convert {describe_type(t)} to unit "
                    f"'{unit.name}' ({describe_type(Type(unit.dim, unit.affine))})",
                    node.unit_span,
                )
            return t
        raise AssertionError(f"unknown node {node!r}")

    @staticmethod
    def _check_not_bool(node, lt: Type, rt: Type):
        if lt.dim is None or rt.dim is None:
            raise DimError("booleans cannot be used in arithmetic", node.op_span)

    @staticmethod
    def _addsub(node, lt: Type, rt: Type) -> Type:
        word = "add" if node.op == "+" else "subtract"
        if lt.affine or rt.affine:
            if lt.affine and rt.affine:
                if node.op == "+":
                    raise DimError(
                        "cannot add two absolute temperatures", node.op_span
                    )
                return Type(TEMPERATURE)
            if lt.affine:
                if rt.dim != TEMPERATURE:
                    raise DimError(
                        f"cannot {word} absolute temperature and "
                        f"{describe_type(rt)}",
                        node.op_span,
                    )
                return Type(TEMPERATURE, affine=True)
            if node.op == "-":
                raise DimError(
                    f"cannot subtract absolute temperature from "
                    f"{describe_type(lt)}",
                    node.op_span,
                )
            if lt.dim != TEMPERATURE:
                raise DimError(
                    f"cannot {word} {describe_type(lt)} and absolute temperature",
                    node.op_span,
                )
            return Type(TEMPERATURE, affine=True)
        if lt.dim != rt.dim:
            raise DimError(
                f"cannot {word} {describe_type(lt)} and {describe_type(rt)}",
                node.op_span,
            )
        return Type(lt.dim)

    @staticmethod
    def _muldiv(node, lt: Type, rt: Type) -> Type:
        if lt.affine or rt.affine:
            raise DimError(
                "absolute temperature cannot be multiplied or divided; "
                "use a temperature difference",
                node.op_span,
            )
        sign = 1 if node.op == "*" else -1
        return Type(lt.dim.combine(rt.dim, sign))


@dataclass
class Quantity:
    value: Fraction  # always in base SI units (kelvin for temperature)
    type: Type
    display: Unit | None = None


_COMPARE = {
    "==": operator.eq,
    "!=": operator.ne,
    "<": operator.lt,
    "<=": operator.le,
    ">": operator.gt,
    ">=": operator.ge,
}


class Evaluator:
    """Second pass: computes values. Runs only after analysis succeeds."""

    def __init__(self):
        self.env: dict[str, Quantity] = {}

    def eval_stmt(self, stmt):
        if isinstance(stmt, Assign):
            self.env[stmt.name] = self.eval(stmt.expr)
            return None
        return self.eval(stmt.expr)

    def eval(self, node):
        if isinstance(node, Number):
            if node.unit is None:
                return Quantity(node.value, NUMBER_TYPE)
            return Quantity(
                node.unit.to_base(node.value),
                Type(node.unit.dim, node.unit.affine),
                node.unit,
            )
        if isinstance(node, Var):
            return self.env[node.name]
        if isinstance(node, Unary):
            q = self.eval(node.operand)
            if q.type.affine:
                unit = q.display or KELVIN
                return Quantity(
                    unit.to_base(-unit.from_base(q.value)), q.type, unit
                )
            return Quantity(-q.value, q.type, q.display)
        if isinstance(node, Binary):
            return self._binary(node, self.eval(node.left), self.eval(node.right))
        if isinstance(node, Compare):
            left = self.eval(node.left)
            right = self.eval(node.right)
            return _COMPARE[node.op](left.value, right.value)
        if isinstance(node, Convert):
            q = self.eval(node.expr)
            return Quantity(q.value, q.type, node.unit)
        raise AssertionError(f"unknown node {node!r}")

    @staticmethod
    def _binary(node, left: Quantity, right: Quantity) -> Quantity:
        op = node.op
        lt, rt = left.type, right.type
        if op in "+-":
            sign = 1 if op == "+" else -1
            if lt.affine and rt.affine:
                return Quantity(left.value - right.value, Type(TEMPERATURE))
            if lt.affine:
                return Quantity(left.value + sign * right.value, lt, left.display)
            if rt.affine:
                return Quantity(left.value + right.value, rt, right.display)
            display = left.display if left.display is not None else right.display
            return Quantity(left.value + sign * right.value, lt, display)
        if op == "*":
            if lt.dim == DIMENSIONLESS:
                return Quantity(left.value * right.value, rt, right.display)
            if rt.dim == DIMENSIONLESS:
                return Quantity(left.value * right.value, lt, left.display)
            return Quantity(
                left.value * right.value, Type(lt.dim.combine(rt.dim, 1))
            )
        if right.value == 0:
            raise EvalError("division by zero", node.op_span)
        if rt.dim == DIMENSIONLESS:
            return Quantity(left.value / right.value, lt, left.display)
        return Quantity(left.value / right.value, Type(lt.dim.combine(rt.dim, -1)))


def format_fraction(value: Fraction) -> str:
    if value.denominator == 1:
        return str(value.numerator)
    den = value.denominator
    twos = 0
    while den % 2 == 0:
        den //= 2
        twos += 1
    fives = 0
    while den % 5 == 0:
        den //= 5
        fives += 1
    if den != 1:
        return f"{value.numerator}/{value.denominator}"
    k = max(twos, fives)
    num = value.numerator * (2 ** (k - twos)) * (5 ** (k - fives))
    sign = ""
    if num < 0:
        sign = "-"
        num = -num
    digits = str(num).rjust(k + 1, "0")
    return f"{sign}{digits[:-k]}.{digits[-k:]}"


def format_value(value) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    q = value
    if q.type.affine:
        unit = q.display or KELVIN
        return f"{format_fraction(unit.from_base(q.value))} {unit.name}"
    if q.type.dim == DIMENSIONLESS:
        return format_fraction(q.value)
    if q.display is not None:
        return f"{format_fraction(q.display.from_base(q.value))} {q.display.name}"
    return f"{format_fraction(q.value)} {dim_unit_name(q.type.dim)}"
