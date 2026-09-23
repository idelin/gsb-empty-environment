"""Independent verification for bounded_cfg, by brute-force enumeration.

This module deliberately shares no code with :mod:`bounded_cfg`.  It
enumerates *every* complete derivation tree inside the (length, steps)
bounds with plain recursion, computes each tree's weight directly as a
product of production weights, and aggregates.  It is exponentially slow
and only meant for tiny grammars -- that is exactly what makes it a
trustworthy cross-check of the dynamic-programming sampler.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Hashable, Iterable


def enumerate_trees(grammar, max_length: int, max_steps: int, start: str | None = None):
    """Yield ``(tree, weight, groups)`` for every bounded complete tree.

    ``tree`` is a nested tuple ``(lhs, ((sym, subtree_or_None), ...))``;
    ``weight`` is the exact product of production weights; ``groups`` is
    the frozenset of production groups used anywhere in the tree.  Only
    trees consuming at most ``max_length`` terminals and at most
    ``max_steps`` production applications are yielded.
    """

    start = grammar.start if start is None else start

    def enum_exact(nt: str, length: int, steps: int):
        """Trees rooted at nt consuming exactly (length, steps)."""
        if steps < 1:
            return
        for prod in grammar.by_nt.get(nt, ()):
            yield from enum_rhs(prod, 0, length, steps - 1, (),
                                Fraction(1), frozenset())

    def enum_rhs(prod, i, length, steps, acc_children, acc_w, acc_groups):
        if i == len(prod.rhs):
            if length == 0 and steps == 0:
                groups = acc_groups | (
                    {prod.group} if prod.group is not None else frozenset()
                )
                yield (prod.lhs, tuple(acc_children)), acc_w * prod.weight, groups
            return
        sym = prod.rhs[i]
        if sym in grammar.nonterminals:
            for child_len in range(length + 1):
                for child_steps in range(steps + 1):
                    for tree, w, groups in enum_exact(sym, child_len, child_steps):
                        yield from enum_rhs(
                            prod, i + 1, length - child_len, steps - child_steps,
                            acc_children + ((sym, tree),), acc_w * w,
                            acc_groups | groups,
                        )
        else:
            if length >= 1:
                yield from enum_rhs(
                    prod, i + 1, length - 1, steps,
                    acc_children + ((sym, None),), acc_w, acc_groups,
                )

    for length in range(max_length + 1):
        for steps in range(max_steps + 1):
            for tree, w, groups in enum_exact(start, length, steps):
                yield tree, w, groups


def tree_yield(tree) -> tuple:
    out = []

    def walk(node):
        _, children = node
        for sym, sub in children:
            if sub is None:
                out.append(sym)
            else:
                walk(sub)

    walk(tree)
    return tuple(out)


def tree_steps(tree) -> int:
    _, children = tree
    return 1 + sum(tree_steps(sub) for _, sub in children if sub is not None)


def partition_by_enumeration(grammar, max_length, max_steps,
                             required_groups: Iterable[Hashable] = ()):
    """Exact Z[length, steps] from brute-force enumeration."""

    required = frozenset(required_groups)
    grid = {}
    for tree, w, groups in enumerate_trees(grammar, max_length, max_steps):
        if required and not required <= groups:
            continue
        key = (len(tree_yield(tree)), tree_steps(tree))
        grid[key] = grid.get(key, Fraction(0)) + w
    return grid


def string_weights(grammar, max_length, max_steps,
                   required_groups: Iterable[Hashable] = ()):
    """Exact total weight per terminal string (sums over ambiguous trees)."""

    required = frozenset(required_groups)
    out = {}
    for tree, w, groups in enumerate_trees(grammar, max_length, max_steps):
        if required and not required <= groups:
            continue
        key = tree_yield(tree)
        out[key] = out.get(key, Fraction(0)) + w
    return out
