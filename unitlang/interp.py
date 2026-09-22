"""解释执行：值一律用 Fraction 精确表示，量纲已在执行前由 checker 检查。"""
from __future__ import annotations

from dataclasses import dataclass
from decimal import Decimal, localcontext
from fractions import Fraction

from .errors import LangError
from .parser import BinOp, Convert, Literal, Unary, Var
from .units import (
    DELTA_OF,
    DIMENSIONLESS,
    TEMP_DIM,
    UNITS,
    Unit,
    convert_mag,
    dim_combine,
    dim_name,
    dim_scale,
    resolve_unit_expr,
    role_of,
)


@dataclass
class Value:
    mag: Fraction  # 以 self.unit 计的数值
    unit: Unit
    role: str  # 'none' | 'absolute' | 'delta'


@dataclass
class BoolVal:
    value: bool


def eval_expr(node, env: dict):
    if isinstance(node, Literal):
        if node.unit is None:
            return Value(node.mag, DIMENSIONLESS, "none")
        unit = resolve_unit_expr(node.unit.terms)
        return Value(node.mag, unit, role_of(unit))
    if isinstance(node, Var):
        return env[node.name]
    if isinstance(node, Unary):
        v = eval_expr(node.operand, env)
        return Value(-v.mag if node.op == "-" else v.mag, v.unit, v.role)
    if isinstance(node, Convert):
        v = eval_expr(node.expr, env)
        unit = resolve_unit_expr(node.unit.terms)
        return Value(convert_mag(v.mag, v.unit, unit), unit, role_of(unit))
    if isinstance(node, BinOp):
        return eval_binop(node, env)
    raise AssertionError(f"未知节点：{node!r}")


def eval_binop(node, env: dict):
    left = eval_expr(node.left, env)
    right = eval_expr(node.right, env)
    op = node.op
    if op in ("<", "<=", ">", ">=", "==", "!="):
        # 同量纲不同单位：先把右操作数换算到左操作数的单位再比较
        rmag = convert_mag(right.mag, right.unit, left.unit)
        a, b = left.mag, rmag
        result = {
            "<": a < b,
            "<=": a <= b,
            ">": a > b,
            ">=": a >= b,
            "==": a == b,
            "!=": a != b,
        }[op]
        return BoolVal(result)
    if op in ("+", "-"):
        return _add_sub(op, left, right)
    if op == "*":
        return _mul(left, right)
    # 除法：除零错误定位到运算符
    if right.mag == 0:
        raise LangError("除数为零", node.op_start, node.op_end)
    return _div(left, right)


def _add_sub(op: str, l: Value, r: Value) -> Value:
    sign = 1 if op == "+" else -1
    if l.role == "absolute" and r.role == "absolute":
        # 绝对温度相减 -> 温差，单位取左操作数对应的温差单位
        rmag = convert_mag(r.mag, r.unit, l.unit)
        return Value(l.mag - rmag, UNITS[DELTA_OF[l.unit.name]], "delta")
    if l.role == "absolute" and r.role == "delta":
        rmag = convert_mag(r.mag, r.unit, l.unit, as_delta=True)
        return Value(l.mag + sign * rmag, l.unit, "absolute")
    if l.role == "delta" and r.role == "absolute":  # 仅加法可达
        lmag = convert_mag(l.mag, l.unit, r.unit, as_delta=True)
        return Value(r.mag + lmag, r.unit, "absolute")
    rmag = convert_mag(r.mag, r.unit, l.unit)
    return Value(l.mag + sign * rmag, l.unit, l.role)


def _canonical(dim: tuple) -> Unit:
    return Unit(Fraction(1), Fraction(0), dim, "linear", dim_name(dim))


def _mul(l: Value, r: Value) -> Value:
    if not l.unit.dim:  # 标量 * 量：保留量的单位
        return Value(l.mag * r.mag, r.unit, r.role)
    if not r.unit.dim:
        return Value(l.mag * r.mag, l.unit, l.role)
    dim = dim_combine(l.unit.dim, r.unit.dim)
    base = (l.mag * l.unit.factor) * (r.mag * r.unit.factor)
    role = "delta" if (dim == TEMP_DIM and "delta" in (l.role, r.role)) else "none"
    return Value(base, _canonical(dim), role)


def _div(l: Value, r: Value) -> Value:
    if not r.unit.dim:
        return Value(l.mag / r.mag, l.unit, l.role)
    dim = dim_combine(l.unit.dim, r.unit.dim, sign=-1)
    base = (l.mag * l.unit.factor) / (r.mag * r.unit.factor)
    role = "delta" if (dim == TEMP_DIM and l.role == "delta") else "none"
    return Value(base, _canonical(dim), role)


# ---- 输出格式化 ----

def fmt_fraction(f: Fraction) -> str:
    """精确输出：整数直接打印；有限小数打十进制；否则打分数并附近似值。"""
    if f.denominator == 1:
        return str(f.numerator)
    d = f.denominator
    a = b = 0
    while d % 2 == 0:
        d //= 2
        a += 1
    while d % 5 == 0:
        d //= 5
        b += 1
    if d == 1:  # 分母只含 2 和 5，十进制有限
        int_digits = len(str(abs(f.numerator) // f.denominator))
        with localcontext() as ctx:
            ctx.prec = int_digits + max(a, b) + 2
            return format(Decimal(f.numerator) / Decimal(f.denominator), "f")
    return f"{f.numerator}/{f.denominator} (≈ {float(f):.6g})"


def fmt_value(v) -> str:
    if isinstance(v, BoolVal):
        return "true" if v.value else "false"
    num = fmt_fraction(v.mag)
    return f"{num} {v.unit.name}" if v.unit.name else num
