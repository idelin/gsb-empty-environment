"""重写规则：模式、替换、条件、优先级与适用域说明。

- priority 数值大者优先；
- condition 不满足时规则不可用（即使模式匹配成功）；
- domain 是该规则适用域的文字说明，会记录进变换链的每一步。
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Optional

from .expr import Add, Div, Expr, Mul, Num, Pow, Sym
from .pattern import PVar, subst


@dataclass(frozen=True)
class Rule:
    name: str
    pattern: object
    replacement: object  # 模板（含 PVar）或 callable(bindings) -> Expr
    condition: Optional[Callable[[dict], bool]] = None
    priority: int = 0
    domain: str = "任意表达式"

    def applies(self, bindings: dict) -> bool:
        return self.condition is None or bool(self.condition(bindings))

    def fire(self, bindings: dict) -> Expr:
        r = self.replacement
        return r(bindings) if callable(r) else subst(r, bindings)


def _is_num(b: dict, key: str) -> bool:
    return isinstance(b[key], Num)


def default_rules() -> list:
    """内置规则集：有理数精确折叠 + 恒等化简，全部保证终止。"""
    x, a, b = PVar("x"), PVar("a"), PVar("b")

    def both_num(m):
        return _is_num(m, "a") and _is_num(m, "b")

    return [
        Rule(
            "fold-add", Add(a, b),
            lambda m: Num(m["a"].value + m["b"].value),
            condition=both_num, priority=20,
            domain="a、b 为有理数；Fraction 精确加法",
        ),
        Rule(
            "fold-mul", Mul(a, b),
            lambda m: Num(m["a"].value * m["b"].value),
            condition=both_num, priority=20,
            domain="a、b 为有理数；Fraction 精确乘法",
        ),
        Rule(
            "fold-div", Div(a, b),
            lambda m: Num(m["a"].value / m["b"].value),
            condition=lambda m: both_num(m) and m["b"].value != 0,
            priority=20,
            domain="a、b 为有理数且 b ≠ 0；Fraction 精确除法",
        ),
        Rule(
            "add-zero", Add(Num(0), x), x,
            priority=15, domain="任意 x；0 是加法单位元",
        ),
        Rule(
            "mul-zero", Mul(Num(0), x), Num(0),
            priority=15, domain="任意 x；0 是乘法吸收元",
        ),
        Rule(
            "mul-one", Mul(Num(1), x), x,
            priority=15, domain="任意 x；1 是乘法单位元",
        ),
        Rule(
            "div-same", Div(x, x), Num(1),
            condition=lambda m: m["x"] != Num(0), priority=10,
            domain="x ≠ 0；相同非零表达式相除为 1",
        ),
        Rule(
            "pow-zero", Pow(x, Num(0)), Num(1),
            condition=lambda m: m["x"] != Num(0), priority=10,
            domain="x ≠ 0；0^0 未定义故排除",
        ),
        Rule(
            "pow-one", Pow(x, Num(1)), x,
            priority=10, domain="任意 x",
        ),
        Rule(
            "double-add", Add(x, x), Mul(Num(2), x),
            priority=5, domain="任意 x；合并两个相同项",
        ),
    ]


def swap_rules() -> list:
    """a→b、b→a 互换代换规则，用于演示循环检测。"""
    return [
        Rule("swap-ab", Sym("a"), Sym("b"), domain="符号 a"),
        Rule("swap-ba", Sym("b"), Sym("a"), domain="符号 b"),
    ]
