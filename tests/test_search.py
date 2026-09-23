import unittest

from product_paths import (
    CursorVersionError,
    Edge,
    ForbiddenRule,
    Graph,
    RuleSet,
    enumerate_paths,
)

from demo_graph import EXPECTED_EDGE_IDS, build_demo_graph


def edge_ids(result):
    return [p.edge_ids for p in result.paths]


class OrderingTest(unittest.TestCase):
    def setUp(self):
        self.graph = build_demo_graph()
        self.rules = RuleSet()

    def test_three_level_ordering(self):
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 5)
        self.assertEqual(edge_ids(result), EXPECTED_EDGE_IDS)
        self.assertTrue(result.complete)
        self.assertIsNone(result.cursor)

    def test_cost_beats_edge_count(self):
        # The cost-1 path uses 3 edges and still ranks above cost-2, 2-edge paths.
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 1)
        self.assertEqual(result.paths[0].edge_ids, ("e2", "e7", "e5"))
        self.assertEqual(result.paths[0].cost, 1.0)
        self.assertEqual(result.paths[0].edge_count, 3)

    def test_edge_id_sequence_breaks_ties(self):
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 4)
        keys = [p.sort_key for p in result.paths]
        self.assertEqual(keys[1], (2.0, 2, ("e1", "e3")))
        self.assertEqual(keys[2], (2.0, 2, ("e2", "e3")))
        self.assertEqual(keys[3], (2.0, 2, ("e4", "e5")))

    def test_state_trajectory_output(self):
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 1)
        path = result.paths[0]
        self.assertEqual(
            path.state_trajectory(),
            (("A", "clear"), ("B", "clear"), ("C", "clear"), ("D", "clear")),
        )
        loaded = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 2).paths[1]
        self.assertEqual(
            loaded.state_trajectory(),
            (("A", "clear"), ("B", "loaded"), ("D", "clear")),
        )

    def test_zero_weight_cycle_terminates(self):
        # B <-> C is a zero-weight cycle; enumeration must still terminate and
        # every returned path must be product-state simple.
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 50)
        self.assertTrue(result.complete)
        self.assertEqual(len(result.paths), 5)
        for path in result.paths:
            product_states = list(path.state_trajectory())
            self.assertEqual(len(product_states), len(set(product_states)))

    def test_same_vertex_different_states_not_pruned(self):
        # (B, loaded) via e1 and (B, clear) via e2 coexist; both lead to D.
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 5)
        self.assertIn(("e1", "e3"), edge_ids(result))
        self.assertIn(("e2", "e3"), edge_ids(result))

    def test_state_blocking_edge_forces_detour(self):
        # Cheap edge into B only works in state "loaded"; arriving "clear"
        # must not prune the more expensive "loaded" route.
        graph = Graph()
        graph.add_edge(Edge("fast", "S", "B", 1.0, {"clear": "clear"}))
        graph.add_edge(Edge("slow", "S", "B", 5.0, {"clear": "loaded"}))
        graph.add_edge(Edge("exit", "B", "T", 1.0, {"loaded": "loaded"}))
        result = enumerate_paths(graph, RuleSet(), "S", "T", "clear", 3)
        self.assertEqual(edge_ids(result), [("slow", "exit")])
        self.assertTrue(result.complete)

    def test_source_equals_target(self):
        result = enumerate_paths(self.graph, self.rules, "A", "A", "clear", 1)
        self.assertEqual(result.paths[0].edge_ids, ())
        self.assertEqual(result.paths[0].cost, 0.0)


