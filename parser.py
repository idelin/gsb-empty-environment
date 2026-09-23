"""把文本等式解析为线性型方程与非零条件（仅使用标准库 ast）。"""
from __future__ import annotations

import ast
import io
import tokenize
from fractions import Fraction

from linear import LinearForm


class RationalExpr:
    """线性型之商 num / den（den 恒为 1 时即普通线性型）。"""

    __slots__ = ("num", "den")

    def __init__(self, num, den=None):
        self.num = num
        self.den = den if den is not None else LinearForm.constant(1)


def _mul_linear(f, g):
    """线性型乘积，仅当其一为常数时合法，否则超出线性求解范围。"""
    if f.is_constant():
        return g.scale(f.const)
    if g.is_constant():
        return f.scale(g.const)
    raise ValueError(f"出现非线性乘积 ({f}) * ({g})，超出线性求解范围")


def _add_rat(a, b):
    num = _mul_linear(a.num, b.den) + _mul_linear(b.num, a.den)
    den = _mul_linear(a.den, b.den)
    return RationalExpr(num, den)


def _neg_rat(a):
    return RationalExpr(-a.num, a.den)


def _sub_rat(a, b):
    return _add_rat(a, _neg_rat(b))


def _mul_rat(a, b):
    return RationalExpr(_mul_linear(a.num, b.num), _mul_linear(a.den, b.den))


def _div_rat(a, b):
    if b.num.is_zero():
        raise ValueError("除以恒为 0 的表达式，等式无意义")
    return RationalExpr(_mul_linear(a.num, b.den), _mul_linear(a.den, b.num))


def _to_rational(node):
    if isinstance(node, ast.Expression):
        return _to_rational(node.body)
    if isinstance(node, ast.Constant):
        if isinstance(node.value, bool) or not isinstance(node.value, (int, float)):
            raise ValueError(f"不支持的常量: {node.value!r}")
        value = (
            Fraction(str(node.value))
            if isinstance(node.value, float)
            else Fraction(node.value)
        )
        return RationalExpr(LinearForm.constant(value))
    if isinstance(node, ast.Name):
        return RationalExpr(LinearForm.variable(node.id))
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.USub):
        return _neg_rat(_to_rational(node.operand))
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.UAdd):
        return _to_rational(node.operand)
    if isinstance(node, ast.BinOp):
        left = _to_rational(node.left)
        right = _to_rational(node.right)
        if isinstance(node.op, ast.Add):
            return _add_rat(left, right)
        if isinstance(node.op, ast.Sub):
            return _sub_rat(left, right)
        if isinstance(node.op, ast.Mult):
            return _mul_rat(left, right)
        if isinstance(node.op, ast.Div):
            return _div_rat(left, right)
        raise ValueError(f"不支持的运算符: {ast.dump(node.op)}")
    raise ValueError(f"不支持的表达式: {ast.dump(node)}")


def _parse_side(text):
    try:
        tree = ast.parse(text, mode="eval")
    except SyntaxError as exc:
        raise ValueError(f"无法解析表达式 {text!r}: {exc}") from exc
    return tree.body


def _split_equation(text):
    """按唯一的单字符 '=' 切分等式两边（兼容 '==' 写法）。"""
    tokens = list(tokenize.generate_tokens(io.StringIO(text).readline))
    eq_token = None
    for tok in tokens:
        if tok.type != tokenize.OP:
            continue
        if tok.string in ("=", "=="):
            if eq_token is not None:
                raise ValueError(f"等式含有多个 '=': {text!r}")
            eq_token = tok
        elif tok.string in ("!=", "<=", ">="):
            raise ValueError(f"需要形如 'lhs = rhs' 的等式: {text!r}")
    if eq_token is None:
        raise ValueError(f"需要形如 'lhs = rhs' 的等式: {text!r}")
    (row, col), (_, endcol) = eq_token.start, eq_token.end
    lines = text.splitlines() or [text]
    if row != 1 or len(lines) != 1:
        raise ValueError(f"等式须为单行: {text!r}")
    return text[:col].strip(), text[endcol:].strip()


def parse_equation(text):
    """解析 'lhs = rhs'，返回 (方程线性型 == 0, 非零条件列表, 说明列表)。"""
    left_text, right_text = _split_equation(text)
    lhs = _to_rational(_parse_side(left_text))
    rhs = _to_rational(_parse_side(right_text))
    diff = _sub_rat(lhs, rhs)  # diff.num / diff.den = 0
    notes = []
    conditions = []
    if diff.den.is_constant():
        if diff.den.const == 0:
            raise ValueError(f"等式 {text!r} 的除数恒为 0，无意义")
        if diff.den.const != 1:
            notes.append(
                f"除数为非零常数 {diff.den.const}，两边同乘不改变解集"
                "（适用域：Q），无需分支"
            )
    else:
        conditions.append(diff.den)
        notes.append(
            f"除以可能为 0 的项 ({diff.den})，须分支：主分支假设 {diff.den} != 0；"
            f"{diff.den} = 0 时原式无定义"
        )
    return diff.num, conditions, notes
