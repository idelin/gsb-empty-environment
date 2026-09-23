"""Independent structural validator for derivation trees.

The validator shares no code with the sampler or the dynamic program: it
recomputes the yield, length, step count, group set and exact weight of a
tree purely from its structure and the grammar.  Stored bookkeeping fields
on the tree nodes are never trusted.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import FrozenSet, Optional

from .grammar import Grammar, Production
from .tree import TreeNode


@dataclass(frozen=True)
class Validation:
    ok: bool
    reason: str
    length: int
    steps: int
    weight: Fraction
    used_groups: FrozenSet
    terminals: tuple


def validate_tree(
    tree,
    grammar: Grammar,
    max_length: Optional[int] = None,
    max_steps: Optional[int] = None,
) -> Validation:
    """Structurally validate ``tree`` and recompute all its attributes."""
    try:
        length, steps, weight, groups, terminals = _check(tree, grammar, is_root=True)
    except ValueError as exc:
        return Validation(False, str(exc), -1, -1, Fraction(0), frozenset(), ())
    if max_length is not None and length > max_length:
        return Validation(False, f"length {length} exceeds bound {max_length}",
                          length, steps, weight, groups, terminals)
    if max_steps is not None and steps > max_steps:
        return Validation(False, f"steps {steps} exceeds bound {max_steps}",
                          length, steps, weight, groups, terminals)
    return Validation(True, "ok", length, steps, weight, groups, terminals)


def _check(node, grammar: Grammar, is_root: bool):
    if not isinstance(node, TreeNode):
        raise ValueError(f"node is not a TreeNode: {node!r}")
    p = node.production
    if not isinstance(p, Production):
        raise ValueError(f"node production is not a Production: {p!r}")
    if p not in grammar.productions:
        raise ValueError(f"production not in grammar: {p!r}")
    if is_root and p.lhs != grammar.start:
        raise ValueError(f"root lhs {p.lhs!r} is not the start symbol {grammar.start!r}")
    rhs = p.rhs
    nt_positions = [i for i, s in enumerate(rhs) if grammar.is_nonterminal(s)]
    if len(node.children) != len(nt_positions):
        raise ValueError(
            f"children count {len(node.children)} does not match "
            f"{len(nt_positions)} nonterminals on the right-hand side"
        )
    length = 0
    steps = 1
    weight = p.weight
    groups = set()
    if p.group is not None:
        groups.add(p.group)
    terminals = []
    ci = 0
    for i, sym in enumerate(rhs):
        if grammar.is_nonterminal(sym):
            child = node.children[ci]
            ci += 1
            if not isinstance(child, TreeNode) or not isinstance(child.production, Production) \
                    or child.production.lhs != sym:
                raise ValueError(f"child {ci} lhs does not match expected {sym!r}")
            c_len, c_steps, c_weight, c_groups, c_terminals = _check(child, grammar, False)
            length += c_len
            steps += c_steps
            weight *= c_weight
            groups |= c_groups
            terminals.extend(c_terminals)
        else:
            length += 1
            terminals.append(sym)
    return length, steps, weight, frozenset(groups), tuple(terminals)
