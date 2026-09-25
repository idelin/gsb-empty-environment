"""Exact proof-subgraph search over a finite fact/rule graph.

The search space is the set of proof DAGs ("options") for each goal.  An
option is a frozenset of items: ("fact", id) and ("rule", id).  Sharing
between subproofs is accounted for by set union, so a fact or rule shared
by several goals (or several branches of one proof) is only counted once.

Objective (lexicographic):
  1. minimise the number of *public* facts in the option (least disclosure)
  2. minimise the package size (facts + rules + steps; steps are 1:1 rules)

The enumerator is exact: for every fact it keeps every option that is not
strictly dominated by set inclusion.  Cost is monotone under set inclusion,
so dropping supersets never changes the optimum.  Cyclic branches are cut
(a minimal proof is always acyclic), which keeps the search finite and
exact on any finite graph.

If the number of enumerated combinations exceeds ``budget`` the search
stops and returns an *undecided* result carrying component-wise lower and
upper bounds instead of an exact optimum.
"""
from __future__ import annotations

from dataclasses import dataclass
from itertools import product

FACT = "fact"
RULE = "rule"

DEFAULT_BUDGET = 200_000


class BudgetExceeded(Exception):
    """Internal control-flow signal: enumeration budget exhausted."""


class UnprovableError(ValueError):
    def __init__(self, goal: str):
        self.goal = goal
        super().__init__(f"goal is not provable from the knowledge base: {goal!r}")


@dataclass(frozen=True, order=True)
class Cost:
    public_facts: int
    size: int  # facts + rules + steps (steps are 1:1 with rules)

    def as_tuple(self) -> tuple[int, int]:
        return (self.public_facts, self.size)


def cost_of(items: frozenset, kb) -> Cost:
    public = 0
    facts = 0
    rules = 0
    for kind, ident in items:
        if kind == FACT:
            facts += 1
            if kb.facts[ident].public:
                public += 1
        else:
            rules += 1
    return Cost(public, facts + 2 * rules)


def _sort_key(items: frozenset, kb) -> tuple:
    cost = cost_of(items, kb)
    return (cost.public_facts, cost.size, sorted(items))


def _prune(options: list) -> list:
    """Drop every option that is a strict superset of another option."""
    unique = sorted(set(options), key=lambda o: (len(o), sorted(o)))
    keep: list = []
    for opt in unique:
        if any(existing <= opt for existing in keep):
            continue
        keep.append(opt)
    return keep


class _Enumerator:
    def __init__(self, kb, budget: int):
        self.kb = kb
        self.budget = budget
        self.spent = 0
        self._memo: dict[str, list] = {}

    def tick(self, n: int = 1) -> None:
        self.spent += n
        if self.spent > self.budget:
            raise BudgetExceeded

    def options_for(self, fact_id: str, visiting=frozenset()) -> list:
        if fact_id in visiting:
            return []  # cyclic branch: no finite proof down this path
        if fact_id in self._memo:
            return self._memo[fact_id]
        fact = self.kb.facts[fact_id]
        options: list = []
        if fact.base:
            options.append(frozenset({(FACT, fact_id)}))
        visiting = visiting | {fact_id}
        for rule in self.kb.rules_concluding(fact_id):
            per_premise = []
            for premise in rule.premises:
                premise_options = self.options_for(premise, visiting)
                if not premise_options:
                    break
                per_premise.append(premise_options)
            else:
                for combo in product(*per_premise):
                    self.tick()
                    items = frozenset({(RULE, rule.id), (FACT, fact_id)}).union(*combo)
                    options.append(items)
        pruned = _prune(options)
        if self.kb.acyclic:
            # Memoisation is only sound when no branch can be cut by a cycle.
            self._memo[fact_id] = pruned
        return pruned


@dataclass(frozen=True)
class SearchResult:
    status: str  # "exact" or "undecided"
    goals: tuple
    option: frozenset | None  # best known option (witness of the upper bound)
    cost: Cost | None  # cost of `option`
    lower: Cost | None  # component-wise lower bound on the optimum
    upper: Cost | None  # component-wise upper bound on the optimum

    @property
    def exact(self) -> bool:
        return self.status == "exact"


def search(kb, goals, budget: int = DEFAULT_BUDGET) -> SearchResult:
    goals = tuple(dict.fromkeys(goals))
    if not goals:
        raise ValueError("at least one root goal is required")
    for goal in goals:
        if goal not in kb.facts:
            raise UnprovableError(goal)

    enumerator = _Enumerator(kb, budget)
    per_goal: list = []
    incomplete = False
    for goal in goals:
        try:
            options = enumerator.options_for(goal)
        except BudgetExceeded:
            options, incomplete = None, True
        per_goal.append(options)
    for goal, options in zip(goals, per_goal):
        if options is not None and not options:
            raise UnprovableError(goal)

    best = None
    if not incomplete:
        try:
            for combo in product(*per_goal):
                enumerator.tick()
                items = frozenset().union(*combo)
                if best is None or _sort_key(items, kb) < _sort_key(best, kb):
                    best = items
        except BudgetExceeded:
            incomplete = True

    if incomplete:
        return _undecided(kb, goals, per_goal)
    cost = cost_of(best, kb)
    return SearchResult("exact", goals, best, cost, cost, cost)


def _undecided(kb, goals, per_goal) -> SearchResult:
    """Bounds from per-goal optima.

    Lower bound: any package proving all goals proves each single goal, so
    the optimum is at least the per-goal minimum (component-wise max).
    Upper bound: the union of the per-goal best options is a valid complete
    proof (sharing already deduplicated by the union).
    """
    lower_public = 0
    lower_size = 0
    witness_parts = []
    witness_ok = True
    for options in per_goal:
        if not options:
            witness_ok = False
            continue
        best = min(options, key=lambda o: _sort_key(o, kb))
        cost = cost_of(best, kb)
        lower_public = max(lower_public, cost.public_facts)
        lower_size = max(lower_size, cost.size)
        witness_parts.append(best)
    lower = Cost(lower_public, lower_size)
    if witness_ok:
        union = frozenset().union(*witness_parts)
        upper = cost_of(union, kb)
        return SearchResult("undecided", goals, union, upper, lower, upper)
    return SearchResult("undecided", goals, None, None, lower, None)
