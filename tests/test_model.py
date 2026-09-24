import unittest

from graphcolor import Graph, Interval, Problem, color_symmetry_classes


class TestInterval(unittest.TestCase):
    def test_left_closed_right_open(self):
        iv = Interval(2, 5)
        self.assertIn(2, iv)
        self.assertIn(4.999, iv)
        self.assertNotIn(5, iv)
        self.assertNotIn(1.999, iv)

    def test_empty_rejected(self):
        with self.assertRaises(ValueError):
            Interval(3, 3)
        with self.assertRaises(ValueError):
            Interval(4, 2)

    def test_overlaps(self):
        self.assertTrue(Interval(0, 2).overlaps(Interval(1, 3)))
        self.assertFalse(Interval(0, 1).overlaps(Interval(1, 2)))  # touching != overlap


class TestGraph(unittest.TestCase):
    def test_isolated_vertices_preserved(self):
        g = Graph()
        g.add_vertex("alone")
        g.add_edge("e1", "a", "b")
        self.assertEqual(set(g.vertices), {"alone", "a", "b"})

    def test_multi_edges_preserved(self):
        g = Graph()
        g.add_edge("m1", "a", "b")
        g.add_edge("m2", "a", "b")
        self.assertEqual([e.id for e in g.edges], ["m1", "m2"])
        adj = g.adjacency()
        self.assertEqual(sorted(eid for eid, _ in adj["a"]), ["m1", "m2"])

    def test_duplicate_edge_id_rejected(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        with self.assertRaises(ValueError):
            g.add_edge("e1", "a", "c")

    def test_string_ids_enforced(self):
        g = Graph()
        with self.assertRaises(ValueError):
            g.add_vertex("")
        with self.assertRaises(ValueError):
            g.add_edge("", "a", "b")


class TestProblem(unittest.TestCase):
    def test_validation(self):
        g = Graph()
        g.add_vertex("v")
        with self.assertRaises(ValueError):
            Problem(g, ("r",), precoloring={"v": "blue"}).validate()
        with self.assertRaises(ValueError):
            Problem(g, ("r",), precoloring={"ghost": "r"}).validate()
        with self.assertRaises(ValueError):
            Problem(g, ("r",), capacities={"r": -1}).validate()
        with self.assertRaises(ValueError):
            Problem(g, ("r",), capacities={"blue": 1}).validate()

    def test_symmetry_classes(self):
        g = Graph()
        g.add_vertex("v")
        # equal capacities -> interchangeable
        p = Problem(g, ("a", "b", "c"), capacities={"a": 1, "b": 1, "c": 2})
        self.assertEqual(color_symmetry_classes(p), [("a", "b")])
        # precolored color pinned out of its class
        p = Problem(g, ("a", "b", "c"), precoloring={"v": "a"})
        self.assertEqual(color_symmetry_classes(p), [("b", "c")])


if __name__ == "__main__":
    unittest.main()
