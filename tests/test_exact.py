import random
import unittest

from proofsched import (
    Fact,
    Goal,
    GoalStatus,
    GreedyScheduler,
    ProofEngine,
    Rule,
    exact_max_weight,
)
from proofsched.sample import build_sample


def random_world(rng, n_facts=3, n_rules=6, n_goals=3, max_budget=5):
    """Random acyclic synthetic world: rule premises only reference earlier
    nodes, so the derivation graph is finite and cycle-free."""
    facts = {f"f{i}": Fact(f"f{i}") for i in range(n_facts)}
    nodes = list(facts)
    rules = {}
    for i in range(n_rules):
        n_premises = rng.randint(1, min(2, len(nodes)))
        premises = tuple(rng.sample(nodes, n_premises))
        conclusion = f"n{i}"
        rules[f"r{i}"] = Rule(f"r{i}", premises, conclusion)
        nodes.append(conclusion)
    goals = {}
    for i in range(n_goals):
        n_targets = rng.randint(1, 2)
        targets = tuple(rng.sample(nodes, n_targets))
        goals[f"g{i}"] = Goal(f"g{i}", targets, float(rng.randint(1, 5)))
    return facts, rules, goals, max_budget


class ExactReferenceTests(unittest.TestCase):
    def test_exact_on_known_instance(self):
        # r1+r2 prove g1 (weight 5); r3 alone proves g2 (weight 4).
        facts = {"a": Fact("a")}
        rules = {
            "r1": Rule("r1", ("a",), "b"),
            "r2": Rule("r2", ("b",), "c"),
            "r3": Rule("r3", ("a",), "d"),
        }
        goals = {
            "g1": Goal("g1", ("c",), 5.0),
            "g2": Goal("g2", ("d",), 4.0),
        }
        self.assertEqual(exact_max_weight(facts, rules, goals, 0).weight, 0.0)
        self.assertEqual(exact_max_weight(facts, rules, goals, 1).weight, 4.0)
        result = exact_max_weight(facts, rules, goals, 2)
        self.assertEqual(result.weight, 5.0)
        self.assertEqual(exact_max_weight(facts, rules, goals, 3).weight, 9.0)

    def test_exact_plan_replays_on_engine(self):
        facts, rules, goals = build_sample()
        for budget in range(0, 8):
            result = exact_max_weight(facts, rules, goals, budget)
            engine = ProofEngine(facts, rules, goals, budget=budget)
            for rule_id in result.plan:
                engine.apply(rule_id)
            self.assertEqual(engine.proved_weight(), result.weight, budget)

    def test_heuristic_never_beats_exact_on_random_small_worlds(self):
        for seed in range(40):
            rng = random.Random(seed)
            facts, rules, goals, max_budget = random_world(rng)
            for budget in range(0, max_budget + 1):
                exact = exact_max_weight(facts, rules, goals, budget)
                engine = ProofEngine(facts, rules, goals, budget=budget)
                GreedyScheduler(engine).run(exhaust=True)
                got = engine.proved_weight()
                self.assertLessEqual(got, exact.weight, (seed, budget))
                # Engine steps never exceed the budget.
                self.assertLessEqual(engine.stats.steps_computed, budget)

    def test_random_plan_matches_engine_closure(self):
        # Applying an exact plan to the engine proves exactly the goals the
        # exact model predicts, confirming both see the same finite graph.
        for seed in range(20):
            rng = random.Random(1000 + seed)
            facts, rules, goals, max_budget = random_world(rng)
            result = exact_max_weight(facts, rules, goals, max_budget)
            engine = ProofEngine(facts, rules, goals, budget=max_budget)
            for rule_id in result.plan:
                engine.apply(rule_id)
            self.assertEqual(engine.proved_weight(), result.weight, seed)


class SampleWorldExactTests(unittest.TestCase):
    def test_sample_world_exact_curve(self):
        facts, rules, goals = build_sample()
        expected = {0: 0.0, 1: 0.0, 2: 5.0, 3: 7.0, 4: 9.0, 5: 12.0, 6: 12.0}
        for budget, weight in expected.items():
            self.assertEqual(
                exact_max_weight(facts, rules, goals, budget).weight,
                weight,
                budget,
            )


if __name__ == "__main__":
    unittest.main()
