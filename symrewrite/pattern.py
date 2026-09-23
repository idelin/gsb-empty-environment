"""模式匹配与模板替换。

模式是含有 PVar（模式变量）的表达式树；匹配为确定性结构匹配，
同名 PVar 必须匹配到结构相等的子表达式。
"""
from __future__ import annotations

from dataclasses import dataclass

from .expr import Add, Div, Expr, Mul, Num, Pow, Sym


@dataclass(frozen=True)
class PVar:
    """模式变量，可匹配任意子表达式并绑定。"""

    name: str


def match(pattern, target: Expr, bindings: dict | None = None) -> dict | None:
    """尝试用 pattern 匹配 target，成功返回绑定字典，失败返回 None。"""
    b = {} if bindings is None else dict(bindings)
    return b if _match_into(pattern, target, b) else None


def _match_into(p, t: Expr, b: dict) -> bool:
    if isinstance(p, PVar):
        if p.name in b:
            return b[p.name] == t
        b[p.name] = t
        return True
    if isinstance(p, Num):
        return isinstance(t, Num) and p.value == t.value
    if isinstance(p, Sym):
        return isinstance(t, Sym) and p.name == t.name
    if isinstance(p, (Add, Mul)):
        return (
            isinstance(t, type(p))
            and len(p.args) == len(t.args)
            and all(_match_into(pa, ta, b) for pa, ta in zip(p.args, t.args))
        )
    if isinstance(p, Div):
        return (
            isinstance(t, Div)
            and _match_into(p.num, t.num, b)
            and _match_into(p.den, t.den, b)
        )
    if isinstance(p, Pow):
        return (
            isinstance(t, Pow)
            and _match_into(p.base, t.base, b)
            and _match_into(p.exp, t.exp, b)
        )
    raise TypeError(f"非法模式节点: {p!r}")


def subst(template, bindings: dict) -> Expr:
    """把模板中的 PVar 替换为绑定值，得到具体表达式。"""
    if isinstance(template, PVar):
        return bindings[template.name]
    if isinstance(template, (Num, Sym)):
        return template
    if isinstance(template, (Add, Mul)):
        return type(template)(*(subst(a, bindings) for a in template.args))
    if isinstance(template, Div):
        return Div(subst(template.num, bindings), subst(template.den, bindings))
    if isinstance(template, Pow):
        return Pow(subst(template.base, bindings), subst(template.exp, bindings))
    raise TypeError(f"非法模板节点: {template!r}")
