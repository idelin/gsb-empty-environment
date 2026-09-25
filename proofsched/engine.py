"""Proof engine: shared frontier, budgeted derivation, retraction, statistics.

Design invariants
-----------------
* One global derivation state and one reusable frontier, shared by all goals;
  nothing is recomputed per goal.
* A derivation step (rule application) is computed at most once while valid;
  reusing a valid subproof costs zero budget ("shared subproof consumes
  effective computation once").
* A goal is DISPROVED only when the frontier is exhausted (fixpoint reached);
  "not yet searched" is never treated as disproof.  With budget left but a
  non-empty frontier, unproved goals stay UNDECIDED.
* Retracting a fact or bumping a fact/rule version invalidates dependent
  derivations, recovers the budget they consumed, and forces re-verification
  of stale frontier entries that referenced the invalidated evidence.
* Adding budget never revokes a verified conclusion.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .model import Certificate, Fact, Goal, GoalStatus, ProofNode, Rule


class BudgetExhausted(RuntimeError):
    """Raised when a derivation step is requested with zero budget."""


@dataclass
class SearchStats:
    steps_computed: int = 0          # rule applications actually computed
    duplicate_steps_avoided: int = 0  # apply() calls hitting a valid cached step
    budget_recovered: int = 0         # steps refunded by invalidation
    frontier_revalidations: int = 0   # stale frontier entries re-verified
    alternative_proofs_reused: int = 0  # conclusions re-attached via other rules
    retractions: int = 0              # fact retractions / version bumps handled


class ProofEngine:
    def __init__(
        self,
        facts: dict[str, Fact],
        rules: dict[str, Rule],
        goals: dict[str, Goal],
        budget: int,
    ) -> None:
        if budget < 0:
            raise ValueError("budget must be >= 0")
        self.facts: dict[str, Fact] = dict(facts)
        self.rules: dict[str, Rule] = dict(rules)
        self.goals: dict[str, Goal] = dict(goals)
        self.budget = budget
        self.stats = SearchStats()
        self._derived: dict[str, str] = {}  # conclusion node -> rule_id used
        self._applied: dict[str, str] = {}  # rule_id -> conclusion node
        self._frontier: list[str] = []
        self._rebuild_frontier()

    # ------------------------------------------------------------------ views

    @property
    def frontier(self) -> tuple[str, ...]:
        """Rule IDs whose premises are all proved but not yet applied."""
        return tuple(self._frontier)

    def is_proved(self, node: str) -> bool:
        return node in self.facts or node in self._derived

    def goal_status(self, goal_id: str) -> GoalStatus:
        goal = self.goals[goal_id]
        if all(self.is_proved(t) for t in goal.targets):
            return GoalStatus.PROVED
        if not self._frontier:
            # Fixpoint reached: nothing left to derive, so unproved goals can
            # never be proved from the current facts/rules.  Only now may we
            # call them disproved.
            return GoalStatus.DISPROVED
        return GoalStatus.UNDECIDED

    def status_report(self) -> dict[str, GoalStatus]:
        return {gid: self.goal_status(gid) for gid in sorted(self.goals)}

    def proved_weight(self) -> float:
        return sum(
            g.weight
            for g in self.goals.values()
            if self.goal_status(g.goal_id) is GoalStatus.PROVED
        )

    # --------------------------------------------------------------- stepping

    def apply(self, rule_id: str) -> bool:
        """Apply one derivation step.  Returns True if a new step was computed.

        Applying an already-valid rule is a cache hit: it costs no budget and
        is counted as duplicate work avoided.
        """
        rule = self.rules[rule_id]
        if rule_id in self._applied:
            self.stats.duplicate_steps_avoided += 1
            return False
        if not self._applicable(rule):
            raise ValueError(f"rule {rule_id!r} is not applicable")
        if self.budget <= 0:
            raise BudgetExhausted(f"no budget left to apply {rule_id!r}")
        self.budget -= 1
        self.stats.steps_computed += 1
        self._applied[rule_id] = rule.conclusion
        # Keep the first proof of a conclusion; alternatives stay cached in
        # _applied and are re-attached for free if the first proof breaks.
        self._derived.setdefault(rule.conclusion, rule_id)
        self._rebuild_frontier()
        return True

    def add_budget(self, amount: int) -> None:
        """Increase the step budget.  Never touches existing conclusions."""
        if amount < 0:
            raise ValueError("amount must be >= 0")
        self.budget += amount

    # ------------------------------------------------- retraction / versions

    def retract_fact(self, fact_id: str) -> None:
        """Withdraw a base fact; invalidate dependents and recover budget."""
        if fact_id not in self.facts:
            raise KeyError(fact_id)
        del self.facts[fact_id]
        self.stats.retractions += 1
        self._invalidate(changed={fact_id})

    def update_fact(self, fact: Fact) -> None:
        """Assert or re-version a fact.  A version bump invalidates evidence
        built on the old version; dependents must be re-derived."""
        old = self.facts.get(fact.fact_id)
        self.facts[fact.fact_id] = fact
        if old is not None and old != fact:
            self.stats.retractions += 1
            self._invalidate(changed={fact.fact_id})
        else:
            self._rebuild_frontier()

    def update_rule(self, rule: Rule) -> None:
        """Replace or re-version a rule.  Steps computed with the old version
        are refunded and must be re-computed with the new version."""
        old = self.rules.get(rule.rule_id)
        if old == rule:
            return
        changed: set[str] = set()
        if rule.rule_id in self._applied:
            conclusion = self._applied.pop(rule.rule_id)
            if self._derived.get(conclusion) == rule.rule_id:
                del self._derived[conclusion]
            self.budget += 1
            self.stats.budget_recovered += 1
            changed.add(conclusion)
        self.rules[rule.rule_id] = rule
        self._invalidate(changed=changed)

    # ---------------------------------------------------------------- proofs

    def certificate(self, goal_id: str) -> Certificate:
        """Build a replayable certificate for a proved goal."""
        if self.goal_status(goal_id) is not GoalStatus.PROVED:
            raise ValueError(f"goal {goal_id!r} is not proved")
        goal = self.goals[goal_id]
        return Certificate(
            goal_id=goal_id,
            proofs=tuple(self._proof_node(t) for t in goal.targets),
        )

    def shared_work_avoided(self) -> int:
        """Duplicate derivation work avoided by sharing, measured over proved
        goals: sum of per-goal proof-tree rule counts minus their union."""
        total = 0
        union: set[str] = set()
        for goal in self.goals.values():
            if self.goal_status(goal.goal_id) is not GoalStatus.PROVED:
                continue
            tree_rules: set[str] = set()
            for proof in self.certificate(goal.goal_id).proofs:
                self._collect_rules(proof, tree_rules)
            total += len(tree_rules)
            union |= tree_rules
        return total - len(union)

    # -------------------------------------------------------------- internal

    @staticmethod
    def _collect_rules(node: ProofNode, out: set[str]) -> None:
        if node.rule_id is not None:
            out.add(node.rule_id)
        for child in node.children:
            ProofEngine._collect_rules(child, out)

    def _proof_node(self, node: str) -> ProofNode:
        fact = self.facts.get(node)
        if fact is not None:
            return ProofNode(node_id=node, fact_version=fact.version)
        rule_id = self._derived[node]
        rule = self.rules[rule_id]
        return ProofNode(
            node_id=node,
            rule_id=rule.rule_id,
            rule_version=rule.version,
            children=tuple(self._proof_node(p) for p in rule.premises),
        )

    def _applicable(self, rule: Rule) -> bool:
        return all(self.is_proved(p) for p in rule.premises)

    def _rebuild_frontier(self) -> None:
        self._frontier = sorted(
            rid
            for rid, rule in self.rules.items()
            if rid not in self._applied and self._applicable(rule)
        )

    def _dependents(self, seeds: set[str]) -> set[str]:
        """Derived nodes transitively depending on any seed node."""
        invalid: set[str] = set()
        growing = True
        while growing:
            growing = False
            for node, rule_id in self._derived.items():
                if node in invalid:
                    continue
                premises = self.rules[rule_id].premises
                if any(p in seeds or p in invalid for p in premises):
                    invalid.add(node)
                    growing = True
        return invalid

    def _invalidate(self, changed: set[str]) -> None:
        invalid = self._dependents(changed) if changed else set()
        old_frontier = list(self._frontier)
        for node in sorted(invalid):
            rule_id = self._derived.pop(node, None)
            if rule_id is None:
                continue
            self._applied.pop(rule_id, None)
            self.budget += 1
            self.stats.budget_recovered += 1
        # Re-attach conclusions that still have a valid, already-computed
        # alternative proof (costs nothing: the step was computed once).
        reattached = True
        while reattached:
            reattached = False
            for rule_id, conclusion in sorted(self._applied.items()):
                if conclusion in self._derived:
                    continue
                if all(self.is_proved(p) for p in self.rules[rule_id].premises):
                    self._derived[conclusion] = rule_id
                    self.stats.alternative_proofs_reused += 1
                    reattached = True
        self._rebuild_frontier()
        # Stale frontier entries that referenced invalidated evidence must be
        # re-verified; the rebuild above is that re-verification, counted here.
        stale = [
            rid
            for rid in old_frontier
            if any(p in invalid or p in changed for p in self.rules[rid].premises)
        ]
        self.stats.frontier_revalidations += len(stale)
