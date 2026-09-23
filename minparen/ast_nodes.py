"""语法树（AST）节点定义。

结构相等性：所有节点都是冻结 dataclass，相等即"结构相同"，与数值等价无关。
例如 ``1 + 2`` 与 ``3`` 数值相等，但语法树结构不同。打印器的最小括号目标
正是以这种结构相等来定义的：``parse(print(t)) == t``（按节点结构比较）。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


class Node:
    """所有语法树节点的基类。"""

    __slots__ = ()


@dataclass(frozen=True)
class Const(Node):
    """有理数常量，值以 ``Fraction`` 精确保存，无浮点损失。

    适用域约定（打印器与解析器的公共规范形式）：
    常量值必须非负；负常量一律表示为 ``Unary('-', Const(...))``。
    拒绝传入 ``float``，因为二进制浮点无法精确表示大多数十进制小数。
    """

    value: Fraction

    def __post_init__(self) -> None:
        if isinstance(self.value, float):
            raise TypeError(
                "Const 不接受 float（二进制浮点有误差）；"
                "请传 int / Fraction / 十进制字符串"
            )
        object.__setattr__(self, "value", Fraction(self.value))


@dataclass(frozen=True)
class Var(Node):
    """变量，如 ``a``、``x``。"""

    name: str


@dataclass(frozen=True)
class Unary(Node):
    """一元前缀运算，如负号 ``-x``。"""

    op: str
    operand: Node


@dataclass(frozen=True)
class Binary(Node):
    """二元运算，如 ``a + b``、``x ^ 2``。"""

    op: str
    left: Node
    right: Node


@dataclass(frozen=True)
class Cond(Node):
    """条件表达式 ``cond ? then : otherwise``（右结合，优先级最低）。"""

    cond: Node
    then: Node
    otherwise: Node


@dataclass(frozen=True)
class Call(Node):
    """函数调用，如 ``f(x, y + 1)``。参数是完整表达式。"""

    name: str
    args: tuple

    def __post_init__(self) -> None:
        object.__setattr__(self, "args", tuple(self.args))
