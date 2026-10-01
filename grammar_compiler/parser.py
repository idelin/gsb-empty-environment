"""Table-driven predictive parser.

The parser never loops on left recursion: expanding a nonterminal at an input
position where the same nonterminal is already being expanded raises
LeftRecursionError.  Hitting a multiply-defined table cell raises
ConflictError carrying the competing productions and the consumed prefix.
"""
from __future__ import annotations

from dataclasses import dataclass

from .symbols import END, Sym


class ParseError(Exception):
    pass


class ConflictError(ParseError):
    def __init__(self, nonterminal, terminal, candidates, consumed):
        self.nonterminal = nonterminal
        self.terminal = terminal
        self.candidates = tuple(candidates)
        self.consumed = tuple(consumed)
        super().__init__(
            f"conflict at {nonterminal} on lookahead {terminal} after "
            f"consuming [{' '.join(t.name for t in self.consumed)}]; "
            f"candidates: {list(self.candidates)}")


class LeftRecursionError(ParseError):
    pass


@dataclass(frozen=True)
class _Marker:
    sym: Sym
    pos: int


class Parser:
    def __init__(self, table, productions, start):
        self.table = table                # (Sym, Sym) -> tuple of pids
        self.productions = productions    # pid -> Production
        self.start = start

    def parse(self, tokens, chooser=None):
        inp = [t if isinstance(t, Sym) else Sym("T", t) for t in tokens]
        inp.append(END)
        stack = [END, self.start]
        pos = 0
        active = set()
        steps = 0
        while stack:
            top = stack.pop()
            if isinstance(top, _Marker):
                active.discard((top.sym, top.pos))
                continue
            look = inp[pos]
            if top.is_terminal:
                if top != look:
                    raise ParseError(
                        f"expected {top} but got {look} at position {pos}")
                pos += 1
                continue
            cell = self.table.get((top, look))
            if not cell:
                raise ParseError(
                    f"no production for {top} on lookahead {look} "
                    f"at position {pos}")
            if len(cell) > 1:
                if chooser is None:
                    raise ConflictError(top, look, cell, tuple(inp[:pos]))
                pid = chooser(top, look, cell)
            else:
                pid = cell[0]
            key = (top, pos)
            if key in active:
                raise LeftRecursionError(
                    f"left recursion: {top} expands to itself without "
                    f"consuming input at position {pos}")
            active.add(key)
            stack.append(_Marker(top, pos))
            stack.extend(reversed(self.productions[pid].rhs))
            steps += 1
        if pos != len(inp):
            raise ParseError("unconsumed input")
        return steps
