"""Exact total weight of all bounded derivations of a given terminal string.

Multiple derivation trees may yield the same string; the string's
probability is the *sum* of the tree weights.  This module computes that
sum exactly with a CYK-style dynamic program over the same integer-scaled
weights as ``dp.PartitionTable`` (state: nonterminal, span, exact steps,
group mask).  Sampling never deduplicates trees, so it automatically
matches these string probabilities.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Callable, Optional, Sequence

from .dp import PartitionTable, ResourceBudget


def string_mass(
    table: PartitionTable,
    tokens: Sequence,
    steps: Optional[int] = None,
    require_mask: int = 0,
    predicate: Optional[Callable[[int], bool]] = None,
    budget: Optional[ResourceBudget] = None,
) -> Fraction:
    """Exact summed weight of all bounded derivations of ``tokens``."""
    tokens = tuple(tokens)
    n = len(tokens)
    if n > table.L:
        return Fraction(0)
    grammar = table.grammar
    D = table.D
    budget = budget if budget is not None else ResourceBudget()

    dp = {}       # (nt, i, j, d, m) -> int, exact layer d
    dplay = [{} for _ in range(D + 1)]  # dplay[d][nt] -> [(i, j, m, Z)]
    # pref[pi][pos][(i, j, child_steps, m)] -> int, exact child-step slices
    pref = [[dict() for _ in range(len(p.rhs) + 1)] for p in table.productions]

    for d in range(1, D + 1):
        for pi, p in enumerate(table.productions):
            k = len(p.rhs)
            if k == 0:
                continue
            slices = pref[pi]
            if d == 1:
                for i in range(n + 1):  # an empty prefix may start anywhere
                    slices[0][(i, i, 0, 0)] = 1
            for pos, sym in enumerate(p.rhs):
                src, dst = slices[pos], slices[pos + 1]
                if grammar.is_nonterminal(sym):
                    for s1 in range(0, d - 1):
                        d2 = d - 1 - s1
                        entries = dplay[d2].get(sym)
                        if not entries:
                            continue
                        for (i, j, s, m), v in src.items():
                            if s != s1:
                                continue
                            for (i2, j2, m2, z) in entries:
                                budget.tick()
                                if i2 != j:
                                    continue
                                key = (i, j2, d - 1, m | m2)
                                dst[key] = dst.get(key, 0) + v * z
                else:
                    for (i, j, s, m), v in src.items():
                        if s != d - 1:
                            continue
                        budget.tick()
                        if j < n and tokens[j] == sym:
                            key = (i, j + 1, s, m)
                            dst[key] = dst.get(key, 0) + v
        added = {}
        for pi, p in enumerate(table.productions):
            w = table.w[pi]
            if w == 0:
                continue
            bit = table.pbit[pi]
            if len(p.rhs) == 0:
                if d == 1:  # an epsilon tree has exactly one step
                    for i in range(n + 1):
                        budget.tick()
                        key = (p.lhs, i, i, 1, bit)
                        dp[key] = dp.get(key, 0) + w
                        added.setdefault(p.lhs, []).append((i, i, bit, w))
                continue
            for (i, j, s, m), v in pref[pi][len(p.rhs)].items():
                if s != d - 1:
                    continue
                budget.tick()
                nv = w * v
                key = (p.lhs, i, j, d, m | bit)
                dp[key] = dp.get(key, 0) + nv
                added.setdefault(p.lhs, []).append((i, j, m | bit, nv))
        dplay[d] = added

    total = 0
    for (nt, i, j, d, m), z in dp.items():
        if nt != grammar.start or i != 0 or j != n:
            continue
        if steps is not None and d != steps:
            continue
        if m & require_mask != require_mask:
            continue
        if predicate is not None and not predicate(m):
            continue
        total += z * table.Q ** (D - d)
    return Fraction(total, table.Q ** D)
