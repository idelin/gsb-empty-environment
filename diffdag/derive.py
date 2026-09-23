"""DAG 上的符号求导：记忆化保证共享子图只求导一次，每一步记录来源规则与适用域。"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .expr import Factory, Node
from .pretty import to_str


@dataclass
class Step:
    rule: str      # 来源规则名
    node_str: str  # 被求导的子表达式
    detail: str    # 规则内容
    domain: str    # 该步适用域


class Differentiator:
    def __init__(self, factory: Factory, var_name: str = "x") -> None:
        self.f = factory
        self.var_name = var_name
        self.memo: dict[Node, Node] = {}
        self.steps: list[Step] = []
        self.reuse_count = 0  # 共享子图命中缓存的次数

    def diff(self, node: Node) -> Node:
        cached = self.memo.get(node)
        if cached is not None:
            self.reuse_count += 1
            self.steps.append(Step(
                "复用", to_str(node),
                f"节点 #{node.nid} 的导数已缓存，共享子图不重复展开",
                "—"))
            return cached
        d, rule, detail, dom = self._apply(node)
        self.memo[node] = d
        self.steps.append(Step(rule, to_str(node), detail, dom))
        return d

    def _apply(self, node: Node):
        f = self.f
        op = node.op
        if op == "const":
            return f.zero, "常数法则", f"d/dx {to_str(node)} = 0", "ℝ"
        if op == "var":
            if node.name == self.var_name:
                return f.one, "变量法则", "d/dx x = 1", "ℝ"
            return f.zero, "常数法则", f"d/dx {node.name} = 0（非求导变量，视为常数）", "ℝ"
        if op == "add":
            a, b = node.args
            da, db = self.diff(a), self.diff(b)
            return f.add(da, db), "和法则", "(u+v)' = u' + v'", "dom(u) ∩ dom(v)"
        if op == "mul":
            a, b = node.args
            da, db = self.diff(a), self.diff(b)
            return (f.add(f.mul(da, b), f.mul(a, db)),
                    "乘积法则", "(u·v)' = u'·v + u·v'", "dom(u) ∩ dom(v)")
        if op == "pow":
            return self._diff_pow(node)
        u = node.args[0]
        du = self.diff(u)
        us = to_str(u)
        if op == "sin":
            return f.mul(f.cos(u), du), "链式法则·正弦", "(sin u)' = cos(u)·u'", "dom(u)"
        if op == "cos":
            return (f.mul(f.const(-1), f.mul(f.sin(u), du)),
                    "链式法则·余弦", "(cos u)' = -sin(u)·u'", "dom(u)")
        if op == "exp":
            return f.mul(f.exp(u), du), "链式法则·指数", "(exp u)' = exp(u)·u'", "dom(u)"
        if op == "log":
            return (f.mul(du, f.pow(u, -1)),
                    "链式法则·对数", "(log u)' = u'/u",
                    f"保留原定义域 {us} > 0")
        raise ValueError(f"未知算子: {op}")

    def _diff_pow(self, node: Node):
        f = self.f
        u = node.args[0]
        n: Fraction = node.value
        us = to_str(u)
        if n == 0:
            return (f.zero, "零指数约定",
                    "u^0 ≡ 1（常函数约定，含 u = 0），导数为 0", "ℝ")
        du = self.diff(u)
        d = f.mul(f.mul(f.const(n), f.pow(u, n - 1)), du)
        if n.denominator == 1 and n > 0:
            dom = "ℝ"
        elif n.denominator == 1:
            dom = f"{us} ≠ 0"
        else:
            dom = f"{us} > 0"
        def _fmt_exp(e):
            return str(e.numerator) if e.denominator == 1 and e >= 0 else f"({e})"
        detail = f"(u^{_fmt_exp(n)})' = {n}·u^{_fmt_exp(n - 1)}·u'"
        return d, "幂法则", detail, dom
