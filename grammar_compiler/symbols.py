"""Core symbol and production types.

Symbols are namespaced: a nonterminal is identified by (module, name) so two
modules may define a same-named symbol without ever being merged.
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True, order=True)
class Sym:
    kind: str            # "T" terminal, "N" nonterminal
    name: str
    module: str = ""     # namespace; terminals live in the global "" namespace

    @property
    def is_terminal(self) -> bool:
        return self.kind == "T"

    @property
    def is_nonterminal(self) -> bool:
        return self.kind == "N"

    def __str__(self) -> str:
        if self.kind == "T":
            return f"'{self.name}'"
        return f"{self.module}.{self.name}" if self.module else self.name


END = Sym("T", "$")


@dataclass(frozen=True)
class Production:
    pid: int
    lhs: Sym
    rhs: tuple
    condition: str | None = None   # restricted conditional production

    def __str__(self) -> str:
        rhs = " ".join(str(s) for s in self.rhs) or "epsilon"
        cond = f"   [if {self.condition}]" if self.condition else ""
        return f"#{self.pid} {self.lhs} -> {rhs}{cond}"
