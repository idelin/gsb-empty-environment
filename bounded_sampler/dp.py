"""Exact partition functions for bounded derivations of a weighted CFG.

State: ``dp[(nt, length, steps, mask)]`` is an integer ``Z`` such that the
exact total weight of complete derivation trees rooted at ``nt`` with yield
length ``length``, EXACTLY ``steps`` production applications and exactly the
group set ``mask`` equals ``Z / Q**steps``, where ``Q`` is the common
denominator of all production weights.  All arithmetic is exact big-integer
arithmetic; rational weights are never converted to floats.

Every production application (including epsilon productions and self-loops
such as ``A -> A``) consumes one step of the budget ``D``, so cyclic and
epsilon-generating grammars have finitely many bounded derivations and the
dynamic program is well founded: layer ``d`` depends only on layers ``< d``.

Sub-problems are shared through the ``dp`` dict; sequence convolutions over
arbitrary right-hand-side lengths use per-production prefix tables
``_pref[pi][pos][(length, child_steps, mask)]`` holding EXACT child-step
slices.  At global layer ``d`` only slices with ``child_steps == d - 1`` are
extended, so each slice is built exactly once.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import gcd
from typing import Callable, Dict, Hashable, List, Optional, Tuple

from .grammar import Grammar


class BudgetExhaustedError(RuntimeError):
    """Raised when a caller-supplied resource budget is exhausted.

    The library never silently truncates the distribution: when the budget
    is insufficient the computation aborts with this error instead of
    returning a partial distribution masquerading as the real one.
    """


@dataclass
class ResourceBudget:
    """Optional accounting of DP work and big-integer size."""

    max_ops: Optional[int] = None
    max_int_bits: Optional[int] = None
    ops: int = 0

    def tick(self, n: int = 1) -> None:
        self.ops += n
        if self.max_ops is not None and self.ops > self.max_ops:
            raise BudgetExhaustedError(
                f"operation budget exhausted ({self.ops} > {self.max_ops})"
            )

    def check_int(self, value: int) -> None:
        if self.max_int_bits is not None and abs(value).bit_length() > self.max_int_bits:
            raise BudgetExhaustedError(
                "big-integer bit-length budget exhausted "
                f"({abs(value).bit_length()} > {self.max_int_bits})"
            )


class PartitionTable:
    """Exact partition functions Z[nt, length, steps, mask] for one grammar."""

    def __init__(
        self,
        grammar: Grammar,
        max_length: int,
        max_steps: int,
        groups: Optional[Tuple[Hashable, ...]] = None,
        budget: Optional[ResourceBudget] = None,
    ) -> None:
        if max_length < 0 or max_steps < 0:
            raise ValueError("max_length and max_steps must be non-negative")
        self.grammar = grammar
        self.L = int(max_length)
        self.D = int(max_steps)
        if groups is None:
            groups = tuple(
                sorted({p.group for p in grammar.productions if p.group is not None}, key=repr)
            )
        self.groups = tuple(groups)
        self.group_bit: Dict[Hashable, int] = {g: 1 << i for i, g in enumerate(self.groups)}
        self.full_mask = (1 << len(self.groups)) - 1
        self.budget = budget if budget is not None else ResourceBudget()

        self.productions = tuple(grammar.productions)
        lhs_order = []
        for p in self.productions:
            if p.lhs not in lhs_order:
                lhs_order.append(p.lhs)
        self.by_lhs: Dict[Hashable, Tuple[int, ...]] = {
            lhs: tuple(pi for pi, p in enumerate(self.productions) if p.lhs == lhs)
            for lhs in lhs_order
        }

        # Scale rational weights to integers: weight_p = w[pi] / Q.
        q = 1
        for p in self.productions:
            q = q * p.weight.denominator // gcd(q, p.weight.denominator)
        self.Q = q
        self.w: List[int] = [int(p.weight * q) for p in self.productions]
        self.pbit: List[int] = [self.group_bit.get(p.group, 0) for p in self.productions]

        self.dp: Dict[Tuple[Hashable, int, int, int], int] = {}
        self._build()

    # ------------------------------------------------------------------ build

    def _build(self) -> None:
        grammar, L, D = self.grammar, self.L, self.D
        dp, budget = self.dp, self.budget
        # pref[pi][pos][(length, child_steps, mask)] -> int (exact child_steps)
        pref: List[List[Dict[Tuple[int, int, int], int]]] = [
            [dict() for _ in range(len(p.rhs) + 1)] for p in self.productions
        ]
        # dplay[d][nt] -> [(length, mask, Z)] for the exact layer d
        dplay: List[Dict[Hashable, List[Tuple[int, int, int]]]] = [{} for _ in range(D + 1)]

        for d in range(1, D + 1):
            # Phase 1: extend prefix convolutions to exact child-step slice d-1.
            for pi, p in enumerate(self.productions):
                k = len(p.rhs)
                if k == 0:
                    continue
                slices = pref[pi]
                if d == 1:
                    slices[0][(0, 0, 0)] = 1
                for pos, sym in enumerate(p.rhs):
                    src, dst = slices[pos], slices[pos + 1]
                    if grammar.is_nonterminal(sym):
                        for s1 in range(0, d - 1):
                            d2 = d - 1 - s1  # >= 1: child steps at exact layer d2
                            entries = dplay[d2].get(sym)
                            if not entries:
                                continue
                            for (l, s, m), v in src.items():
                                if s != s1:
                                    continue
                                for (l2, m2, z) in entries:
                                    budget.tick()
                                    nl = l + l2
                                    if nl > L:
                                        continue
                                    nv = v * z
                                    budget.check_int(nv)
                                    key = (nl, d - 1, m | m2)
                                    dst[key] = dst.get(key, 0) + nv
                    else:
                        # terminals add no steps: propagate the new slice d-1
                        for (l, s, m), v in src.items():
                            if s != d - 1:
                                continue
                            budget.tick()
                            if l + 1 > L:
                                continue
                            key = (l + 1, s, m)
                            dst[key] = dst.get(key, 0) + v

            # Phase 2: aggregate finished productions into exact dp layer d.
            added: Dict[Hashable, List[Tuple[int, int, int]]] = {}
            for pi, p in enumerate(self.productions):
                w = self.w[pi]
                if w == 0:
                    continue
                bit = self.pbit[pi]
                if len(p.rhs) == 0:
                    if d == 1:  # an epsilon tree has exactly one step
                        budget.tick()
                        key = (p.lhs, 0, 1, bit)
                        dp[key] = dp.get(key, 0) + w
                        added.setdefault(p.lhs, []).append((0, bit, w))
                    continue
                for (l, s, m), v in pref[pi][len(p.rhs)].items():
                    if s != d - 1:
                        continue
                    budget.tick()
                    nv = w * v
                    budget.check_int(nv)
                    key = (p.lhs, l, d, m | bit)
                    dp[key] = dp.get(key, 0) + nv
                    added.setdefault(p.lhs, []).append((l, m | bit, nv))
            dplay[d] = added

    # ---------------------------------------------------------------- queries

    def mask_of_groups(self, labels) -> int:
        """Bit mask for an iterable of group labels (KeyError if unknown)."""
        mask = 0
        for g in labels:
            mask |= self.group_bit[g]
        return mask

    def groups_of_mask(self, mask: int) -> Tuple[Hashable, ...]:
        return tuple(g for g in self.groups if mask & self.group_bit[g])

    def cell(self, nt: Hashable, length: int, steps: int, mask: int) -> int:
        """Integer Z; exact value is Z / Q**steps."""
        return self.dp.get((nt, length, steps, mask), 0)

    def root_choices(
        self,
        length: Optional[int] = None,
        steps: Optional[int] = None,
        require_mask: int = 0,
        predicate: Optional[Callable[[int], bool]] = None,
    ) -> List[Tuple[int, int, int, int]]:
        """All (length, steps, mask, Z) start-symbol entries satisfying the condition."""
        out = []
        start = self.grammar.start
        for (nt, l, d, m), z in self.dp.items():
            if nt != start or z == 0:
                continue
            if length is not None and l != length:
                continue
            if steps is not None and d != steps:
                continue
            if m & require_mask != require_mask:
                continue
            if predicate is not None and not predicate(m):
                continue
            out.append((l, d, m, z))
        out.sort()
        return out

    def total_mass(
        self,
        length: Optional[int] = None,
        steps: Optional[int] = None,
        require_mask: int = 0,
        predicate: Optional[Callable[[int], bool]] = None,
    ) -> Fraction:
        """Exact total weight of all bounded start derivations meeting the condition."""
        num = 0
        for (_, d, _, z) in self.root_choices(length, steps, require_mask, predicate):
            num += z * self.Q ** (self.D - d)
        return Fraction(num, self.Q ** self.D)

    def joint_partition(
        self,
        length: Optional[int] = None,
        steps: Optional[int] = None,
        require_mask: int = 0,
        predicate: Optional[Callable[[int], bool]] = None,
    ) -> Dict[Tuple[int, int], Fraction]:
        """Exact partition mass per (length, steps) combination."""
        acc: Dict[Tuple[int, int], int] = {}
        for (l, d, _, z) in self.root_choices(length, steps, require_mask, predicate):
            acc[(l, d)] = acc.get((l, d), 0) + z * self.Q ** (self.D - d)
        return {k: Fraction(v, self.Q ** self.D) for k, v in sorted(acc.items())}
