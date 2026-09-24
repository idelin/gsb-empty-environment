"""Model-level tests: intervals, multigraph invariants, canonical form."""
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from graphpatch import Edge, Graph, GraphError, Interval, Vertex
from helpers import random_graph


class TestInterval(unittest.TestCase):
    def test_left_closed_right_open(self):
        iv = Interval(1.0, 3.0)
        self.assertTrue(iv.contains(1.0))    # left boundary included
        self.assertTrue(iv.contains(2.5))
        self.assertFalse(iv.contains(3.0))   # right boundary excluded
        self.assertFalse(iv.contains(0.9))

    def test_adjacent_intervals_do_not_overlap(self):
        a = Interval(0.0, 1.0)
        b = Interval(1.0, 2.0)
        self.assertFalse(a.overlaps(b))
        self.assertFalse(b.overlaps(a))
        self.assertTrue(a.overlaps(Interval(0.5, 1.5)))

    def test_invalid_interval_rejected(self):
        with self.assertRaises(ValueError):
            Interval(2.0, 2.0)
        with self.assertRaises(ValueError):
            Interval(3.0, 1.0)

    def test_unbounded_interval(self):
        iv = Interval()
        self.assertTrue(iv.contains(-1e18))
        self.assertTrue(iv.contains(1e18))


class TestGraph(unittest.TestCase):
    def test_multiedges_and_isolated_vertices(self):
        g = Graph()
        g.add_vertex(Vertex("a"))
        g.add_vertex(Vertex("b"))
        g.add_vertex(Vertex("lonely"))  # isolated vertex
        g.add_edge(Edge("e1", "a", "b"))
        g.add_edge(Edge("e2", "a", "b"))  # parallel edge: multiplicity 2
        g.add_edge(Edge("e3", "a", "a"))  # self-loop
        self.assertEqual(g.edge_multiplicity("a", "b"), 2)
        self.assertIn("lonely", g.vertices)
        self.assertEqual(len(g.edges), 3)

    def test_edge_requires_existing_endpoints(self):
        g = Graph()
        g.add_vertex(Vertex("a"))
        with self.assertRaises(GraphError):
            g.add_edge(Edge("e1", "a", "ghost"))

    def test_vertex_removal_requires_detached(self):
        g = Graph()
        g.add_vertex(Vertex("a"))
        g.add_vertex(Vertex("b"))
        g.add_edge(Edge("e1", "a", "b"))
        with self.assertRaises(GraphError):
            g.remove_vertex("a")
        g.remove_edge("e1")
        g.remove_vertex("a")
        self.assertNotIn("a", g.vertices)

    def test_active_at_respects_half_open_intervals(self):
        g = Graph()
        g.add_vertex(Vertex("a", {}, Interval(0.0, 2.0)))
        g.add_vertex(Vertex("b", {}, Interval(2.0, 4.0)))
        g.add_vertex(Vertex("c"))  # always active
        g.add_edge(Edge("e1", "a", "c", {}, Interval(1.0, 3.0)))
        at0 = g.active_at(0.0)
        self.assertEqual(set(at0.vertices), {"a", "c"})
        at2 = g.active_at(2.0)  # a expired (right-open), b just started
        self.assertEqual(set(at2.vertices), {"b", "c"})
        at15 = g.active_at(1.5)
        self.assertEqual(set(at15.edges), {"e1"})
        at35 = g.active_at(3.5)  # e1 expired
        self.assertEqual(set(at35.edges), set())

    def test_equality_and_canonical_stability(self):
        rng = random.Random(7)
        g1 = random_graph(rng, 6, 9)
        g2 = g1.copy()
        self.assertEqual(g1, g2)
        self.assertEqual(g1.canonical(), g2.canonical())
        self.assertEqual(g1.content_hash(), g2.content_hash())
        # copy is deep: mutating the copy must not touch the original
        v = next(iter(g2.vertices))
        g2.vertices[v].attrs["tampered"] = True
        self.assertNotEqual(g1, g2)


if __name__ == "__main__":
    unittest.main()
