import math
import unittest

from pareto_path import Edge, Graph, pareto_frontier


def build_demo_graph() -> Graph:
    g = Graph()
    g.add_vertex("iso")
    g.add_edge(Edge("e1", "s", "a", cost=1, delay=4, start=0, end=10))
    g.add_edge(Edge("e2", "a", "t", cost=0, delay=5))
    g.add_edge(Edge("e3", "s", "b", cost=3, delay=1))
    g.add_edge(Edge("e4", "b", "t", cost=2, delay=1))
    g.add_edge(Edge("e5", "s", "t", cost=3, delay=5))
    g.add_edge(Edge("e6", "s", "t", cost=4, delay=6))
    g.add_edge(Edge("e7", "s", "a", cost=1, delay=6))
    return g


class FrontierTests(unittest.TestCase):
    def test_frontier_contains_both_tradeoff_extremes(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=5)
        pairs = [(e.cost, e.delay) for e in result.entries]
        self.assertEqual(pairs, [(1, 9), (3, 5), (5, 2)])
        self.assertIn((1, 9), pairs)  # 低成本高时延
        self.assertIn((5, 2), pairs)  # 高成本低时延

    def test_dominated_path_excluded(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=5)
        pairs = [(e.cost, e.delay) for e in result.entries]
        self.assertNotIn((4, 6), pairs)  # e6 被 e5 的 (3, 5) 两维同时支配
        self.assertNotIn((1, 11), pairs)  # 平行边 e7 路径被 e1 路径支配

    def test_frontier_sorted_and_strictly_tradeoff(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=5)
        costs = [e.cost for e in result.entries]
        delays = [e.delay for e in result.entries]
        self.assertEqual(costs, sorted(costs))
        self.assertTrue(all(d1 > d2 for d1, d2 in zip(delays, delays[1:])))

    def test_budget_boundary_inclusive(self):
        at_budget = pareto_frontier(build_demo_graph(), "s", "t", budget=5)
        self.assertIn((5, 2), [(e.cost, e.delay) for e in at_budget.entries])
        below = pareto_frontier(build_demo_graph(), "s", "t", budget=4)
        self.assertEqual([(e.cost, e.delay) for e in below.entries], [(1, 9), (3, 5)])

    def test_no_feasible_path_returns_empty_set(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=0)
        self.assertEqual(result.entries, ())
        self.assertFalse(result.truncated)
        self.assertTrue(result.complete)

    def test_unreachable_target_returns_empty_set(self):
        g = Graph()
        g.add_edge(Edge("e1", "s", "a", cost=1, delay=1))
        g.add_vertex("t")
        result = pareto_frontier(g, "s", "t", budget=10)
        self.assertEqual(result.entries, ())
        self.assertTrue(result.complete)

    def test_negative_budget_returns_empty_set(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=-1)
        self.assertEqual(result.entries, ())
        self.assertTrue(result.complete)

    def test_isolated_vertex_preserved(self):
        g = build_demo_graph()
        self.assertIn("iso", g.vertices)
        self.assertEqual(g.outgoing("iso"), ())

    def test_parallel_edges_preserved(self):
        g = build_demo_graph()
        parallel = [e.id for e in g.edges if e.source == "s" and e.target == "a"]
        self.assertEqual(parallel, ["e1", "e7"])

    def test_window_left_closed(self):
        g = Graph()
        g.add_edge(Edge("e1", "s", "a", cost=1, delay=1))
        g.add_edge(Edge("e2", "a", "t", cost=1, delay=1, start=1, end=5))
        result = pareto_frontier(g, "s", "t", budget=10)
        self.assertEqual([(e.cost, e.delay) for e in result.entries], [(2, 2)])

    def test_window_right_open(self):
        g = Graph()
        g.add_edge(Edge("e1", "s", "a", cost=1, delay=5))
        g.add_edge(Edge("e2", "a", "t", cost=1, delay=1, start=0, end=5))
        result = pareto_frontier(g, "s", "t", budget=10)
        self.assertEqual(result.entries, ())  # 到达时刻 5 不在 [0, 5) 内

    def test_waiting_until_window_opens(self):
        g = Graph()
        g.add_edge(Edge("e1", "s", "a", cost=1, delay=1))
        g.add_edge(Edge("e2", "a", "t", cost=1, delay=1, start=3, end=9))
        result = pareto_frontier(g, "s", "t", budget=10)
        self.assertEqual([(e.cost, e.delay) for e in result.entries], [(2, 4)])

    def test_max_results_truncation_marks_incomplete(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=5, max_results=2)
        self.assertEqual(len(result.entries), 2)
        self.assertEqual([(e.cost, e.delay) for e in result.entries], [(1, 9), (3, 5)])
        self.assertTrue(result.truncated)
        self.assertFalse(result.complete)  # 截断不得称为完整

    def test_max_labels_truncation_marks_incomplete(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=5, max_labels=1)
        self.assertTrue(result.truncated)
        self.assertFalse(result.complete)

    def test_no_truncation_means_complete(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=5, max_results=3)
        self.assertFalse(result.truncated)
        self.assertTrue(result.complete)

    def test_zero_cycle_terminates_with_simple_path(self):
        g = Graph()
        g.add_edge(Edge("e1", "s", "u", cost=0, delay=0))
        g.add_edge(Edge("e2", "u", "s", cost=0, delay=0))
        g.add_edge(Edge("e3", "s", "t", cost=1, delay=1))
        result = pareto_frontier(g, "s", "t", budget=5)
        self.assertEqual([(e.cost, e.delay) for e in result.entries], [(1, 1)])
        for entry in result.entries:
            self.assertEqual(len(entry.vertices), len(set(entry.vertices)))

    def test_all_output_paths_are_simple(self):
        result = pareto_frontier(build_demo_graph(), "s", "t", budget=5)
        for entry in result.entries:
            self.assertEqual(len(entry.vertices), len(set(entry.vertices)))
            self.assertEqual(len(entry.edges) + 1, len(entry.vertices))

    def test_source_equals_target(self):
        result = pareto_frontier(build_demo_graph(), "s", "s", budget=0)
        self.assertEqual([(e.cost, e.delay) for e in result.entries], [(0, 0)])

    def test_negative_weight_rejected(self):
        with self.assertRaises(ValueError):
            Edge("bad", "s", "t", cost=-1, delay=0)
        with self.assertRaises(ValueError):
            Edge("bad", "s", "t", cost=0, delay=-0.5)

    def test_invalid_window_rejected(self):
        with self.assertRaises(ValueError):
            Edge("bad", "s", "t", cost=0, delay=0, start=5, end=2)

    def test_unknown_endpoint_raises(self):
        g = build_demo_graph()
        with self.assertRaises(KeyError):
            pareto_frontier(g, "ghost", "t", budget=5)
        with self.assertRaises(KeyError):
            pareto_frontier(g, "s", "ghost", budget=5)

    def test_invalid_limits_rejected(self):
        g = build_demo_graph()
        with self.assertRaises(ValueError):
            pareto_frontier(g, "s", "t", budget=5, max_results=0)
        with self.assertRaises(ValueError):
            pareto_frontier(g, "s", "t", budget=5, max_labels=0)

    def test_string_ids_required(self):
        with self.assertRaises(ValueError):
            Edge("", "s", "t", cost=0, delay=0)
        g = Graph()
        with self.assertRaises(ValueError):
            g.add_vertex("")

    def test_start_time_offsets_delay(self):
        g = Graph()
        g.add_edge(Edge("e1", "s", "t", cost=1, delay=2, start=10, end=20))
        result = pareto_frontier(g, "s", "t", budget=5, start_time=8)
        self.assertEqual([(e.cost, e.delay) for e in result.entries], [(1, 4)])


if __name__ == "__main__":
    unittest.main()
