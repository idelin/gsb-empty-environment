"""Exact AND-OR search for the minimal proof subgraph.

Objective (lexicographic):

1. minimise the number of leaked *public* base facts;
2. minimise the package size, counted in items (fact nodes + rules +
   proof steps).  A plan using ``B`` base facts and ``R`` rules yields
   ``|B| + 3*|R|`` items, because every rule contributes its conclusion
   fact node and exactly one proof step.  Shared subproofs are merged by
   set union, so a shared fact is counted exactly once.

The search is a depth-first AND-OR exploration over *sets* of open goals
with memoisation.  Because every expansion step adds at least one item,
an optimal plan's exploration never revisits a goal-set state (the segment
between two visits could be removed, yielding a strictly cheaper valid
plan).  Cutting re-entrant states is therefore exact on finite graphs,
including cyclic rule graphs.

If the expansion budget is exhausted the search reports unresolved bounds:
an admissible lower bound and the cost of a greedy (valid) upper-bound
plan, so callers know the optimum lies in between.
"""

from __future__ import annotations

from dataclasses import dataclass

from .model import KnowledgeBase

DEFAULT_BUDGET = 100_000


@dataclass(frozen=True)
class Plan:
    """A proof subgraph: base evidence facts plus the rules to apply."""

    base_facts: frozenset[str]
    rules: frozenset[str]

    def cost(self, kb: KnowledgeBase) -> tuple[int, int]:
        leaks = sum(1 for f in self.base_facts if kb.facts[f].public)
        size = len(self.base_facts) + 3 * len(self.rules)
        return (leaks, size)


@dataclass(frozen=True)
class SearchResult:
    """Outcome of a search.

    ``status`` is one of ``OPTIMAL``, ``BUDGET_EXCEEDED`` or
    ``INFEASIBLE``.  ``lower``/``upper`` bound the true optimum cost;
    they coincide when the status is ``OPTIMAL``.
    """

    status: str
    plan: Plan | None
    lower: tuple[int, int] | None
    upper: tuple[int, int] | None
    expansions: int


class _BudgetExceeded(Exception):
    pass


_MISSING = object()
_EMPTY_PLAN = Plan(frozenset(), frozenset())


class _Solver:
    def __init__(self, kb: KnowledgeBase, budget: int):
        self.kb = kb
        self.budget = budget
        self.expansions = 0
        self.memo: dict[frozenset[str], Plan | None] = {}
        self.visiting: set[frozenset[str]] = set()

    def run(self, goals: frozenset[str]) -> Plan | None:
        return self._solve(goals)

    def _better(self, a: Plan | None, b: Plan | None) -> Plan | None:
        if a is None:
            return b
        if b is None:
            return a
        key_a = (a.cost(self.kb), sorted(a.base_facts), sorted(a.rules))
        key_b = (b.cost(self.kb), sorted(b.base_facts), sorted(b.rules))
        return a if key_a <= key_b else b

    def _solve(self, state: frozenset[str]) -> Plan | None:
        if not state:
            return _EMPTY_PLAN
        cached = self.memo.get(state, _MISSING)
        if cached is not _MISSING:
            return cached
        if state in self.visiting:
            return None  # cycle cut: exact for optimal plans (see docstring)
        self.expansions += 1
        if self.expansions > self.budget:
            raise _BudgetExceeded
        self.visiting.add(state)
        best: Plan | None = None
        try:
            goal = min(state)  # deterministic goal choice
            rest = state - {goal}
            fact = self.kb.facts[goal]
            if fact.evidence:
                sub = self._solve(rest)
                if sub is not None:
                    best = Plan(sub.base_facts | {goal}, sub.rules)
            for rule in self.kb.rules_concluding(goal):
                sub = self._solve(rest | set(rule.premises))
                if sub is not None:
                    cand = Plan(sub.base_facts, sub.rules | {rule.rule_id})
                    best = self._better(best, cand)
        finally:
            self.visiting.discard(state)
        self.memo[state] = best
        return best


def greedy_plan(kb: KnowledgeBase, goals) -> Plan | None:
    """A fast valid plan (evidence first, then rules in ID order).

    Used only as an upper bound when the exact search runs out of budget.
    """

    base: set[str] = set()
    rules: set[str] = set()
    done: set[str] = set()
    stack: set[str] = set()

    def rec(goal: str) -> bool:
        if goal in done:
            return True
        if goal in stack:
            return False
        fact = kb.facts.get(goal)
        stack.add(goal)
        try:
            if fact is not None and fact.evidence:
                base.add(goal)
                done.add(goal)
                return True
            for rule in kb.rules_concluding(goal):
                if all(rec(p) for p in rule.premises):
                    rules.add(rule.rule_id)
                    done.add(goal)
                    return True
            return False
        finally:
            stack.discard(goal)

    if all(rec(g) for g in sorted(goals)):
        return Plan(frozenset(base), frozenset(rules))
    return None


def _lower_bound(kb: KnowledgeBase, goals: frozenset[str]) -> tuple[int, int]:
    leaks = sum(
        1
        for g in goals
        if kb.facts[g].public and not kb.rules_concluding(g)
    )
    return (leaks, len(goals))


def solve(kb: KnowledgeBase, goals, budget: int = DEFAULT_BUDGET) -> SearchResult:
    """Find the optimal proof plan for ``goals``, or report bounds."""

    goal_set = frozenset(goals)
    unknown = sorted(g for g in goal_set if g not in kb.facts)
    if unknown:
        return SearchResult("INFEASIBLE", None, None, None, 0)
    solver = _Solver(kb, budget)
    try:
        plan = solver.run(goal_set)
    except _BudgetExceeded:
        upper_plan = greedy_plan(kb, goal_set)
        upper = upper_plan.cost(kb) if upper_plan is not None else None
        return SearchResult(
            "BUDGET_EXCEEDED", None, _lower_bound(kb, goal_set), upper,
            solver.expansions,
        )
    if plan is None:
        return SearchResult("INFEASIBLE", None, None, None, solver.expansions)
    cost = plan.cost(kb)
    return SearchResult("OPTIMAL", plan, cost, cost, solver.expansions)
