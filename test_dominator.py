import unittest

from dominator import DominatorTree, Graph


def diamond() -> tuple[Graph, DominatorTree]:
    """S -> A -> T, S -> B -> T."""
    g = Graph()
    g.add_edge("e-sa", "S", "A")
    g.add_edge("e-sb", "S", "B")
    g.add_edge("e-at", "A", "T")
    g.add_edge("e-bt", "B", "T")
    return g, DominatorTree(g, "S")


class DiamondTests(unittest.TestCase):
    def test_diamond_idom(self):
        _, dt = diamond()
        self.assertIsNone(dt.idom("S"))
        self.assertEqual(dt.idom("A"), "S")
        self.assertEqual(dt.idom("B"), "S")
        self.assertEqual(dt.idom("T"), "S")

    def test_delete_edge_updates_idom(self):
        _, dt = diamond()
        affected = dt.remove_edge("e-bt")
        self.assertEqual(dt.idom("T"), "A")
        self.assertEqual(affected, {"T"})

    def test_dominates_and_counterexample(self):
        _, dt = diamond()
        self.assertTrue(dt.dominates("S", "T"))
        self.assertTrue(dt.dominates("T", "T"))
        self.assertFalse(dt.dominates("A", "T"))
        path = dt.counterexample("A", "T")
        self.assertIsNotNone(path)
        self.assertEqual(path[0], "S")
        self.assertEqual(path[-1], "T")
        self.assertNotIn("A", path)
        self.assertIsNone(dt.counterexample("S", "T"))

    def test_counterexample_after_edge_removal(self):
        _, dt = diamond()
        dt.remove_edge("e-bt")
        self.assertTrue(dt.dominates("A", "T"))
        self.assertIsNone(dt.counterexample("A", "T"))


class DfsParentTrapTests(unittest.TestCase):
    def test_dfs_parent_is_not_idom(self):
        # DFS from S visits A, then C via A: DFS parent of C is A,
        # but C is also reachable via B, so idom(C) must be S.
        g = Graph()
        g.add_edge("e1", "S", "A")
        g.add_edge("e2", "A", "C")
        g.add_edge("e3", "S", "B")
        g.add_edge("e4", "B", "C")
        dt = DominatorTree(g, "S")
        self.assertEqual(dt.idom("C"), "S")
        self.assertEqual(dt.dominators("C"), {"S", "C"})


class UnreachableTests(unittest.TestCase):
    def test_unreachable_cycle_and_isolated_vertex(self):
        g, dt = diamond()
        g.add_edge("e-xy", "X", "Y")
        g.add_edge("e-yx", "Y", "X")
        g.add_vertex("Z")  # isolated vertex is preserved
        dt = DominatorTree(g, "S")
        self.assertEqual(dt.unreachable, frozenset({"X", "Y", "Z"}))
        self.assertIn("Z", g.vertices)
        for v in ("X", "Y", "Z"):
            self.assertFalse(dt.is_reachable(v))
            with self.assertRaises(ValueError):
                dt.idom(v)
        # dominator tree only contains reachable vertices
        self.assertEqual(set(dt._idom), {"S", "A", "B", "T"})

    def test_add_edge_makes_vertex_reachable(self):
        g, dt = diamond()
        g.add_vertex("U")
        dt = DominatorTree(g, "S")
        self.assertIn("U", dt.unreachable)
        affected = dt.add_edge("e-su", "S", "U")
        self.assertEqual(affected, {"U"})
        self.assertEqual(dt.idom("U"), "S")
        self.assertEqual(dt.unreachable, frozenset())


class ParallelEdgeTests(unittest.TestCase):
    def test_parallel_edges_preserved(self):
        g = Graph()
        g.add_edge("p1", "S", "A")
        g.add_edge("p2", "S", "A")
        self.assertEqual(set(g.edges), {"p1", "p2"})
        dt = DominatorTree(g, "S")
        self.assertEqual(dt.idom("A"), "S")
        affected = dt.remove_edge("p1")
        self.assertEqual(affected, set())
        self.assertTrue(dt.is_reachable("A"))
        self.assertEqual(dt.idom("A"), "S")


class TimeIntervalTests(unittest.TestCase):
    def build(self) -> DominatorTree:
        g = Graph()
        g.add_edge("e-sa", "S", "A")
        g.add_edge("e-sb", "S", "B")
        g.add_edge("e-at", "A", "T")
        g.add_edge("e-bt", "B", "T", start=10.0, end=20.0)  # [10, 20)
        return DominatorTree(g, "S", time=10.0)

    def test_left_closed_right_open(self):
        dt = self.build()
        # t = 10: edge active (left-closed) -> diamond, idom(T) = S
        self.assertEqual(dt.idom("T"), "S")
        # t = 20: edge inactive (right-open) -> idom(T) = A
        affected = dt.set_time(20.0)
        self.assertEqual(dt.idom("T"), "A")
        self.assertEqual(affected, {"T"})
        # t = 9.999: still inactive
        dt.set_time(9.999)
        self.assertEqual(dt.idom("T"), "A")
        # t = 19.999: active again
        dt.set_time(19.999)
        self.assertEqual(dt.idom("T"), "S")


if __name__ == "__main__":
    unittest.main()
