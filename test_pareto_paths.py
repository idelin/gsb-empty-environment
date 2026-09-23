"""Unit tests for pareto_paths (Python 3.11, stdlib unittest only)."""

import math
import unittest

from pareto_paths import Graph, pareto_paths


def tradeoffs(result):
    """Sorted (cost, delay) pairs of a SearchResult."""
    return sorted((p.cost, p.delay) for p in result.paths)


class FrontierSemanticsTest(unittest.TestCase):
    def build(self):
        graph = Graph()
        # Three parallel routes S -> T via multi-edges and relays.
        graph.add_edge("e-fast-expensive", "S", "T", cost=5, delay=2)
        graph.add_edge("e-slow-cheap", "S", "T", cost=1, delay=9)
        graph.add_edge("e-mid", "S", "T", cost=3, delay=5)
        graph.add_edge("e-dominated", "S", "T", cost=4, delay=6)
        return graph

    def test_both_extremes_on_frontier(self):
        # cost=1/delay=9 and cost=5/delay=2 must both survive.
        result = pareto_paths(self.build(), "S", "T", budget=5)
        points = tradeoffs(result)
        self.assertIn((1.0, 9.0), points)
        self.assertIn((5.0, 2.0), points)

    def test_dominated_path_excluded(self):
        # (4, 6) is dominated by (3, 5) in both dimensions.
        result = pareto_paths(self.build(), "S", "T", budget=5)
        self.assertEqual(tradeoffs(result), [(1.0, 9.0), (3.0, 5.0), (5.0, 2.0)])

    def test_frontier_sorted_by_cost(self):
        result = pareto_paths(self.build(), "S", "T", budget=5)
        costs = [p.cost for p in result.paths]
        self.assertEqual(costs, sorted(costs))

    def test_budget_boundary_inclusive(self):
        # cost exactly equal to the budget is allowed.
        result = pareto_paths(self.build(), "S", "T", budget=5)
        self.assertIn((5.0, 2.0), tradeoffs(result))
        # budget=4.999 drops the cost-5 path but keeps cheaper ones.
        result = pareto_paths(self.build(), "S", "T", budget=4.999)
        self.assertEqual(tradeoffs(result), [(1.0, 9.0), (3.0, 5.0)])

    def test_no_feasible_path_returns_empty_set(self):
        result = pareto_paths(self.build(), "S", "T", budget=0)
        self.assertEqual(result.paths, ())
        self.assertFalse(result.truncated)
        self.assertTrue(result.is_complete)

    def test_source_equals_target(self):
        result = pareto_paths(self.build(), "S", "S", budget=0)
        self.assertEqual(tradeoffs(result), [(0.0, 0.0)])
        self.assertEqual(result.paths[0].vertices, ("S",))
        self.assertEqual(result.paths[0].edges, ())


class GraphStructureTest(unittest.TestCase):
    def test_string_ids_and_multiedges_preserved(self):
        graph = Graph()
        graph.add_edge("edge/甲", "节点-S", "节点-T", cost=1, delay=9)
        graph.add_edge("edge/乙", "节点-S", "节点-T", cost=5, delay=2)
        self.assertEqual(len(graph.edges), 2)  # multi-edges both kept
        result = pareto_paths(graph, "节点-S", "节点-T", budget=5)
        self.assertEqual(tradeoffs(result), [(1.0, 9.0), (5.0, 2.0)])
        used_edge_ids = {p.edges[0] for p in result.paths}
        self.assertEqual(used_edge_ids, {"edge/甲", "edge/乙"})

    def test_isolated_vertices_kept(self):
        graph = Graph()
        graph.add_vertex("lonely")
        graph.add_edge("e1", "S", "T", cost=1, delay=1)
        self.assertIn("lonely", graph.vertices)
        self.assertEqual(graph.out_edges("lonely"), ())

    def test_no_repeated_vertices(self):
        graph = Graph()
        graph.add_edge("S->A", "S", "A", cost=0, delay=0)
        graph.add_edge("A->S", "A", "S", cost=0, delay=0)  # cycle bait
        graph.add_edge("A->B", "A", "B", cost=1, delay=1)
        graph.add_edge("B->A", "B", "A", cost=0, delay=0)  # cycle bait
        graph.add_edge("B->T", "B", "T", cost=1, delay=1)
        graph.add_edge("S->T", "S", "T", cost=5, delay=1)
        result = pareto_paths(graph, "S", "T", budget=10)
        for path in result.paths:
            self.assertEqual(
                len(path.vertices), len(set(path.vertices)),
                f"repeated vertex in {path.vertices}",
            )
        self.assertEqual(tradeoffs(result), [(2.0, 2.0), (5.0, 1.0)])


