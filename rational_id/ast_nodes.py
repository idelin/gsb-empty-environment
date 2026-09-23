"""表达式语法树节点，支持有理数精确求值。"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


class Node:
    def eval(self, env):
        """在给定点（{变量名: Fraction}）上精确求值；除数为零抛 ZeroDivisionError。"""
        raise NotImplementedError

    def variables(self):
        raise NotImplementedError


@dataclass(frozen=True)
class Const(Node):
    value: Fraction

    def eval(self, env):
        return self.value

    def variables(self):
        return set()


@dataclass(frozen=True)
class Var(Node):
    name: str

    def eval(self, env):
        return env[self.name]

    def variables(self):
        return {self.name}


@dataclass(frozen=True)
class Add(Node):
    left: Node
    right: Node

    def eval(self, env):
        return self.left.eval(env) + self.right.eval(env)

    def variables(self):
        return self.left.variables() | self.right.variables()


@dataclass(frozen=True)
class Sub(Node):
    left: Node
    right: Node

    def eval(self, env):
        return self.left.eval(env) - self.right.eval(env)

    def variables(self):
        return self.left.variables() | self.right.variables()


@dataclass(frozen=True)
class Mul(Node):
    left: Node
    right: Node

    def eval(self, env):
        return self.left.eval(env) * self.right.eval(env)

    def variables(self):
        return self.left.variables() | self.right.variables()


@dataclass(frozen=True)
class Div(Node):
    left: Node
    right: Node

    def eval(self, env):
        denominator = self.right.eval(env)
        if denominator == 0:
            raise ZeroDivisionError("除数为零")
        return self.left.eval(env) / denominator

    def variables(self):
        return self.left.variables() | self.right.variables()


@dataclass(frozen=True)
class Neg(Node):
    operand: Node

    def eval(self, env):
        return -self.operand.eval(env)

    def variables(self):
        return self.operand.variables()


@dataclass(frozen=True)
class Pow(Node):
    base: Node
    exp: int  # 非负整数（解析器保证）

    def eval(self, env):
        return self.base.eval(env) ** self.exp

    def variables(self):
        return self.base.variables()
