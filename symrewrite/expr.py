"""符号表达式的不可变 AST 定义与规范化。

有理数一律用 fractions.Fraction 精确表示，不经过浮点。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


class Expr:
    """所有表达式节点的基类。"""

    __slots__ = ()


@dataclass(frozen=True)
class Num(Expr):
    """精确有理数，例如 Num(Fraction(1, 3)) 或 Num("1/3")。"""

    value: Fraction

    def __init__(self, value):
        object.__setattr__(self, "value", Fraction(value))


@dataclass(frozen=True)
class Sym(Expr):
    """符号（变量），例如 Sym("x")。"""

    name: str


@dataclass(frozen=True)
class Add(Expr):
    """n 元加法：Add(a, b, ...)。"""

    args: tuple

    def __init__(self, *args):
        object.__setattr__(self, "args", tuple(args))


@dataclass(frozen=True)
class Mul(Expr):
    """n 元乘法：Mul(a, b, ...)。"""

    args: tuple

    def __init__(self, *args):
        object.__setattr__(self, "args", tuple(args))


@dataclass(frozen=True)
class Div(Expr):
    """除法：Div(分子, 分母)。分母非零是适用域约束，由规则条件保证。"""

    num: Expr
    den: Expr


@dataclass(frozen=True)
class Pow(Expr):
    """幂：Pow(底, 指数)。"""

    base: Expr
    exp: Expr


def children(e: Expr) -> tuple:
    if isinstance(e, (Add, Mul)):
        return e.args
    if isinstance(e, Div):
        return (e.num, e.den)
    if isinstance(e, Pow):
        return (e.base, e.exp)
    return ()


def rebuild(e: Expr, new_children) -> Expr:
    if isinstance(e, Add):
        return Add(*new_children)
    if isinstance(e, Mul):
        return Mul(*new_children)
    if isinstance(e, Div):
        return Div(*new_children)
    if isinstance(e, Pow):
        return Pow(*new_children)
    raise TypeError(f"叶子节点没有子节点可重建: {e!r}")


def get_at(e: Expr, path: tuple) -> Expr:
    """按位置路径（子节点下标序列）取子表达式。"""
    for i in path:
        e = children(e)[i]
    return e


def replace_at(e: Expr, path: tuple, new: Expr) -> Expr:
    """在指定位置做局部替换，返回新表达式（原表达式不变）。"""
    if not path:
        return new
    head, rest = path[0], path[1:]
    cs = list(children(e))
    cs[head] = replace_at(cs[head], rest, new)
    return rebuild(e, cs)


def to_str(e: Expr) -> str:
    if isinstance(e, Num):
        v = e.value
        return str(v.numerator) if v.denominator == 1 else f"{v.numerator}/{v.denominator}"
    if isinstance(e, Sym):
        return e.name
    if isinstance(e, Add):
        return "(" + " + ".join(to_str(a) for a in e.args) + ")"
    if isinstance(e, Mul):
        return "(" + " * ".join(to_str(a) for a in e.args) + ")"
    if isinstance(e, Div):
        return f"({to_str(e.num)} / {to_str(e.den)})"
    if isinstance(e, Pow):
        return f"({to_str(e.base)} ^ {to_str(e.exp)})"
    raise TypeError(f"未知节点: {e!r}")


_TYPE_RANK = {Num: 0, Sym: 1, Add: 2, Mul: 3, Div: 4, Pow: 5}


def _sort_key(e: Expr):
    return (_TYPE_RANK[type(e)], to_str(e))


def canonical(e: Expr) -> Expr:
    """规范化：拍平并排序可交换的 Add/Mul 参数，使等价式有唯一形态。

    用于重复状态（循环）检测，不做任何代数化简。
    """
    if isinstance(e, (Add, Mul)):
        flat = []
        for a in e.args:
            ca = canonical(a)
            if isinstance(ca, type(e)):
                flat.extend(ca.args)
            else:
                flat.append(ca)
        return type(e)(*sorted(flat, key=_sort_key))
    if isinstance(e, Div):
        return Div(canonical(e.num), canonical(e.den))
    if isinstance(e, Pow):
        return Pow(canonical(e.base), canonical(e.exp))
    return e


def canonical_key(e: Expr) -> str:
    return to_str(canonical(e))
