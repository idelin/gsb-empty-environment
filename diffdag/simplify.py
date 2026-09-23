"""化简器：自底向上重写，逐步记录规则与适用域。

化简不改变定义域的实现方式：定义域条件由外层管线随原表达式一并保留，
任何可能「扩大」定义域的重写（如 0·u ⇒ 0）只在原定义域上断言相等，
并在步骤记录中注明适用域。
"""
from __future__ import annotations

from dataclasses import dataclass

from .expr import Factory, Node
from .pretty import to_str


@dataclass
class SimpStep:
    rule: str
    before: str
    after: str
    domain: str  # 该步适用域说明


def _is_const(node: Node, value=None) -> bool:
    return node.op == "const" and (value is None or node.value == value)


class Simplifier:
    def __init__(self, factory: Factory) -> None:
        self.f = factory
        self.memo: dict[Node, Node] = {}
        self.steps: list[SimpStep] = []

    def simplify(self, node: Node) -> Node:
        cached = self.memo.get(node)
        if cached is not None:
            return cached
        args = tuple(self.simplify(a) for a in node.args)
        cur = node if args == node.args else self.f.rebuild(node, args)
        while True:
            nxt = self._rewrite(cur)
            if nxt is cur:
                break
            cur = nxt
        self.memo[node] = cur
        return cur

    def _record(self, node: Node, new: Node, rule: str, domain: str) -> Node:
        if new is not node:
            self.steps.append(SimpStep(rule, to_str(node), to_str(new), domain))
        return new

    def _rewrite(self, node: Node) -> Node:
        f = self.f
        op = node.op
        if op == "add":
            a, b = node.args
            if _is_const(a) and _is_const(b):
                return self._record(node, f.const(a.value + b.value),
                                    "常数折叠", "ℝ（精确有理数加法）")
            if _is_const(a, 0):
                return self._record(node, b, "加法零元", "dom(右项)，定义域不变")
            if _is_const(b, 0):
                return self._record(node, a, "加法零元", "dom(左项)，定义域不变")
            if a is b:
                return self._record(node, f.mul(f.const(2), a),
                                    "合并同类项", "dom(u)，定义域不变")
        elif op == "mul":
            a, b = node.args
            if _is_const(a) and _is_const(b):
                return self._record(node, f.const(a.value * b.value),
                                    "常数折叠", "ℝ（精确有理数乘法）")
            if _is_const(a, 0) or _is_const(b, 0):
                return self._record(node, f.zero, "乘法零元",
                                    "在原定义域 dom(被消去项) 上恒等；结果定义域由管线保留")
            if _is_const(a, 1):
                return self._record(node, b, "乘法幺元", "dom(右项)，定义域不变")
            if _is_const(b, 1):
                return self._record(node, a, "乘法幺元", "dom(左项)，定义域不变")
            if _is_const(a) and b.op == "mul" and _is_const(b.args[0]):
                merged = f.mul(f.const(a.value * b.args[0].value), b.args[1])
                return self._record(node, merged, "常数合并（结合律）",
                                    "dom(剩余因子)，定义域不变")
        elif op == "pow":
            u = node.args[0]
            n = node.value
            if n == 0:
                return self._record(node, f.one, "零指数约定",
                                    "u^0 ≡ 1（含 u = 0），适用域 ℝ")
            if n == 1:
                return self._record(node, u, "单位指数", "dom(u)，定义域不变")
            if u.op == "const" and n.denominator == 1 and (n >= 0 or u.value != 0):
                return self._record(node, f.const(u.value ** n),
                                    "常数折叠", "底数非零或指数非负")
        return node
