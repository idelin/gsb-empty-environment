"""表达式 DAG：哈希一致化（hash-consing）保证结构相同的子表达式共享同一节点。

设计约定：
- 幂运算仅限「有理数常数指数」，指数存于 Node.value（Fraction）。
- 零指数约定：u^0 视为常函数 1（含 u = 0 处），导数为 0。
- 非整数有理指数要求底数 > 0；负整数指数要求底数 != 0。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction


@dataclass(frozen=True)
class Node:
    op: str                              # const/var/add/mul/pow/sin/cos/exp/log
    args: tuple["Node", ...] = ()
    value: Fraction | None = None        # const 的值 / pow 的指数
    name: str | None = None              # var 的变量名
    nid: int = field(default=-1, compare=False, hash=False)


class Factory:
    """构造并内化（intern）节点：相同结构的子表达式全仓库只存一份。"""

    def __init__(self) -> None:
        self._table: dict = {}
        self._count = 0
        self.zero = self.const(0)
        self.one = self.const(1)

    def _intern(self, op, args=(), value=None, name=None) -> Node:
        key = (op, tuple(args), value, name)
        node = self._table.get(key)
        if node is None:
            node = Node(op, tuple(args), value, name, self._count)
            self._count += 1
            self._table[key] = node
        return node

    @property
    def node_count(self) -> int:
        return self._count

    def rebuild(self, node: Node, args) -> Node:
        return self._intern(node.op, tuple(args), node.value, node.name)

    def const(self, v) -> Node:
        return self._intern("const", value=Fraction(v))

    def var(self, name: str) -> Node:
        return self._intern("var", name=name)

    def add(self, a: Node, b: Node) -> Node:
        return self._intern("add", (a, b))

    def mul(self, a: Node, b: Node) -> Node:
        return self._intern("mul", (a, b))

    def pow(self, base: Node, exponent) -> Node:
        return self._intern("pow", (base,), value=Fraction(exponent))

    def sin(self, u: Node) -> Node:
        return self._intern("sin", (u,))

    def cos(self, u: Node) -> Node:
        return self._intern("cos", (u,))

    def exp(self, u: Node) -> Node:
        return self._intern("exp", (u,))

    def log(self, u: Node) -> Node:
        return self._intern("log", (u,))