class ForbiddenRuleTest(unittest.TestCase):
    def setUp(self):
        self.graph = build_demo_graph()
        self.rules = RuleSet()
        self.rules.add_rule(
            ForbiddenRule("ban-C-clear", "C", "clear", valid_from=10.0, valid_to=20.0)
        )

    def test_shorter_path_yields_to_longer_legal_path(self):
        # At t=15 the cost-1 path through (C, clear) is illegal; the best
        # legal path is the cost-2 (e1, e3) path.
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 1, at=15.0)
        self.assertEqual(result.paths[0].edge_ids, ("e1", "e3"))
        self.assertEqual(result.paths[0].cost, 2.0)

    def test_interval_edges(self):
        at_open = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 1, at=10.0)
        at_closed = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 1, at=20.0)
        self.assertEqual(at_open.paths[0].edge_ids, ("e1", "e3"))
        self.assertEqual(at_closed.paths[0].edge_ids, ("e2", "e7", "e5"))

    def test_top_k_integrates_forbidden_rules(self):
        # Post-filtering the unconstrained top-2 would leave a single path;
        # the integrated search must return both legal paths.
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 2, at=15.0)
        self.assertEqual(edge_ids(result), [("e1", "e3"), ("e2", "e3")])
        self.assertTrue(result.complete)
        self.assertGreater(result.stats.pruned_forbidden, 0)

    def test_forbidden_start_state(self):
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("ban-start", "A", "clear"))
        result = enumerate_paths(self.graph, rules, "A", "D", "clear", 3)
        self.assertEqual(result.paths, [])
        self.assertTrue(result.complete)

    def test_version_update_changes_results(self):
        v1 = self.rules.version
        at_v1 = enumerate_paths(
            self.graph, self.rules, "A", "D", "clear", 1, at=15.0, version=v1
        )
        v2 = self.rules.remove_rule("ban-C-clear")
        at_v2 = enumerate_paths(
            self.graph, self.rules, "A", "D", "clear", 1, at=15.0, version=v2
        )
        self.assertEqual(at_v1.paths[0].edge_ids, ("e1", "e3"))
        self.assertEqual(at_v2.paths[0].edge_ids, ("e2", "e7", "e5"))


class CursorTest(unittest.TestCase):
    def setUp(self):
        self.graph = build_demo_graph()
        self.rules = RuleSet()

    def test_pagination_covers_full_enumeration(self):
        collected = []
        cursor = None
        pages = 0
        while True:
            result = enumerate_paths(
                self.graph, self.rules, "A", "D", "clear", 2, cursor=cursor
            )
            collected.extend(result.paths)
            pages += 1
            if result.complete:
                break
            cursor = result.cursor
            self.assertIsNotNone(cursor)
        self.assertEqual([p.edge_ids for p in collected], EXPECTED_EDGE_IDS)
        self.assertEqual(pages, 3)

    def test_cursor_bound_to_version(self):
        first = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 2)
        cursor = first.cursor
        self.rules.add_rule(ForbiddenRule("r", "C", "clear"))
        with self.assertRaises(CursorVersionError):
            enumerate_paths(self.graph, self.rules, "A", "D", "clear", 2, cursor=cursor)
        # Re-querying the pinned old version with the cursor still works.
        resumed = enumerate_paths(
            self.graph, self.rules, "A", "D", "clear", 2,
            version=cursor.version, cursor=cursor,
        )
        self.assertEqual(edge_ids(resumed), [("e2", "e3"), ("e4", "e5")])


class BudgetTest(unittest.TestCase):
    def setUp(self):
        self.graph = build_demo_graph()
        self.rules = RuleSet()

    def test_budget_records_expansions_and_pending_states(self):
        result = enumerate_paths(
            self.graph, self.rules, "A", "D", "clear", 5, max_expansions=1
        )
        self.assertTrue(result.stats.budget_exhausted)
        self.assertEqual(result.stats.expansions, 1)
        self.assertEqual(result.paths, [])
        self.assertEqual(
            result.stats.pending_states,
            (("B", "clear"), ("B", "loaded"), ("C", "clear")),
        )

    def test_expansion_count_recorded_on_full_run(self):
        result = enumerate_paths(self.graph, self.rules, "A", "D", "clear", 5)
        self.assertFalse(result.stats.budget_exhausted)
        self.assertGreater(result.stats.expansions, 0)
        self.assertGreaterEqual(result.stats.generated, result.stats.expansions)


if __name__ == "__main__":
    unittest.main()
