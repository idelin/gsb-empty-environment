"""Grammar definitions for exact bounded-derivation sampling.

A grammar is a context-free grammar whose productions carry non-negative
rational weights (``fractions.Fraction``).  Symbols are arbitrary hashable
objects (strings recommended); a symbol is a nonterminal iff it appears on
the left-hand side of some production.  The empty right-hand side ``()``
denotes an epsilon production.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Any, Hashable, Optional, Tuple


@dataclass(frozen=True)
class Production:
    """A single weighted production ``lhs -> rhs``.

    ``rhs`` is a tuple of symbols; ``rhs == ()`` is an epsilon production.
    ``weight`` must be a non-negative rational.  ``group`` is an optional
    hashable label used for conditional queries ("at least one production of
    group g was used"); productions without a label never set any group bit.
    """

    lhs: Hashable
    rhs: Tuple[Hashable, ...]
    weight: Fraction
    group: Optional[Hashable] = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "rhs", tuple(self.rhs))
        object.__setattr__(self, "weight", Fraction(self.weight))
        if self.weight < 0:
            raise ValueError(f"negative production weight: {self.weight}")

    @property
    def is_epsilon(self) -> bool:
        return len(self.rhs) == 0


@dataclass(frozen=True)
class Grammar:
    """A weighted context-free grammar."""

    start: Hashable
    productions: Tuple[Production, ...] = field(default_factory=tuple)

    @staticmethod
    def create(start: Hashable, productions) -> "Grammar":
        prods = tuple(productions)
        nonterminals = frozenset(p.lhs for p in prods)
        if start not in nonterminals:
            raise ValueError(f"start symbol {start!r} has no productions")
        return Grammar(start=start, productions=prods)

    @property
    def nonterminals(self):
        return frozenset(p.lhs for p in self.productions)

    def is_nonterminal(self, sym: Any) -> bool:
        return sym in self.nonterminals

    def productions_of(self, lhs: Hashable) -> Tuple[Production, ...]:
        return tuple(p for p in self.productions if p.lhs == lhs)
