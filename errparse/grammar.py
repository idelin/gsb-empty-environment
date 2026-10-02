"""Context-free grammar, FIRST/FOLLOW, and LL(1) predictive table construction."""
from __future__ import annotations

from .tokens import EOF_SYMBOL

EPS = "epsilon"


class Grammar:
    def __init__(self, start, productions):
        self.start = start
        self.productions = {a: [tuple(p) for p in prods] for a, prods in productions.items()}
        if start not in self.productions:
            raise ValueError(f"unknown start symbol {start!r}")
        self.nonterminals = frozenset(self.productions)
        used = {s for prods in self.productions.values() for p in prods for s in p}
        self.terminals = frozenset(used - self.nonterminals)

    def is_terminal(self, sym):
        return sym in self.terminals

    def is_nonterminal(self, sym):
        return sym in self.nonterminals


def first_of_sequence(seq, first, grammar):
    out = set()
    for sym in seq:
        if grammar.is_terminal(sym):
            out.add(sym)
            return out
        out |= first[sym] - {EPS}
        if EPS not in first[sym]:
            return out
    out.add(EPS)
    return out


def compute_first(grammar):
    first = {a: set() for a in grammar.nonterminals}
    changed = True
    while changed:
        changed = False
        for a, prods in grammar.productions.items():
            for p in prods:
                new = first_of_sequence(p, first, grammar)
                if not new <= first[a]:
                    first[a] |= new
                    changed = True
    return first


def compute_follow(grammar, first):
    follow = {a: set() for a in grammar.nonterminals}
    follow[grammar.start].add(EOF_SYMBOL)
    changed = True
    while changed:
        changed = False
        for a, prods in grammar.productions.items():
            for p in prods:
                trailer = set(follow[a])
                for sym in reversed(p):
                    if grammar.is_terminal(sym):
                        trailer = {sym}
                    else:
                        if not trailer <= follow[sym]:
                            follow[sym] |= trailer
                            changed = True
                        trailer = (first[sym] - {EPS}) | (trailer if EPS in first[sym] else set())
    return follow


class TableConflict(Exception):
    def __init__(self, conflicts):
        self.conflicts = conflicts  # list of (nonterminal, terminal, existing, new)
        lines = [f"  {a} / {t!r}: {old} vs {new}" for a, t, old, new in conflicts]
        super().__init__("grammar is not LL(1), conflicts:\n" + "\n".join(lines))


def build_table(grammar):
    """Build the predictive parsing table; raise TableConflict if not LL(1)."""
    first = compute_first(grammar)
    follow = compute_follow(grammar, first)
    table = {a: {} for a in grammar.nonterminals}
    conflicts = []
    for a, prods in grammar.productions.items():
        for p in prods:
            fs = first_of_sequence(p, first, grammar)
            targets = set(fs) - {EPS}
            if EPS in fs:
                targets |= follow[a]
            for t in targets:
                old = table[a].get(t)
                if old is not None and old != p:
                    conflicts.append((a, t, old, p))
                else:
                    table[a][t] = p
    if conflicts:
        raise TableConflict(conflicts)
    return table
