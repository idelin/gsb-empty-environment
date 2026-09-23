"""dominator 模块的单元测试（Python 标准库 unittest）。"""

import unittest

from dominator import (
    DOMINATES,
    NOT_DOMINATES,
    TARGET_UNREACHABLE,
    DominatorEngine,
    Edge,
    Graph,
)


def diamond_graph():
    """S -> A/B -> T 菱形，外加孤立点 ISO 与不可达环 X<->Y。"""
    g = Graph()
    g.add_edge(Edge("e_sa", "S", "A"))
    g.add_edge(Edge("e_sb", "S", "B"))
    g.add_edge(Edge("e_at", "A", "T"))
    g.add_edge(Edge("e_bt", "B", "T"))
    g.add_vertex("ISO")                      # 孤立点
    g.add_edge(Edge("e_xy", "X", "Y"))       # 不可达环
    g.add_edge(Edge("e_yx", "Y", "X"))
    return g


class DiamondTest(unittest.TestCase):
    def setUp(self):
        self.eng = DominatorEngine(diamond_graph(), "S")

    def test_diamond_idom(self):
        snap = self.eng.snapshot(0)
        self.assertIsNone(snap.idom["S"])
        self.assertEqual(snap.idom["A"], "S")
        self.assertEqual(snap.idom["B"], "S")
        # 菱形汇合点 T 的立即支配者是 S，不是 DFS 父节点
        self.assertEqual(snap.idom["T"], "S")

    def test_unreachable_kept_separate(self):
        snap = self.eng.snapshot(0)
        self.assertEqual(snap.unreachable, frozenset({"ISO", "X", "Y"}))
        # 不可达环不进入支配树
        for v in ("ISO", "X", "Y"):
            self.assertNotIn(v, snap.idom)
            self.assertNotIn(v, snap.children)

    def test_remove_edge_recompute_affected(self):
        report = self.eng.remove_edge("e_bt", at=0)
        # 删 B->T 后 T 的 idom 由 S 变为 A，且只有 T 受影响
        self.assertEqual(self.eng.snapshot(0).idom["T"], "A")
        self.assertEqual(report.affected, frozenset({"T"}))
        self.assertEqual(report.idom_changed, {"T": ("S", "A")})
        self.assertEqual(report.became_unreachable, frozenset())
        self.assertEqual(report.became_reachable, frozenset())

    def test_dominance_queries(self):
        self.assertTrue(self.eng.dominates("S", "T", 0))
        self.assertTrue(self.eng.dominates("S", "S", 0))   # 自反
        self.assertFalse(self.eng.dominates("A", "T", 0))
        self.assertFalse(self.eng.dominates("T", "S", 0))
        self.assertFalse(self.eng.dominates("X", "Y", 0))  # 不可达点不支配

    def test_counterexample_path(self):
        res = self.eng.explain("A", "T", 0)
        self.assertEqual(res.status, NOT_DOMINATES)
        # 反例：S -> B -> T 绕开了 A
        self.assertEqual(res.path.vertices, ["S", "B", "T"])
        self.assertEqual(res.path.edges, ["e_sb", "e_bt"])
        self.assertNotIn("A", res.path.vertices)
        ok = self.eng.explain("S", "T", 0)
        self.assertEqual(ok.status, DOMINATES)
        self.assertIsNone(ok.path)
        un = self.eng.explain("S", "X", 0)
        self.assertEqual(un.status, TARGET_UNREACHABLE)


class DfsParentTrapTest(unittest.TestCase):
    """DFS 父节点不是支配者的经典陷阱：S->A, S->B, A->C, B->C。"""

    def test_idom_not_dfs_parent(self):
        g = Graph()
        g.add_edge(Edge("e1", "S", "A"))
        g.add_edge(Edge("e2", "S", "B"))
        g.add_edge(Edge("e3", "A", "C"))  # DFS 会先把 C 挂到 A 下
        g.add_edge(Edge("e4", "B", "C"))
        eng = DominatorEngine(g, "S")
        self.assertEqual(eng.snapshot(0).idom["C"], "S")


class GraphShapeTest(unittest.TestCase):
    def test_multi_edges_preserved(self):
        g = Graph()
        g.add_edge(Edge("m1", "S", "A"))
        g.add_edge(Edge("m2", "S", "A"))  # 同一对顶点的多重边
        self.assertEqual(len(g.edges), 2)
        eng = DominatorEngine(g, "S")
        self.assertEqual(eng.snapshot(0).idom["A"], "S")
        with self.assertRaises(ValueError):
            g.add_edge(Edge("m1", "S", "A"))  # 边 ID 必须唯一

    def test_isolated_vertex_preserved(self):
        g = Graph()
        g.add_vertex("LONELY")
        g.add_edge(Edge("e", "S", "A"))
        eng = DominatorEngine(g, "S")
        snap = eng.snapshot(0)
        self.assertIn("LONELY", g.vertices)
        self.assertIn("LONELY", snap.unreachable)


class TimeIntervalTest(unittest.TestCase):
    def test_half_open_interval(self):
        g = Graph()
        g.add_edge(Edge("e_sa", "S", "A"))
        g.add_edge(Edge("e_ab", "A", "B", start=5, end=10))  # [5, 10)
        eng = DominatorEngine(g, "S")
        self.assertIn("B", eng.snapshot(4).unreachable)   # t=4 未生效
        self.assertIn("B", eng.snapshot(5).reachable)     # 左闭：t=5 生效
        self.assertIn("B", eng.snapshot(9).reachable)
        self.assertIn("B", eng.snapshot(10).unreachable)  # 右开：t=10 失效

    def test_interval_change_report(self):
        g = Graph()
        g.add_edge(Edge("e_sa", "S", "A"))
        g.add_edge(Edge("e_ab", "A", "B"))
        eng = DominatorEngine(g, "S")
        # 让 e_ab 在 t=10 失效（右开端）
        report = eng.set_edge_interval("e_ab", float("-inf"), 10, at=10)
        self.assertEqual(report.became_unreachable, frozenset({"B"}))
        self.assertIn("B", report.affected)
        # t=9 仍然可达（左闭右开）
        self.assertIn("B", eng.snapshot(9).reachable)


class IncrementalTest(unittest.TestCase):
    def test_add_edge_makes_cycle_reachable(self):
        g = diamond_graph()
        eng = DominatorEngine(g, "S")
        report = eng.add_edge(Edge("e_sx", "S", "X"), at=0)
        self.assertEqual(report.became_reachable, frozenset({"X", "Y"}))
        snap = eng.snapshot(0)
        self.assertEqual(snap.idom["X"], "S")
        self.assertEqual(snap.idom["Y"], "X")
        self.assertEqual(snap.unreachable, frozenset({"ISO"}))

    def test_remove_edge_makes_unreachable(self):
        g = Graph()
        g.add_edge(Edge("e1", "S", "A"))
        g.add_edge(Edge("e2", "A", "B"))
        eng = DominatorEngine(g, "S")
        report = eng.remove_edge("e2", at=0)
        self.assertEqual(report.became_unreachable, frozenset({"B"}))
        self.assertEqual(report.affected, frozenset({"B"}))

    def test_noop_change_reports_empty(self):
        eng = DominatorEngine(diamond_graph(), "S")
        eng.snapshot(0)
        # 加一条不改变支配关系的平行边（多重边）
        report = eng.add_edge(Edge("e_at2", "A", "T"), at=0)
        self.assertEqual(report.affected, frozenset())


if __name__ == "__main__":
    unittest.main()
