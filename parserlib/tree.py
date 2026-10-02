"""Parse tree nodes, including error nodes that retain skipped tokens."""
from __future__ import annotations

from dataclasses import dataclass, field

from .tokens import Token

ERROR_SYMBOL = "<error>"


@dataclass
class Node:
    symbol: str
    children: list["Node"] = field(default_factory=list)
    token: Token | None = None
    is_error: bool = False
    skipped: tuple[Token, ...] = ()
    note: str = ""

    @classmethod
    def error(cls, skipped: list[Token]) -> "Node":
        return cls(symbol=ERROR_SYMBOL, is_error=True, skipped=tuple(skipped))

    @property
    def is_leaf(self) -> bool:
        return not self.children

    def leaves(self) -> list["Node"]:
        out: list[Node] = []

        def walk(node: "Node") -> None:
            if node.is_leaf:
                out.append(node)
            for child in node.children:
                walk(child)

        walk(self)
        return out

    def find_all(self, symbol: str) -> list["Node"]:
        return [n for n in self._walk() if n.symbol == symbol]

    def _walk(self) -> list["Node"]:
        out = [self]
        for child in self.children:
            out.extend(child._walk())
        return out

    def source_map(self) -> list[tuple[str, tuple[int, int], str]]:
        """(symbol, span, origin) for every leaf carrying a token, in order."""
        mapping = []
        for leaf in self.leaves():
            if leaf.token is not None:
                mapping.append(
                    (leaf.symbol, leaf.token.span, leaf.token.origin.value)
                )
        return mapping

    def pretty(self, indent: int = 0) -> str:
        pad = "  " * indent
        bits = [pad + self.symbol]
        if self.token is not None:
            bits.append(
                f" {self.token.text!r} @{self.token.start}..{self.token.end}"
                f" [{self.token.origin.value}]"
            )
        if self.is_error:
            bits.append("  <ERROR")
            if self.skipped:
                bits.append(" skipped=" + ",".join(t.text for t in self.skipped))
            bits.append(">")
        if self.note:
            bits.append(f"  ; {self.note}")
        lines = ["".join(bits)]
        for child in self.children:
            lines.append(child.pretty(indent + 1))
        return "\n".join(lines)
