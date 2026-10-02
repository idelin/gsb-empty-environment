"""LL(1) predictive parsing table with explicit conflict detection.

The parser only runs on a conflict-free table: any cell that two distinct
productions compete for is reported through :class:`TableConflictError`.
"""
from __future__ import annotations

from dataclasses import dataclass

from .grammar import Grammar


@dataclass(frozen=True)
class Conflict:
    nonterminal: str
    terminal: str
    existing: tuple[str, ...]
    incoming: tuple[str, ...]


class TableConflictError(Exception):
    def __init__(self, conflicts: list[Conflict]) -> None:
        self.conflicts = list(conflicts)
        details = "; ".join(
            f"[{c.nonterminal}, {c.terminal}]: {c.existing} vs {c.incoming}"
            for c in self.conflicts
        )
        super().__init__(f"grammar is not LL(1), conflicts: {details}")


class ParseTable:
    def __init__(self, grammar: Grammar) -> None:
        self.grammar = grammar
        self.first = grammar.first_sets()
        self.follow = grammar.follow_sets()
        self.entries: dict[str, dict[str, tuple[str, ...]]] = {
            a: {} for a in grammar.nonterminals
        }
        conflicts: list[Conflict] = []
        for lhs, rhss in grammar.rules.items():
            for rhs in rhss:
                first_rhs, nullable_rhs = grammar.first_of_sequence(rhs)
                targets = set(first_rhs)
                if nullable_rhs:
                    targets |= self.follow[lhs]
                for terminal in targets:
                    existing = self.entries[lhs].get(terminal)
                    if existing is not None and existing != rhs:
                        conflicts.append(Conflict(lhs, terminal, existing, rhs))
                    else:
                        self.entries[lhs][terminal] = rhs
        if conflicts:
            raise TableConflictError(conflicts)

    def get(self, nonterminal: str, terminal: str) -> tuple[str, ...] | None:
        return self.entries[nonterminal].get(terminal)

    def expected(self, nonterminal: str) -> tuple[str, ...]:
        """Terminals for which ``nonterminal`` has a table entry."""
        return tuple(sorted(self.entries[nonterminal]))
