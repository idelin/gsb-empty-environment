"""抽象语法树。

节点（全部为不可变 dataclass，可直接用 ``==`` 比较结构）：

- ``Rat(num, den=1)``  有理常数字面量，``den > 0``，构造时约分。
- ``Var(name)``        变量引用。
- ``Neg(operand)``     一元负号。
- ``BinOp(op, left, right)``  二元运算。
- ``Cond(cond, then, otherwise)``  三元条件 ``cond ? then : otherwise``。
- ``Call(func, args)`` 函数调用 ``func(a, b, ...)``。

``Rat`` 的适用域：分子为整数、分母为正整数；不存在浮点，因此没有
浮点舍入（例如 1/3 可精确表示）。负的有理常数在打印器中以
``Rat(非负分子)`` 之外包一层 ``Neg`` 的规范形式出现。
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import StructureError


@dataclass(frozen=True)
class Rat:
    num: int
    den: int = 1

    def __post_init__(self) -> None:
        if not isinstance(self.num, int) or isinstance(self.num, bool):
            raise StructureError("有理数分子必须是整数")
        if not isinstance(self.den, int) or isinstance(self.den, bool):
            raise StructureError("有理数分母必须是整数")
        if self.den == 0:
            raise StructureError("有理数分母不能为 0")
        if self.den < 0:
            object.__setattr__(self, "num", -self.num)
            object.__setattr__(self, "den", -self.den)
        from math import gcd

        g = gcd(abs(self.num), self.den)
        if g > 1:
            object.__setattr__(self, "num", self.num // g)
            object.__setattr__(self, "den", self.den // g)


@dataclass(frozen=True)
class Var:
    name: str


@dataclass(frozen=True)
class Neg:
    operand: object


@dataclass(frozen=True)
class BinOp:
    op: str
    left: object
    right: object


@dataclass(frozen=True)
class Cond:
    cond: object
    then: object
    otherwise: object


@dataclass(frozen=True)
class Call:
    func: str
    args: tuple
