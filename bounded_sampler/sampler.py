"""Unbiased exact sampling of bounded derivation trees.

Sampling consumes randomness only through a caller-supplied uniform integer
source ``randbelow(n) -> int in [0, n)``.  All decisions compare big-integer
masses against a single uniform draw; rational weights are never converted
to floating-point probabilities.

At every node the alternatives (production + split of the remaining length /
step / group-mask budget among the right-hand-side nonterminals) all live in
the same step layer ``d``, hence share the denominator ``Q**d`` and their
integer masses are directly comparable.  The root alternatives are rescaled
to the common denominator ``Q**D``.  This makes the sampler exactly
proportional to the target distribution - no rejection sampling, so cyclic
grammars cannot cause unbounded waiting.
"""

from __future__ import annotations

import random
from dataclasses import dataclass
from fractions import Fraction
from typing import Callable, Hashable, Optional, Tuple

from .dp import PartitionTable, ResourceBudget
from .tree import TreeNode, tree_weight, tree_yield


@dataclass(frozen=True)
class Sample:
    """One sampled derivation tree with its exact probability."""

    tree: TreeNode
    terminals: tuple
    steps: int
    probability: Fraction


def randbelow_from_random(rng: random.Random) -> Callable[[int], int]:
    """Adapt a ``random.Random`` instance to the required randbelow(n) interface."""
    return rng.randrange


def _weighted_index(masses, r: int) -> int:
    acc = 0
    for i, m in enumerate(masses):
        acc += m
        if r < acc:
            return i
    raise AssertionError("random integer source returned an out-of-range value")


class BoundedSampler:
    """Draws derivation trees exactly from the bounded weight distribution."""

    def __init__(
        self,
        table: PartitionTable,
        randbelow: Callable[[int], int],
        budget: Optional[ResourceBudget] = None,
    ) -> None:
        self.table = table
        self.randbelow = randbelow
        self.budget = budget if budget is not None else ResourceBudget()

    # ------------------------------------------------------------- public

    def sample(
        self,
        length: Optional[int] = None,
        steps: Optional[int] = None,
        require_groups: Tuple[Hashable, ...] = (),
        require_mask: int = 0,
        predicate: Optional[Callable[[int], bool]] = None,
    ) -> Optional[Sample]:
        """Sample one tree, or return None when no feasible tree exists.

        ``require_groups`` / ``require_mask`` restrict to trees using every
        listed group at least once; the condition is part of the DP state,
        so the conditional distribution is sampled directly.
        """
        table = self.table
        mask = require_mask | table.mask_of_groups(require_groups)
        choices = table.root_choices(length=length, steps=steps,
                                     require_mask=mask, predicate=predicate)
        if not choices:
            return None
        Q, D = table.Q, table.D
        masses = [z * Q ** (D - d) for (_, d, _, z) in choices]
        total = sum(masses)
        l0, d0, m0, _ = choices[_weighted_index(masses, self.randbelow(total))]
        tree = self._build(table.grammar.start, l0, d0, m0)
        probability = tree_weight(tree) / Fraction(total, Q ** D)
        return Sample(
            tree=tree,
            terminals=tree_yield(tree, table.grammar),
            steps=d0,
            probability=probability,
        )

    # ------------------------------------------------------------ internal

    def _build(self, nt, length: int, steps: int, mask: int) -> TreeNode:
        table = self.table
        grammar = table.grammar
        options = []  # (mass, production, splits)
        for pi in table.by_lhs.get(nt, ()):
            w = table.w[pi]
            if w == 0:
                continue
            p = table.productions[pi]
            bit = table.pbit[pi]
            rhs = p.rhs
            if not rhs:
                if steps == 1 and length == 0 and (mask & ~bit) == 0:
                    options.append((w, p, ()))
                continue
            child_nts = tuple(s for s in rhs if grammar.is_nonterminal(s))
            terms = len(rhs) - len(child_nts)
            if terms > length:
                continue
            # children may use any group already in ``mask`` (union is
            # idempotent); together they must cover ``mask & ~bit``.
            for splits in self._splits(child_nts, length - terms, steps - 1,
                                       mask, mask & ~bit, 0):
                mass = w
                for (c_nt, c_l, c_d, c_m) in splits:
                    mass *= table.dp[(c_nt, c_l, c_d, c_m)]
                options.append((mass, p, splits))
        if not options:
            raise AssertionError(f"infeasible DP state {(nt, length, steps, mask)}")
        self.budget.tick()
        masses = [o[0] for o in options]
        _, prod, splits = options[_weighted_index(masses, self.randbelow(sum(masses)))]
        children = tuple(self._build(c_nt, c_l, c_d, c_m) for (c_nt, c_l, c_d, c_m) in splits)
        groups = set()
        if prod.group is not None:
            groups.add(prod.group)
        for c in children:
            groups |= c.used_groups
        return TreeNode(production=prod, children=children,
                        length=length, steps=steps, used_groups=frozenset(groups))

    def _splits(self, child_nts, rem_l: int, rem_d: int, allowed: int,
                required: int, covered: int):
        """Yield budget splits ``((nt, l, d, m), ...)`` for the child sequence.

        Child group masks may overlap (two subtrees can both use the same
        group); each must be contained in ``allowed`` and their union must
        cover ``required``.  ``covered`` is the union accumulated so far.
        """
        table = self.table
        if not child_nts:
            if rem_l == 0 and rem_d == 0 and (covered & required) == required:
                yield ()
            return
        nt, rest = child_nts[0], child_nts[1:]
        for (ent, l, d, m), z in table.dp.items():
            self.budget.tick()
            if ent != nt or z == 0:
                continue
            if l > rem_l or d > rem_d or (m & ~allowed):
                continue
            for tail in self._splits(rest, rem_l - l, rem_d - d,
                                     allowed, required, covered | m):
                yield ((nt, l, d, m),) + tail
