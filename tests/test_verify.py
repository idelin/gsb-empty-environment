import unittest

from graphcolor import Graph, Problem, verify


def problem():
    g = Graph()
    g.add_edge("e1", "a", "b")
    g.add_edge("e2", "b", "c")
    return Problem(g, ("r", "g"), capacities={"r": 2}, precoloring={"a": "r"})


class TestVerifier(unittest.TestCase):
    def test_valid_coloring(self):
        self.assertEqual(verify(problem(), {"a": "r", "b": "g", "c": "r"}), [])

    def test_edge_violation_reports_edge_id(self):
        v = verify(problem(), {"a": "r", "b": "r", "c": "g"})
        self.assertTrue(any("e1" in msg for msg in v))

    def test_capacity_violation(self):
        v = verify(problem(), {"a": "r", "b": "g", "c": "r"})  # r twice: ok
        self.assertEqual(v, [])
        p = problem()
        p.capacities["r"] = 1
        v = verify(p, {"a": "r", "b": "g", "c": "r"})
        self.assertTrue(any("capacity" in msg for msg in v))

    def test_precoloring_violation(self):
        v = verify(problem(), {"a": "g", "b": "r", "c": "g"})
        self.assertTrue(any("precoloring" in msg for msg in v))

    def test_missing_and_unknown(self):
        v = verify(problem(), {"a": "r", "b": "g", "ghost": "r"})
        self.assertTrue(any("missing" in msg for msg in v))
        self.assertTrue(any("unknown vertex" in msg for msg in v))

    def test_unknown_color(self):
        v = verify(problem(), {"a": "r", "b": "blue", "c": "g"})
        self.assertTrue(any("unknown color" in msg for msg in v))


if __name__ == "__main__":
    unittest.main()
