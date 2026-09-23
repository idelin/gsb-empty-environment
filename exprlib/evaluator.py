"""精确有理数求值（标准库 ``fractions.Fraction``，不使用符号代数库）。

各变换的适用域：

- ``+ - *``：任意有理数；
- ``/``：除数非零，否则 :class:`DomainError`；
- ``^``（幂）：指数必须是非负整数或任意整数（底数非零时允许负指数），
  结果仍是有理数；非整数次幂不属于有理数域，:class:`DomainError`；
- 一元负号：任意有理数；
- 条件 ``c ? a : b``：先判 ``c`` 是否非零（非零为真），只对被选中的
  分支求值；
- 变量：必须出现在环境映射中，否则 :class:`EvalError`；
- 函数：由调用方提供 ``名称 -> callable``，未提供即 :class:`EvalError`。
"""

from __future__ import annotations

from fractions import Fraction

from . import ast as tree
from .errors import DomainError, EvalError


class Evaluator:
    def __init__(self, env=None, functions=None):
        self.env = {k: Fraction(v) for k, v in (env or {}).items()}
        self.functions = functions or {}

    def evaluate(self, node) -> Fraction:
        if isinstance(node, tree.Rat):
            return Fraction(node.num, node.den)
        if isinstance(node, tree.Var):
            if node.name not in self.env:
                raise EvalError(f"未绑定的变量：{node.name}")
            return self.env[node.name]
        if isinstance(node, tree.Neg):
            return -self.evaluate(node.operand)
        if isinstance(node, tree.BinOp):
            return self._binary(node)
        if isinstance(node, tree.Cond):
            branch = node.then if self.evaluate(node.cond) != 0 else node.otherwise
            return self.evaluate(branch)
        if isinstance(node, tree.Call):
            if node.func not in self.functions:
                raise EvalError(f"未定义的函数：{node.func}")
            args = [self.evaluate(a) for a in node.args]
            return Fraction(self.functions[node.func](*args))
        raise EvalError(f"未知语法树节点：{node!r}")

    def _binary(self, node: tree.BinOp) -> Fraction:
        op = node.op
        a = self.evaluate(node.left)
        b = self.evaluate(node.right)
        if op == "+":
            return a + b
        if op == "-":
            return a - b
        if op == "*":
            return a * b
        if op == "/":
            if b == 0:
                raise DomainError("除数为 0")
            return a / b
        if op == "^":
            if b.denominator != 1:
                raise DomainError(f"有理域内只支持整数次幂，得到指数 {b}")
            exp = b.numerator
            if exp < 0 and a == 0:
                raise DomainError("0 的负整数次幂无定义")
            return a ** exp
        raise EvalError(f"求值器未实现算符：{op}")


def evaluate(node, env=None, functions=None) -> Fraction:
    return Evaluator(env, functions).evaluate(node)
