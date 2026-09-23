"""各证明规则的独立检查器。

每个检查器只根据步骤的 input 独立重算变换结果，再与声称的 output
比较；绝不凭步骤的 rule 标签直接信任结论。每条规则都声明其适用域。
"""

from __future__ import annotations

from fractions import Fraction
from typing import Any

from .expr import ExprError, Poly, _const, _node, normalize

RULE_DOMAINS = {
    "expand": "x ∈ ℚ：乘法对加法的分配律在有理数域上处处成立",
    "combine": "x ∈ ℚ：合并同类项是恒等变形，在有理数域上处处成立",
    "differentiate": "多项式在 ℚ 上处处可导，幂法则逐项适用",
    "substitute": "代入值须为有理数；多项式在 ℚ 上处处有定义",
}


class RuleViolation(ValueError):
    """步骤声称的变换不成立。"""


def monomial_degree(expr: Any, var: str) -> int | None:
    """若 expr 是单项式（常数与 var 的幂之积）返回次数，否则返回 None。"""
    try:
        tag, payload = _node(expr)
    except ExprError:
        return None
    if tag == "const":
        try:
            _const(payload)
        except ExprError:
            return None
        return 0
    if tag == "var":
        return 1 if payload == var else None
    if tag == "pow":
        if not isinstance(payload, list) or len(payload) != 2:
            return None
        base, n = payload
        if isinstance(n, bool) or not isinstance(n, int) or n < 0:
            return None
        base_deg = monomial_degree(base, var)
        return None if base_deg is None else base_deg * n
    if tag == "mul":
        if not isinstance(payload, list) or not payload:
            return None
        total = 0
        for factor in payload:
            deg = monomial_degree(factor, var)
            if deg is None:
                return None
            total += deg
        return total
    return None


def expanded_degrees(expr: Any, var: str) -> list[int] | None:
    """若 expr 是展开形（单项式之和），返回各单项式次数列表，否则 None。"""
    try:
        tag, payload = _node(expr)
    except ExprError:
        return None
    if tag == "add":
        if not isinstance(payload, list) or not payload:
            return None
        degs = [monomial_degree(term, var) for term in payload]
        return None if any(d is None for d in degs) else list(degs)
    deg = monomial_degree(expr, var)
    return None if deg is None else [deg]


def _io(step: dict) -> tuple[Any, Any]:
    if "input" not in step or "output" not in step:
        raise RuleViolation("步骤缺少 input/output 字段")
    return step["input"], step["output"]


def check_expand(step: dict, var: str) -> tuple[Poly, Poly]:
    """展开：输入输出必须是同一多项式，且输出为展开形（单项式之和）。"""
    src, dst = _io(step)
    pin = normalize(src, var)
    pout = normalize(dst, var)
    if pin != pout:
        raise RuleViolation("展开改变了多项式的值")
    if expanded_degrees(dst, var) is None:
        raise RuleViolation("输出不是展开形（单项式之和）")
    return pin, pout


def check_combine(step: dict, var: str) -> tuple[Poly, Poly]:
    """合并同类项：值不变，输出为展开形且每个次数至多出现一次。"""
    src, dst = _io(step)
    pin = normalize(src, var)
    pout = normalize(dst, var)
    if pin != pout:
        raise RuleViolation("合并同类项改变了多项式的值")
    degs = expanded_degrees(dst, var)
    if degs is None:
        raise RuleViolation("输出不是展开形（单项式之和）")
    if len(set(degs)) != len(degs):
        raise RuleViolation("输出仍含同类项，未完全合并")
    return pin, pout


def check_differentiate(step: dict, var: str) -> tuple[Poly, Poly]:
    """求导：对证明变量按幂法则独立求导，再与输出比较。"""
    src, dst = _io(step)
    if step.get("var") != var:
        raise RuleViolation(f"求导变量 {step.get('var')!r} 与证明变量 {var!r} 不一致")
    pin = normalize(src, var)
    deriv: Poly = {}
    for exp, coef in pin.items():
        if exp >= 1:
            deriv[exp - 1] = coef * exp
    pout = normalize(dst, var)
    if pout != deriv:
        raise RuleViolation("求导结果与独立计算不一致")
    return pin, pout


def check_substitute(step: dict, var: str) -> tuple[Poly, Poly]:
    """代入：把有理数值代入变量独立求值，再与输出比较。"""
    src, dst = _io(step)
    if step.get("var") != var:
        raise RuleViolation(f"代入变量 {step.get('var')!r} 与证明变量 {var!r} 不一致")
    if "value" not in step:
        raise RuleViolation("代入步骤缺少 value 字段")
    try:
        value = _const(step["value"])
    except ExprError as exc:
        raise RuleViolation(f"代入值不是有理数: {exc}") from exc
    pin = normalize(src, var)
    total = Fraction(0)
    for exp, coef in pin.items():
        total += coef * value**exp
    expect: Poly = {0: total} if total else {}
    pout = normalize(dst, var)
    if pout != expect:
        raise RuleViolation("代入求值结果与独立计算不一致")
    return pin, pout
