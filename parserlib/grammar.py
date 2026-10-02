"""Context-free grammar: validation, FIRST/FOLLOW computation.

Grammars are given explicitly as production lists; epsilon productions are
empty right-hand sides.  Left-recursive cycles (A =>+ A) are rejected because
they would make predictive expansion non-terminating.
"""
from __future__ import annotations

from dataclasses import dataclass

from .tokens import EOF_KIND

EPSILON = "ε"
EPS: tuple[str, ...] = ()


class GrammarError(ValueError):
    """Raised when a grammar is malformed or unfit for predictive parsing."""


@dataclass
class Grammar:
    start: str
    rules: dict[str, list[tuple[str, ...]]]
    terminals: frozenset[str]

    def __post_init__(self) -> None:
        self.terminals = frozenset(self.terminals)
        self.rules = {
            lhs: [tuple(rhs) for rhs in rhss] for lhs, rhss in self.rules.items()
        }
        self.nonterminals = frozenset(self.rules)
        self._nullable: set[str] | None = None
        self._first: dict[str, set[str]] | None = None
        self._follow: dict[str, set[str]] | None = None
        self._validate()

    def _validate(self) -> None:
        if self.start not in self.rules:
            raise GrammarError(f"start symbol {self.start!r} has no productions")
        if EOF_KIND in self.terminals or EOF_KIND in self.rules:
            raise GrammarError(f"{EOF_KIND!r} is reserved for end-of-input")
        both = self.nonterminals & self.terminals
        if both:
            raise GrammarError(
                f"symbols are both terminal and nonterminal: {sorted(both)}"
            )
        for lhs, rhss in self.rules.items():
            if not rhss:
                raise GrammarError(f"nonterminal {lhs!r} has no productions")
            for rhs in rhss:
                for sym in rhs:
                    if sym not in self.terminals and sym not in self.nonterminals:
                        raise GrammarError(
                            f"unknown symbol {sym!r} in production {lhs} -> {rhs}"
                        )
        self._reject_left_recursion()

    def is_terminal(self, sym: str) -> bool:
        return sym in self.terminals

    def is_nonterminal(self, sym: str) -> bool:
        return sym in self.nonterminals

    def nullable_set(self) -> set[str]:
        if self._nullable is None:
            nullable: set[str] = set()
            changed = True
            while changed:
                changed = False
                for lhs, rhss in self.rules.items():
                    if lhs in nullable:
                        continue
                    for rhs in rhss:
                        if all(
                            sym in self.nonterminals and sym in nullable for sym in rhs
                        ):
                            nullable.add(lhs)
                            changed = True
                            break
            self._nullable = nullable
        return self._nullable

    def _reject_left_recursion(self) -> None:
        nullable = self.nullable_set()
        edges: dict[str, set[str]] = {a: set() for a in self.nonterminals}
        for lhs, rhss in self.rules.items():
            for rhs in rhss:
                for sym in rhs:
                    if sym in self.nonterminals:
                        edges[lhs].add(sym)
                        if sym not in nullable:
                            break
                    else:
                        break
        color = {a: 0 for a in self.nonterminals}

        def visit(node: str, trail: list[str]) -> None:
            color[node] = 1
            for nxt in edges[node]:
                if color[nxt] == 1:
                    cycle = " -> ".join(trail + [nxt])
                    raise GrammarError(f"left-recursive cycle: {cycle}")
                if color[nxt] == 0:
                    visit(nxt, trail + [nxt])
            color[node] = 2

        for a in self.nonterminals:
            if color[a] == 0:
                visit(a, [a])

    def first_sets(self) -> dict[str, set[str]]:
        if self._first is None:
            nullable = self.nullable_set()
            first: dict[str, set[str]] = {a: set() for a in self.nonterminals}
            changed = True
            while changed:
                changed = False
                for lhs, rhss in self.rules.items():
                    for rhs in rhss:
                        for sym in rhs:
                            if sym in self.terminals:
                                if sym not in first[lhs]:
                                    first[lhs].add(sym)
                                    changed = True
                                break
                            before = len(first[lhs])
                            first[lhs] |= first[sym]
                            if len(first[lhs]) != before:
                                changed = True
                            if sym not in nullable:
                                break
            self._first = first
        return self._first

    def first_of_sequence(self, symbols: tuple[str, ...]) -> tuple[frozenset[str], bool]:
        """FIRST of a symbol sequence plus whether the whole sequence is nullable."""
        nullable = self.nullable_set()
        first = self.first_sets()
        out: set[str] = set()
        all_nullable = True
        for sym in symbols:
            if sym in self.terminals:
                out.add(sym)
                all_nullable = False
                break
            out |= first[sym]
            if sym not in nullable:
                all_nullable = False
                break
        return frozenset(out), all_nullable

    def follow_sets(self) -> dict[str, set[str]]:
        if self._follow is None:
            follow: dict[str, set[str]] = {a: set() for a in self.nonterminals}
            follow[self.start].add(EOF_KIND)
            changed = True
            while changed:
                changed = False
                for lhs, rhss in self.rules.items():
                    for rhs in rhss:
                        for i, sym in enumerate(rhs):
                            if sym not in self.nonterminals:
                                continue
                            rest_first, rest_nullable = self.first_of_sequence(
                                rhs[i + 1 :]
                            )
                            before = len(follow[sym])
                            follow[sym] |= rest_first
                            if rest_nullable:
                                follow[sym] |= follow[lhs]
                            if len(follow[sym]) != before:
                                changed = True
            self._follow = follow
        return self._follow
