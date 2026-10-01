"""Plain from-scratch fixpoint computation of nullable/FIRST/FOLLOW.

This is the independent reference implementation used to cross-check the
incremental engine after every random modification.
"""
from __future__ import annotations

from collections import defaultdict

from .symbols import END


def compute_sets(productions, start):
    """Return (nullable, first, follow) for the given active productions."""
    nullable = set()
    first = defaultdict(set)
    follow = defaultdict(set)
    if start is not None:
        follow[start].add(END)

    changed = True
    while changed:
        changed = False
        for prod in productions:
            rhs = prod.rhs
            if all(s.is_nonterminal and s in nullable for s in rhs):
                if prod.lhs not in nullable:
                    nullable.add(prod.lhs)
                    changed = True
            for sym in rhs:
                if sym.is_terminal:
                    if sym not in first[prod.lhs]:
                        first[prod.lhs].add(sym)
                        changed = True
                    break
                before = len(first[prod.lhs])
                first[prod.lhs] |= first[sym]
                if len(first[prod.lhs]) != before:
                    changed = True
                if sym not in nullable:
                    break
            for i, sym in enumerate(rhs):
                if sym.is_terminal:
                    continue
                rest_nullable = True
                for nxt in rhs[i + 1:]:
                    if nxt.is_terminal:
                        if nxt not in follow[sym]:
                            follow[sym].add(nxt)
                            changed = True
                        rest_nullable = False
                        break
                    before = len(follow[sym])
                    follow[sym] |= first[nxt]
                    if len(follow[sym]) != before:
                        changed = True
                    if nxt not in nullable:
                        rest_nullable = False
                        break
                if rest_nullable:
                    before = len(follow[sym])
                    follow[sym] |= follow[prod.lhs]
                    if len(follow[sym]) != before:
                        changed = True
    return (nullable,
            {k: v for k, v in first.items() if v},
            {k: v for k, v in follow.items() if v})
