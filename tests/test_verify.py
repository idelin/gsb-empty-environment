import random
import unittest

from product_paths import (
    Edge,
    ForbiddenRule,
    Graph,
    RuleSet,
    brute_force_paths,
    enumerate_paths,
    verify_enumeration,
)

from demo_graph import EXPECTED_EDGE_IDS, build_demo_graph


class BruteForceTest(unittest.TestCase):
    def setUp(self):
        self.graph = build_demo_graph()
        self.rules = RuleSet()

    def test_brute_force_matches_expected_order(self):
        paths, stats = brute_force_paths(self.graph, self.rules, "A", "D", "clear")
        self.assertEqual([p.edge_ids for p in paths], EXPECTED_EDGE_IDS)
        self.assertFalse(stats.budget_exhausted)
        self.assertGreater(stats.expansions, 0)

    def test_brute_force_budget_records_pending_states(self):
        paths, stats = brute_force_paths(
            self.graph, self.rules, "A", "D", "clear", max_expansions=1
        )
        self.assertTrue(stats.budget_exhausted)
        self.assertEqual(stats.expansions, 1)
        self.assertTrue(stats.pending_states)

    def test_verify_demo_graph(self):
        report = verify_enumeration(self.graph, self.rules, "A", "D", "clear", 10)
        self.assertTrue(report.matches)
        self.assertEqual(report.compared, 5)
        self.assertIsNone(report.mismatch_index)

    def test_verify_with_forbidden_rule(self):
        self.rules.add_rule(
            ForbiddenRule("ban", "C", "clear", valid_from=10.0, valid_to=20.0)
        )
        report = verify_enumeration(
            self.graph, self.rules, "A", "D", "clear", 10, at=15.0
        )
        self.assertTrue(report.matches)
        self.assertEqual(
            [p.edge_ids for p in report.actual], [("e1", "e3"), ("e2", "e3")]
        )


class RandomizedCompletenessTest(unittest.TestCase):
    """Cross-check best-first K-enumeration against brute force on many small
    random product graphs, including zero-weight cycles and forbidden rules."""

    def random_instance(self, rng):
        graph = Graph()
        vertices = [f"v{i}" for i in range(rng.randint(2, 5))]
        states = [f"s{i}" for i in range(rng.randint(1, 3))]
        for vertex in vertices:
            graph.add_vertex(vertex)
        for index in range(rng.randint(1, 10)):
            source = rng.choice(vertices)
            target = rng.choice(vertices)
            length = float(rng.choice([0, 0, 1, 2, 3]))
            transitions = {}
            for state in states:
                if rng.random() < 0.7:
                    transitions[state] = rng.choice(states)
            if not transitions:
                transitions[rng.choice(states)] = rng.choice(states)
            graph.add_edge(Edge(f"e{index}", source, target, length, transitions))
        rules = RuleSet()
        for index in range(rng.randint(0, 3)):
            rules.add_rule(
                ForbiddenRule(
                    f"r{index}",
                    rng.choice(vertices),
                    rng.choice(states),
                    valid_from=float(rng.choice([0, 1, 2])),
                    valid_to=rng.choice([None, 3.0, 5.0]),
                )
            )
        source = rng.choice(vertices)
        target = rng.choice(vertices)
        initial_state = rng.choice(states)
        moment = float(rng.choice([0, 1, 2, 4, 6]))
        return graph, rules, source, target, initial_state, moment

    def test_random_small_product_graphs(self):
        rng = random.Random(20260923)
        for trial in range(200):
            graph, rules, source, target, initial_state, moment = self.random_instance(
                rng
            )
            with self.subTest(trial=trial):
                report = verify_enumeration(
                    graph, rules, source, target, initial_state, 25, at=moment
                )
                self.assertTrue(
                    report.matches,
                    msg=f"mismatch at index {report.mismatch_index}",
                )
                expected, _ = brute_force_paths(
                    graph, rules, source, target, initial_state, at=moment
                )
                result = enumerate_paths(
                    graph, rules, source, target, initial_state, 25, at=moment
                )
                self.assertEqual(
                    [p.sort_key for p in result.paths],
                    [p.sort_key for p in expected[:25]],
                )
                self.assertEqual(result.complete, len(expected) <= 25)

    def test_random_pagination_matches_single_shot(self):
        rng = random.Random(7)
        for trial in range(60):
            graph, rules, source, target, initial_state, moment = self.random_instance(
                rng
            )
            with self.subTest(trial=trial):
                single = enumerate_paths(
                    graph, rules, source, target, initial_state, 9, at=moment
                )
                collected = []
                cursor = None
                for _ in range(20):
                    page = enumerate_paths(
                        graph,
                        rules,
                        source,
                        target,
                        initial_state,
                        3,
                        at=moment,
                        cursor=cursor,
                    )
                    collected.extend(page.paths)
                    if page.complete or len(collected) >= 9:
                        break
                    cursor = page.cursor
                self.assertEqual(
                    [p.sort_key for p in collected[:9]],
                    [p.sort_key for p in single.paths],
                )


if __name__ == "__main__":
    unittest.main()