class TimeWindowTest(unittest.TestCase):
    def build(self):
        graph = Graph()
        graph.add_edge("S->A", "S", "A", cost=1, delay=5)
        graph.add_edge("S->B", "S", "B", cost=1, delay=10)
        # Both relay edges share window [5, 10).
        graph.add_edge("A->T", "A", "T", cost=1, delay=1, window=(5, 10))
        graph.add_edge("B->T", "B", "T", cost=1, delay=1, window=(5, 10))
        return graph

    def test_window_left_closed(self):
        # Arrival delay exactly == window start (5) is allowed.
        result = pareto_paths(self.build(), "S", "T", budget=10)
        self.assertIn((2.0, 6.0), tradeoffs(result))

    def test_window_right_open(self):
        # Arrival delay exactly == window end (10) is rejected, so the
        # route via B (arrival 10) must not appear.
        result = pareto_paths(self.build(), "S", "T", budget=10)
        self.assertEqual(tradeoffs(result), [(2.0, 6.0)])

    def test_default_window_always_open(self):
        graph = Graph()
        graph.add_edge("e1", "S", "T", cost=1, delay=100)
        result = pareto_paths(graph, "S", "T", budget=1)
        self.assertEqual(tradeoffs(result), [(1.0, 100.0)])


class TruncationTest(unittest.TestCase):
    def build(self):
        graph = Graph()
        graph.add_edge("e1", "S", "T", cost=1, delay=9)
        graph.add_edge("e2", "S", "T", cost=3, delay=5)
        graph.add_edge("e3", "S", "T", cost=5, delay=2)
        return graph

    def test_max_results_caps_output_and_marks_truncated(self):
        result = pareto_paths(self.build(), "S", "T", budget=5, max_results=2)
        self.assertEqual(len(result.paths), 2)
        self.assertTrue(result.truncated)
        self.assertFalse(result.is_complete)  # must not claim complete

    def test_max_results_large_enough_is_complete(self):
        result = pareto_paths(self.build(), "S", "T", budget=5, max_results=3)
        self.assertEqual(len(result.paths), 3)
        self.assertFalse(result.truncated)
        self.assertTrue(result.is_complete)

    def test_max_labels_caps_search_and_marks_truncated(self):
        result = pareto_paths(self.build(), "S", "T", budget=5, max_labels=0)
        self.assertTrue(result.truncated)
        self.assertFalse(result.is_complete)

    def test_untruncated_result_is_complete(self):
        result = pareto_paths(self.build(), "S", "T", budget=5)
        self.assertFalse(result.truncated)
        self.assertTrue(result.is_complete)


class ValidationTest(unittest.TestCase):
    def test_negative_cost_rejected(self):
        graph = Graph()
        with self.assertRaises(ValueError):
            graph.add_edge("e1", "S", "T", cost=-1, delay=0)

    def test_negative_delay_rejected(self):
        graph = Graph()
        with self.assertRaises(ValueError):
            graph.add_edge("e1", "S", "T", cost=0, delay=-0.5)

    def test_inverted_window_rejected(self):
        graph = Graph()
        with self.assertRaises(ValueError):
            graph.add_edge("e1", "S", "T", cost=0, delay=0, window=(10, 5))

    def test_duplicate_edge_id_rejected(self):
        graph = Graph()
        graph.add_edge("e1", "S", "T", cost=1, delay=1)
        with self.assertRaises(ValueError):
            graph.add_edge("e1", "S", "T", cost=2, delay=2)

    def test_non_string_ids_rejected(self):
        graph = Graph()
        with self.assertRaises(TypeError):
            graph.add_vertex(7)
        with self.assertRaises(TypeError):
            graph.add_edge(3, "S", "T", cost=1, delay=1)

    def test_unknown_endpoints_rejected(self):
        graph = Graph()
        graph.add_edge("e1", "S", "T", cost=1, delay=1)
        with self.assertRaises(ValueError):
            pareto_paths(graph, "S", "nowhere", budget=1)
        with self.assertRaises(ValueError):
            pareto_paths(graph, "nowhere", "T", budget=1)

    def test_negative_budget_rejected(self):
        graph = Graph()
        graph.add_edge("e1", "S", "T", cost=1, delay=1)
        with self.assertRaises(ValueError):
            pareto_paths(graph, "S", "T", budget=-1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
