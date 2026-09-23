import unittest

from product_paths import Edge, Graph

from demo_graph import build_demo_graph


class GraphModelTest(unittest.TestCase):
    def test_isolated_vertex_is_kept(self):
        graph = build_demo_graph()
        self.assertTrue(graph.has_vertex("E"))
        self.assertEqual(graph.outgoing("E"), ())
        self.assertIn("E", graph.vertices)

    def test_parallel_edges_are_kept(self):
        graph = build_demo_graph()
        outgoing = graph.outgoing("A")
        self.assertEqual([e.edge_id for e in outgoing], ["e1", "e2", "e4"])
        self.assertEqual({e.target for e in graph.outgoing("A")}, {"B", "C"})

    def test_string_ids(self):
        graph = build_demo_graph()
        self.assertTrue(all(isinstance(v, str) for v in graph.vertices))
        self.assertTrue(all(isinstance(e.edge_id, str) for e in graph.edges))

    def test_duplicate_edge_id_rejected(self):
        graph = build_demo_graph()
        with self.assertRaises(ValueError):
            graph.add_edge(Edge("e1", "A", "D", 5.0, {"clear": "clear"}))

    def test_negative_length_rejected(self):
        with self.assertRaises(ValueError):
            Edge("bad", "A", "B", -0.5, {"clear": "clear"})

    def test_zero_length_allowed(self):
        edge = Edge("zero", "A", "B", 0.0, {"clear": "clear"})
        self.assertEqual(edge.length, 0.0)

    def test_edge_without_transition_rejected(self):
        with self.assertRaises(ValueError):
            Edge("bad", "A", "B", 1.0, {})

    def test_next_state(self):
        graph = build_demo_graph()
        e1 = graph.edge("e1")
        self.assertEqual(e1.next_state("clear"), "loaded")
        self.assertIsNone(e1.next_state("loaded"))


if __name__ == "__main__":
    unittest.main()
