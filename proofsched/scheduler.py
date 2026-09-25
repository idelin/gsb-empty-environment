"""Heuristic scheduler: greedy goal-weight prioritisation over the shared
frontier.  This is a heuristic for larger instances; it does NOT claim
optimality.  See exact.py for the exact small-scale reference strategy.
"""
from __future__ import annotations

from .engine import ProofEngine
from .model import GoalStatus


class GreedyScheduler:
    """Picks frontier steps that promise the most unproved goal weight.

    Priority of a frontier rule = sum of weights of unproved goals whose
    missing targets are backward-reachable through that rule.  Ties break on
    rule ID, so the schedule is deterministic.  The frontier itself is owned
    by the engine and shared across goals; the scheduler only ranks it.
    """

    def __init__(self, engine: ProofEngine) -> None:
        self.engine = engine

    def _rule_goal_weights(self) -> dict[str, float]:
        weights = {rid: 0.0 for rid in self.engine.rules}
        for goal in self.engine.goals.values():
            if self.engine.goal_status(goal.goal_id) is GoalStatus.PROVED:
                continue
            missing = [t for t in goal.targets if not self.engine.is_proved(t)]
            if not missing:
                continue
            contributing: set[str] = set()
            stack = list(missing)
            seen: set[str] = set()
            while stack:
                node = stack.pop()
                if node in seen:
                    continue
                seen.add(node)
                for rule in self.engine.rules.values():
                    if rule.conclusion == node and rule.rule_id not in contributing:
                        contributing.add(rule.rule_id)
                        stack.extend(rule.premises)
            for rule_id in contributing:
                weights[rule_id] += goal.weight
        return weights

    def run(self, exhaust: bool = False) -> dict[str, GoalStatus]:
        """Apply steps until budget or frontier runs out.

        With exhaust=False (default) the scheduler stops once no frontier step
        can still contribute to an unproved goal.  With exhaust=True it keeps
        draining the frontier (deterministic order) so that unreachable goals
        can legitimately be classified DISPROVED via fixpoint exhaustion.
        """
        engine = self.engine
        while engine.budget > 0 and engine.frontier:
            weights = self._rule_goal_weights()
            best_rule: str | None = None
            best_key: tuple[float, str] | None = None
            for rule_id in engine.frontier:
                weight = weights.get(rule_id, 0.0)
                if not exhaust and weight <= 0.0:
                    continue
                key = (-weight, rule_id)
                if best_key is None or key < best_key:
                    best_key = key
                    best_rule = rule_id
            if best_rule is None:
                break
            engine.apply(best_rule)
        return engine.status_report()
