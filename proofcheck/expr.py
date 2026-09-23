"""表达式 AST 与有理数精确多项式规范化。

表达式用纯 dict 表示（可直接 JSON 序列化）：

    {"const": "3/4"}        有理数常量（字符串或整数，禁止浮点）
    {"var": "x"}            变量
    {"add": [e1, e2, ...]}  求和
    {"mul": [e1, e2, ...]}  求积
    {"pow": [e, n]}         非负整数次幂

多项式内部表示为 {次数: Fraction 系数}，零系数不存储，零多项式为 {}。
"""

from __future__ import annotations

from fractions import Fraction
from typing import Any

Poly = dict  # dict[int, Fraction]


class ExprError(ValueError):
    """表达式结构非法或超出支持片段。"""


def _const(value: Any) -> Fraction:
    if isinstance(value, bool):
        raise ExprError(f"常量类型不支持: {value!r}")
    if isinstance(value, float):
        raise ExprError(f"禁止使用浮点常量以保证精确性: {value!r}")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        try:
            return Fraction(value)
        except (ValueError, ZeroDivisionError) as exc:
            raise ExprError(f"无法解析有理数常量: {value!r}") from exc
    raise ExprError(f"常量类型不支持: {value!r}")


def _node(expr: Any) -> tuple[str, Any]:
    if not isinstance(expr, dict) or len(expr) != 1:
        raise ExprError(f"表达式节点必须是单键 dict: {expr!r}")
    (tag, payload), = expr.items()
    return tag, payload


def _add(a: Poly, b: Poly) -> Poly:
    out = dict(a)
    for exp, coef in b.items():
        merged = out.get(exp, Fraction(0)) + coef
        if merged:
            out[exp] = merged
        else:
            out.pop(exp, None)
    return out


def _mul(a: Poly, b: Poly) -> Poly:
    out: Poly = {}
    for ea, ca in a.items():
        for eb, cb in b.items():
            exp = ea + eb
            merged = out.get(exp, Fraction(0)) + ca * cb
            if merged:
                out[exp] = merged
            else:
                out.pop(exp, None)
    return out


def _pow(base: Poly, n: int) -> Poly:
    out: Poly = {0: Fraction(1)}
    for _ in range(n):
        out = _mul(out, base)
    return out


def normalize(expr: Any, var: str) -> Poly:
    """把表达式规范化为 var 的有理系数多项式（精确计算）。"""
    tag, payload = _node(expr)
    if tag == "const":
        value = _const(payload)
        return {0: value} if value else {}
    if tag == "var":
        if payload != var:
            raise ExprError(f"出现未知变量 {payload!r}，证明变量为 {var!r}")
        return {1: Fraction(1)}
    if tag == "add":
        if not isinstance(payload, list) or not payload:
            raise ExprError("add 需要非空参数列表")
        acc: Poly = {}
        for item in payload:
            acc = _add(acc, normalize(item, var))
        return acc
    if tag == "mul":
        if not isinstance(payload, list) or not payload:
            raise ExprError("mul 需要非空参数列表")
        acc = {0: Fraction(1)}
        for item in payload:
            acc = _mul(acc, normalize(item, var))
        return acc
    if tag == "pow":
        if (
            not isinstance(payload, list)
            or len(payload) != 2
            or isinstance(payload[1], bool)
            or not isinstance(payload[1], int)
            or payload[1] < 0
        ):
            raise ExprError("pow 需要 [底数, 非负整数指数]")
        return _pow(normalize(payload[0], var), payload[1])
    raise ExprError(f"未知表达式节点: {tag!r}")
