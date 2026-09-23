"""自然定义域分析：以符号条件（谓词列表）表示，随表达式结构自底向上合并。"""
from __future__ import annotations

from .expr import Node
from .pretty import to_str


def domain_of(node: Node, memo: dict | None = None) -> tuple[str, ...]:
    """返回节点自然定义域的符号条件元组（空元组表示 ℝ）。"""
    if memo is None:
        memo = {}
    cached = memo.get(node)
    if cached is not None:
        return cached
    conds: list[str] = []
    for child in node.args:
        for c in domain_of(child, memo):
            if c not in conds:
                conds.append(c)
    if node.op == "log":
        conds.append(f"{to_str(node.args[0])} > 0")
    elif node.op == "pow":
        n = node.value
        u = to_str(node.args[0])
        if n == 0:
            pass  # 零指数约定：u^0 ≡ 1（含 u = 0），无限制
        elif n.denominator == 1:
            if n < 0:
                conds.append(f"{u} ≠ 0")
        else:
            conds.append(f"{u} > 0")
    result = tuple(conds)
    memo[node] = result
    return result


def fmt_domain(conds: tuple[str, ...]) -> str:
    return "ℝ" if not conds else " ∩ ".join("{" + c + "}" for c in conds)
