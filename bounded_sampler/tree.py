"""Derivation tree representation and weight computation.

A ``TreeNode`` is a complete derivation tree: every node applies one
production, leaves are implicit (terminals appear in ``production.rhs``).
``length`` and ``steps`` are stored for convenience; the independent
validator recomputes them from structure instead of trusting the fields.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import FrozenSet, Hashable, Tuple

from .grammar import Production


@dataclass(frozen=True)
class TreeNode:
    production: Production
    children: Tuple["TreeNode", ...] = ()
    length: int = 0
    steps: int = 1
    used_groups: FrozenSet[Hashable] = field(default_factory=frozenset)


def tree_yield(node: TreeNode, grammar) -> Tuple:
    """Terminals of the yield, in order."""
    out = []
    ci = 0
    for sym in node.production.rhs:
        if grammar.is_nonterminal(sym):
            out.extend(tree_yield(node.children[ci], grammar))
            ci += 1
        else:
            out.append(sym)
    return tuple(out)


def tree_weight(node: TreeNode) -> Fraction:
    """Product of the weights of all productions used in the tree."""
    w = node.production.weight
    for child in node.children:
        w *= tree_weight(child)
    return w
