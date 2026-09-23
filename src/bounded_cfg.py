"""Exact bounded-derivation sampling for weighted context-free grammars.

The target distribution lives on *complete derivation trees*:

* a tree's weight is the product of its production weights (exact rationals);
* only trees whose terminal yield has length ``<= L`` and which use
  ``<= D`` production applications are admitted (epsilon productions cost
  one application, so epsilon loops stay inside a finite, computable domain);
* conditional probabilities are tree weights divided by the exact partition
  function of the admitted domain.

No rational weight is ever converted to a float.  Weighted choices use exact
integer cumulative sums after scaling fractions by a common denominator.
Ambiguous strings keep every distinct tree: the library samples trees, never
"deduplicates" strings, so a string with k derivations is naturally sampled
with the sum of the k tree weights.

Resource limits
---------------
DP state count is at most ``|N| (L+1) (D+1) 2**G`` where G is the number of
tracked production groups.  Building the tables performs, per production and
derivation depth, prefix convolutions over the length budget, the step
budget and the group-mask lattice (roughly O(k * L^2 * D^2 * 4**G) pair
operations per production for a RHS of length k).  Stored fractions are
products of up to D weights, so their bit length grows like O(D * b) where b
is the largest numerator/denominator bit length of the input weights.

``max_work`` bounds the number of convolution pair operations.  When the
budget is exhausted the constructor raises :class:`BudgetExceeded` *instead
of* returning partial tables: a truncated result would not be the exact
distribution.  Sampling has its own ``max_sample_work`` because enumerating
the candidate allocations at a DP cell must also be exhaustive to remain
unbiased.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from fractions import Fraction
from typing import Callable, Hashable, Iterable, Mapping, Sequence

__all__ = [
    "Production",
    "Node",
    "Grammar",
    "SampleResult",
    "BoundedModel",
    "GrammarError",
    "BudgetExceeded",
    "yield_of",
    "steps_of",
    "tree_weight",
]


class GrammarError(ValueError):
    """Raised when a grammar or query is malformed."""


class BudgetExceeded(RuntimeError):
    """Raised when an exact computation would exceed its work budget."""


@dataclass(frozen=True)
class Production:
    """A single weighted production ``lhs -> rhs``.

    ``rhs`` is a (possibly empty) tuple of symbols; the empty tuple is the
    epsilon production.  ``group`` optionally assigns the production to a
    named group for conditional queries.
    """

    lhs: str
    rhs: tuple[str, ...]
    weight: Fraction
    group: Hashable | None = None
    pid: int = field(default=-1, compare=False)

    def __post_init__(self) -> None:
        if not isinstance(self.lhs, str) or not self.lhs:
            raise GrammarError("production lhs must be a non-empty string")
        if not isinstance(self.rhs, tuple) or not all(
            isinstance(sym, str) and sym for sym in self.rhs
        ):
            raise GrammarError("production rhs must be a tuple of non-empty strings")
        if not isinstance(self.weight, Fraction):
            object.__setattr__(self, "weight", Fraction(self.weight))
        if self.weight < 0:
            raise GrammarError("production weights must be non-negative")


@dataclass(frozen=True)
class Node:
    """A complete derivation tree.

    ``children`` is aligned with ``production.rhs``: for a nonterminal slot
    it holds the child :class:`Node`, for a terminal slot it holds the
    terminal symbol itself.
    """

    production: Production
    children: tuple[object, ...]


@dataclass(frozen=True)
class SampleResult:
    """One exact sample from a bounded conditional tree distribution."""

    tree: Node
    terminals: tuple[str, ...]
    length: int
    steps: int
    groups: frozenset
    weight: Fraction
    probability: Fraction


class Grammar:
    """A CFG with non-negative rational production weights."""

    def __init__(
        self,
        productions: Iterable[Production],
        start: str,
        terminals: Iterable[str] | None = None,
    ) -> None:
        indexed: list[Production] = []
        for pid, prod in enumerate(productions):
            if not isinstance(prod, Production):
                raise GrammarError("productions must be Production instances")
            if prod.pid != -1:
                raise GrammarError("pid is assigned by the grammar")
            indexed.append(
                Production(prod.lhs, prod.rhs, prod.weight, prod.group, pid)
            )
        if not indexed:
            raise GrammarError("grammar needs at least one production")
        self.productions: tuple[Production, ...] = tuple(indexed)
        self.nonterminals: frozenset = frozenset(p.lhs for p in self.productions)
        if start not in self.nonterminals:
            raise GrammarError(f"start symbol {start!r} is not a nonterminal")
        self.start = start

        seen_terminals: set = set()
        for prod in self.productions:
            for sym in prod.rhs:
                if sym not in self.nonterminals:
                    seen_terminals.add(sym)
        declared = None if terminals is None else frozenset(terminals)
        if declared is not None and not seen_terminals <= declared:
            raise GrammarError(
                f"undeclared terminals used in rhs: {sorted(seen_terminals - declared)}"
            )
        self.terminals: frozenset = (
            frozenset(seen_terminals) if declared is None else declared
        )
        if self.nonterminals & self.terminals:
            raise GrammarError("a symbol cannot be both terminal and nonterminal")

        self.by_nt: dict = {nt: [] for nt in self.nonterminals}
        for prod in self.productions:
            self.by_nt[prod.lhs].append(prod)

    def is_terminal(self, sym: str) -> bool:
        return sym in self.terminals

    def reachable_nonterminals(self) -> frozenset:
        seen = {self.start}
        stack = [self.start]
        while stack:
            nt = stack.pop()
            for prod in self.by_nt.get(nt, ()):
                for sym in prod.rhs:
                    if sym in self.nonterminals and sym not in seen:
                        seen.add(sym)
                        stack.append(sym)
        return frozenset(seen)


def yield_of(tree: Node, grammar: Grammar) -> tuple:
    """Terminal yield of a derivation tree."""

    out: list = []

    def walk(node: Node) -> None:
        for sym, child in zip(node.production.rhs, node.children):
            if sym in grammar.nonterminals:
                if not isinstance(child, Node):
                    raise GrammarError("expected a child subtree")
                walk(child)
            else:
                if child != sym:
                    raise GrammarError("terminal slot must hold that terminal")
                out.append(sym)

    walk(tree)
    return tuple(out)


def steps_of(tree: Node) -> int:
    """Number of production applications in a tree (epsilon included)."""

    total = 1
    for child in tree.children:
        if isinstance(child, Node):
            total += steps_of(child)
    return total


def tree_weight(tree: Node) -> Fraction:
    """Exact product of production weights in a tree."""

    total = tree.production.weight
    for child in tree.children:
        if isinstance(child, Node):
            total *= tree_weight(child)
    return total


Cell = tuple  # (yield length, production applications, group mask)

_TERMINAL_CELL: Mapping = {(1, 0, 0): Fraction(1)}


class _WorkMeter:
    __slots__ = ("remaining", "limit")

    def __init__(self, limit: int) -> None:
        if not isinstance(limit, int) or limit <= 0:
            raise GrammarError("work budget must be a positive int")
        self.remaining = limit
        self.limit = limit

    def tick(self, amount: int = 1) -> None:
        self.remaining -= amount
        if self.remaining <= 0:
            raise BudgetExceeded(
                f"exact computation exceeded work budget of {self.limit} operations"
            )


def _randbelow(rng: object, n: int) -> int:
    if callable(rng):
        value = rng(n)
    elif hasattr(rng, "randbelow"):
        value = rng.randbelow(n)
    elif hasattr(rng, "randrange"):
        value = rng.randrange(n)
    else:
        raise GrammarError(
            "rng must be callable rng(n), or provide randbelow(n)/randrange(n)"
        )
    if not isinstance(value, int) or isinstance(value, bool) or not 0 <= value < n:
        raise GrammarError("random integer source returned an out-of-range value")
    return value


def _exact_choice(items: Sequence, rng: object):
    """Choose a key proportionally to exact Fraction weights."""

    positive = [(key, weight) for key, weight in items if weight > 0]
    if not positive:
        raise GrammarError("cannot sample from an empty domain")
    scale = math.lcm(*(weight.denominator for _, weight in positive))
    scaled = [
        (key, weight.numerator * (scale // weight.denominator))
        for key, weight in positive
    ]
    total = sum(weight for _, weight in scaled)
    needle = _randbelow(rng, total)
    cumulative = 0
    for key, weight in scaled:
        cumulative += weight
        if needle < cumulative:
            return key, Fraction(weight, scale)
    raise AssertionError("unreachable")  # pragma: no cover


class BoundedModel:
    """Exact DP tables and sampler for a bounded derivation domain."""

    def __init__(
        self,
        grammar: Grammar,
        max_length: int,
        max_steps: int,
        tracked_groups: Iterable = (),
        max_work: int = 10_000_000,
        max_sample_work: int = 2_000_000,
    ) -> None:
        if not isinstance(grammar, Grammar):
            raise GrammarError("expected a Grammar")
        if not isinstance(max_length, int) or max_length < 0:
            raise GrammarError("max_length must be a non-negative int")
        if not isinstance(max_steps, int) or max_steps < 0:
            raise GrammarError("max_steps must be a non-negative int")
        self.grammar = grammar
        self.max_length = max_length
        self.max_steps = max_steps
        self.tracked_groups: tuple = tuple(tracked_groups)
        if len(set(self.tracked_groups)) != len(self.tracked_groups):
            raise GrammarError("tracked group names must be unique")
        self.group_bit: dict = {
            name: 1 << i for i, name in enumerate(self.tracked_groups)
        }
        self.max_work = max_work
        self.max_sample_work = max_sample_work

        self.table: dict = {nt: {} for nt in grammar.nonterminals}
        self._build()

    # ------------------------------------------------------------------ DP

    def _prod_bit(self, prod: Production) -> int:
        return self.group_bit.get(prod.group, 0)

    def _slot_table(self, sym: str) -> Mapping:
        if sym in self.grammar.nonterminals:
            return self.table[sym]
        return _TERMINAL_CELL

    @staticmethod
    def _combine(
        left: Mapping,
        right: Mapping,
        cap_length: int,
        cap_steps: int,
        work: _WorkMeter,
    ) -> dict:
        """Budget convolution of two RHS-prefix weight tables."""

        out: dict = {}
        ticks = 0
        for (ll, ls, lm), lw in left.items():
            for (rl, rs, rm), rw in right.items():
                ticks += 1
                if ticks >= 256:
                    work.tick(ticks)
                    ticks = 0
                length = ll + rl
                steps = ls + rs
                if length <= cap_length and steps <= cap_steps:
                    key = (length, steps, lm | rm)
                    out[key] = out.get(key, Fraction(0)) + lw * rw
        if ticks:
            work.tick(ticks)
        return out

    def _build(self) -> None:
        work = _WorkMeter(self.max_work)
        grammar = self.grammar
        for depth in range(1, self.max_steps + 1):
            per_nt: dict = {}
            for nt in grammar.nonterminals:
                added: dict = {}
                child_step_budget = depth - 1
                for prod in grammar.by_nt[nt]:
                    if prod.weight == 0:
                        continue
                    acc: Mapping = {(0, 0, 0): Fraction(1)}
                    for sym in prod.rhs:
                        acc = self._combine(
                            acc,
                            self._slot_table(sym),
                            self.max_length,
                            child_step_budget,
                            work,
                        )
                        if not acc:
                            break
                    bit = self._prod_bit(prod)
                    for (length, steps, mask), weight in acc.items():
                        if steps != child_step_budget:
                            continue
                        key = (length, depth, mask | bit)
                        added[key] = added.get(key, Fraction(0)) + prod.weight * weight
                per_nt[nt] = added
            for nt, added in per_nt.items():
                self.table[nt].update(added)

    # ------------------------------------------------------------- queries

    def _required_mask(self, groups: Iterable) -> int:
        mask = 0
        for name in groups:
            if name not in self.group_bit:
                raise GrammarError(
                    f"group {name!r} is not tracked by this model; rebuild with it "
                    "in tracked_groups"
                )
            mask |= self.group_bit[name]
        return mask

    def _domain_entries(
        self,
        length: int | None,
        steps: int | None,
        required_mask: int,
    ) -> list:
        if length is not None and not 0 <= length <= self.max_length:
            raise GrammarError("requested length is outside [0, max_length]")
        if steps is not None and not 0 <= steps <= self.max_steps:
            raise GrammarError("requested steps are outside [0, max_steps]")
        entries: list = []
        for cell, weight in self.table[self.grammar.start].items():
            cell_length, cell_steps, mask = cell
            if length is not None and cell_length != length:
                continue
            if steps is not None and cell_steps != steps:
                continue
            if mask & required_mask != required_mask:
                continue
            entries.append((cell, weight))
        return entries

    def partition(
        self,
        length: int | None = None,
        steps: int | None = None,
        required_groups: Iterable = (),
    ) -> Fraction:
        """Exact partition weight of the (possibly conditional) domain."""

        required_mask = self._required_mask(required_groups)
        return sum(
            (weight for _, weight in self._domain_entries(length, steps, required_mask)),
            Fraction(0),
        )

    def partition_by_length_steps(self, required_groups: Iterable = ()) -> dict:
        """Exact Z[length, steps], summed over group masks."""

        required_mask = self._required_mask(required_groups)
        grid = {
            (length, steps): Fraction(0)
            for length in range(self.max_length + 1)
            for steps in range(self.max_steps + 1)
        }
        for (length, steps, mask), weight in self.table[self.grammar.start].items():
            if mask & required_mask == required_mask:
                grid[(length, steps)] += weight
        return grid

    # ------------------------------------------------------------ sampling

    def sample(
        self,
        rng: object,
        length: int | None = None,
        steps: int | None = None,
        required_groups: Iterable = (),
    ) -> SampleResult | None:
        """Draw one exact sample; ``None`` means the domain is empty.

        ``rng`` is the caller's random integer source: either a callable
        ``rng(n) -> int in [0, n)`` or an object with ``randbelow(n)``
        (e.g. ``random.Random``).  The draw is unbiased with respect to the
        exact rational weights; no floats are involved.
        """

        required_mask = self._required_mask(required_groups)
        entries = self._domain_entries(length, steps, required_mask)
        entries = [(cell, w) for cell, w in entries if w > 0]
        if not entries:
            return None
        total = sum((w for _, w in entries), Fraction(0))
        cell, _ = _exact_choice(entries, rng)
        work = _WorkMeter(self.max_sample_work)
        tree = self._sample_tree(self.grammar.start, cell, rng, work)
        weight = tree_weight(tree)
        return SampleResult(
            tree=tree,
            terminals=yield_of(tree, self.grammar),
            length=cell[0],
            steps=cell[1],
            groups=frozenset(
                name
                for name, bit in self.group_bit.items()
                if cell[2] & bit
            ),
            weight=weight,
            probability=weight / total,
        )

    def _sample_tree(self, nt: str, cell: Cell, rng: object, work: _WorkMeter) -> Node:
        length, steps, mask = cell
        target = self.table[nt].get(cell, Fraction(0))
        if target <= 0:
            raise GrammarError(f"no derivation of {nt!r} for cell {cell}")

        # Choose a production of nt proportionally to its contribution.
        options: list = []
        child_budget = steps - 1
        for prod in self.grammar.by_nt[nt]:
            if prod.weight == 0:
                continue
            bit = self._prod_bit(prod)
            # The production's own bit is OR-ed into the cell mask, so it
            # must already be a subset of `mask`.
            if bit & ~mask:
                continue
            contrib = self._production_contrib(prod, length, child_budget, mask, work)
            if contrib > 0:
                options.append((prod, prod.weight * contrib))
        if not options:
            raise GrammarError(f"no production options for {nt!r} at {cell}")
        prod, _ = _exact_choice(options, rng)

        # Choose an allocation of (length, steps, mask) across the RHS slots.
        # Children may use any bit of ``mask`` (including this production's
        # own group bit); they must cover the bits the production itself
        # does not contribute.
        bit = self._prod_bit(prod)
        alloc = self._sample_allocation(
            prod, length, child_budget, mask, mask & ~bit, rng, work
        )
        children: list = []
        for sym, child_cell in zip(prod.rhs, alloc):
            if sym in self.grammar.nonterminals:
                children.append(self._sample_tree(sym, child_cell, rng, work))
            else:
                children.append(sym)
        return Node(prod, tuple(children))

    def _production_contrib(
        self,
        prod: Production,
        length: int,
        child_budget: int,
        mask: int,
        work: _WorkMeter,
    ) -> Fraction:
        """Total weight of RHS allocations whose OR-mask plus prod's bit is mask."""

        bit = self._prod_bit(prod)
        total = Fraction(0)
        for _, weight in self._allocations(
            prod.rhs, length, child_budget, mask, mask & ~bit, work
        ):
            total += weight
        return total

    def _allocations(
        self,
        rhs: tuple,
        length: int,
        steps: int,
        allowed: int,
        needed: int,
        work: _WorkMeter,
    ) -> list:
        """All slot allocations with their exact combined weights.

        Each item is ``(cells, weight)`` where ``cells`` aligns with ``rhs``
        (terminal slots carry the fixed cell ``(1, 0, 0)``).

        ``allowed`` bounds which group bits children may use; ``needed`` is
        the set of bits the children must still cover between them.  A child
        may use any allowed bit (bits may be shared between children -- the
        cell mask is an OR), so ``allowed`` stays fixed while ``needed``
        shrinks as bits get covered.
        """

        results: list = []

        def rec(i: int, rem_len: int, rem_steps: int, need: int,
                cells: list, acc_w: Fraction) -> None:
            work.tick()
            if i == len(rhs):
                if rem_len == 0 and rem_steps == 0 and need == 0:
                    results.append((tuple(cells), acc_w))
                return
            sym = rhs[i]
            if sym in self.grammar.terminals:
                if rem_len >= 1:
                    cells.append((1, 0, 0))
                    rec(i + 1, rem_len - 1, rem_steps, need, cells, acc_w)
                    cells.pop()
                return
            for (cl, cs, cm), cw in self.table[sym].items():
                if cl > rem_len or cs > rem_steps:
                    continue
                if cm & ~allowed:
                    continue
                cells.append((cl, cs, cm))
                rec(i + 1, rem_len - cl, rem_steps - cs, need & ~cm,
                    cells, acc_w * cw)
                cells.pop()

        rec(0, length, steps, needed, [], Fraction(1))
        return results

    def _sample_allocation(
        self,
        prod: Production,
        length: int,
        steps: int,
        allowed: int,
        needed: int,
        rng: object,
        work: _WorkMeter,
    ) -> tuple:
        allocs = self._allocations(prod.rhs, length, steps, allowed, needed, work)
        if not allocs:
            raise GrammarError("no feasible allocation for chosen production")
        weighted = [(cells, weight) for cells, weight in allocs if weight > 0]
        cells, _ = _exact_choice(weighted, rng)
        return cells
