"""Parse tree nodes, including error and epsilon nodes."""
from __future__ import annotations


class Node:
    __slots__ = ("symbol", "kind", "children", "token")

    def __init__(self, symbol, kind="rule"):
        self.symbol = symbol
        self.kind = kind  # 'rule' | 'token' | 'epsilon' | 'error'
        self.children = []
        self.token = None

    def iter_tokens(self):
        """Yield token leaves left to right."""
        if self.kind == "token":
            yield self
        for child in self.children:
            yield from child.iter_tokens()

    def pretty(self):
        lines = []

        def rec(node, depth):
            pad = "  " * depth
            if node.kind == "token":
                tok = node.token
                shown = tok.value if tok.value else f"<{tok.type}>"
                lines.append(f"{pad}{tok.type} {shown!r} [{tok.uid} @{tok.pos} {tok.origin}]")
            elif node.kind == "epsilon":
                lines.append(f"{pad}epsilon")
            elif node.kind == "error":
                lines.append(f"{pad}<error:{node.symbol}>")
                for c in node.children:
                    rec(c, depth + 1)
            else:
                lines.append(f"{pad}{node.symbol}")
                for c in node.children:
                    rec(c, depth + 1)

        rec(self, 0)
        return "\n".join(lines)
