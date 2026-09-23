"""Brute-force enumeration of all bounded derivation trees (test reference).

This is an independent, deliberately simple implementation used to verify
the dynamic program: it enumerates every complete derivation tree within
the length and step budgets together with its exact rational weight.  Only
use it on tiny grammars - the number of trees grows explosively.
"""

from __future__ import annotations

import itertools
from dataclasses import dataclass
from fractions import Fraction
from functools import lru_cache
from typing import Optional, Tuple

from .dp import ResourceBudget
from .grammar import Grammar
from .tree import TreeNode


@dataclass(frozen=True)
class EnumTree:
    tree: TreeNode
    weight: Fraction


def enumerate_trees(
    grammar: Grammar,
    max_length: int,
    max_steps: int,
    start=None,
    budget: Optional[ResourceBudget] = None,
) -> Tuple[EnumTree, ...]:
    """All complete derivation trees with length <= max_length and steps <= max_steps."""
    if start is None:
        start = grammar.start
    budget = budget if budget is not None else ResourceBudget()

    @lru_cache(maxsize=None)
    def enum(nt, length, steps):
        out = []
        if steps >= 1:
            for p in grammar.productions_of(nt):
                if p.weight == 0:
                    continue
                rhs = p.rhs
                terms = sum(1 for s in rhs if not grammar.is_nonterminal(s))
                if terms > length:
                    continue
                child_nts = tuple(s for s in rhs if grammar.is_nonterminal(s))
                for combo in _assign(child_nts, length - terms, steps - 1):
                    pools = [enum(c, cl, cd) for (c, cl, cd) in combo]
                    for picked in itertools.product(*pools):
                        children = tuple(et.tree for et in picked)
                        weight = p.weight
                        groups = set()
                        if p.group is not None:
                            groups.add(p.group)
                        for et in picked:
                            weight *= et.weight
                            groups |= et.tree.used_groups
                        node = TreeNode(
                            production=p, children=children, length=length,
                            steps=steps, used_groups=frozenset(groups),
                        )
                        out.append(EnumTree(node, weight))
                        budget.tick()
        return tuple(out)

    def _assign(nts, length, steps):
        if not nts:
            if length == 0 and steps == 0:
                yield ()
            return
        first, rest = nts[0], nts[1:]
        for l1 in range(length + 1):
            for d1 in range(1, steps - len(rest) + 1):
                if not enum(first, l1, d1):
                    continue
                for tail in _assign(rest, length - l1, steps - d1):
                    yield ((first, l1, d1),) + tail

    result = []
    for l in range(max_length + 1):
        for d in range(1, max_steps + 1):
            result.extend(enum(start, l, d))
    return tuple(result)
