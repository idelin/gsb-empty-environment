"""hyperproof 的 unittest 测试套件。"""

import unittest

from hyperproof import HyperProofGraph, Interval


class TestAndSemantics(unittest.TestCase):
    """超边是 AND 语义: 所有前置成立才产生结论。"""

    def test_partial_premises_do_not_derive(self):
        # {A,B}->C 只有 A 不能推 C
        g = HyperProofGraph()
        g.add_edge("e1", ("A", "B"), "C")
        g.assert_seed("A")
        self.assertTrue(g.holds("A"))
        self.assertFalse(g.holds("C"))

    def test_all_premises_derive(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A", "B"), "C")
        g.assert_seed("A")
        g.assert_seed("B")
        self.assertTrue(g.holds("C"))

    def test_single_premise_edge(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A",), "B")
        g.assert_seed("A")
        self.assertTrue(g.holds("B"))


class TestAlternativeProofsAndRetraction(unittest.TestCase):
    """替代证明与撤回。"""

    def test_retraction_with_alternative_proof(self):
        # {A,B}->C 且 D->C: 撤回 B 后 C 仍由 D 维持
        g = HyperProofGraph()
        g.add_edge("e1", ("A", "B"), "C")
        g.add_edge("e2", ("D",), "C")
        g.assert_seed("A")
        g.assert_seed("B")
        g.assert_seed("D")
        self.assertTrue(g.holds("C"))
        g.retract_seed("B")
        self.assertTrue(g.holds("C"))  # D->C 维持
        g.retract_seed("D")
        self.assertFalse(g.holds("C"))  # 两条证明都没了

    def test_retraction_cascades(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A",), "B")
        g.add_edge("e2", ("B",), "C")
        g.assert_seed("A")
        self.assertTrue(g.holds("C"))
        g.retract_seed("A")
        self.assertFalse(g.holds("B"))
        self.assertFalse(g.holds("C"))

    def test_retract_specific_interval(self):
        g = HyperProofGraph()
        g.assert_seed("A", 0.0, 5.0)
        g.assert_seed("A", 10.0, 20.0)
        g.retract_seed("A", 0.0, 5.0)
        self.assertFalse(g.holds("A", 2.0))
        self.assertTrue(g.holds("A", 15.0))


class TestCyclesNeedSeed(unittest.TestCase):
    """循环证明必须有外部种子。"""

    def test_pure_cycle_without_seed(self):
        # 纯 C->E->C 无种子时均不成立
        g = HyperProofGraph()
        g.add_edge("e1", ("C",), "E")
        g.add_edge("e2", ("E",), "C")
        self.assertFalse(g.holds("C"))
        self.assertFalse(g.holds("E"))
        self.assertEqual(g.derived(), frozenset())

    def test_cycle_with_seed(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("C",), "E")
        g.add_edge("e2", ("E",), "C")
        g.assert_seed("C")
        self.assertTrue(g.holds("C"))
        self.assertTrue(g.holds("E"))
        g.retract_seed("C")
        self.assertFalse(g.holds("C"))
        self.assertFalse(g.holds("E"))

    def test_self_loop_without_seed(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("X",), "X")
        self.assertFalse(g.holds("X"))


class TestGraphStructure(unittest.TestCase):
    """字符串 ID、孤立点、多重边。"""

    def test_isolated_vertices_preserved(self):
        g = HyperProofGraph()
        g.add_vertex("lonely")
        g.add_edge("e1", ("A",), "B")
        self.assertIn("lonely", g.vertices)
        self.assertFalse(g.holds("lonely"))

    def test_parallel_edges_kept(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A",), "B", cost=1.0)
        g.add_edge("e2", ("A",), "B", cost=2.0)  # 同样的前置与结论, 不同 ID
        self.assertEqual(len(g.edges), 2)
        self.assertEqual({e.id for e in g.edges}, {"e1", "e2"})

    def test_duplicate_edge_id_rejected(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A",), "B")
        with self.assertRaises(ValueError):
            g.add_edge("e1", ("A",), "C")

    def test_non_string_ids_rejected(self):
        g = HyperProofGraph()
        with self.assertRaises(TypeError):
            g.add_vertex(1)
        with self.assertRaises(TypeError):
            g.add_edge("e1", ("A",), 2)
        with self.assertRaises(TypeError):
            g.add_edge(3, ("A",), "B")


class TestIntervals(unittest.TestCase):
    """时间区间左闭右开 [start, end)。"""

    def test_left_closed_right_open(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A",), "B")
        g.assert_seed("A", 2.0, 5.0)
        self.assertFalse(g.holds("B", 1.999))
        self.assertTrue(g.holds("B", 2.0))   # 左闭
        self.assertTrue(g.holds("B", 4.999))
        self.assertFalse(g.holds("B", 5.0))  # 右开

    def test_invalid_interval_rejected(self):
        with self.assertRaises(ValueError):
            Interval(5.0, 5.0)
        with self.assertRaises(ValueError):
            Interval(6.0, 5.0)


class TestProofCost(unittest.TestCase):
    """证明成本: 超边成本 + 前置最小成本之和, 取最小。"""

    def test_cost_sums_premises_and_edge(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A", "B"), "C", cost=2.0)
        g.assert_seed("A")
        g.assert_seed("B")
        self.assertEqual(g.proof_cost("C"), 2.0)  # 种子成本为 0

    def test_cost_chains(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A",), "B", cost=1.5)
        g.add_edge("e2", ("B",), "C", cost=2.5)
        g.assert_seed("A")
        self.assertEqual(g.proof_cost("C"), 4.0)

    def test_min_cost_over_alternatives(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A",), "C", cost=10.0)
        g.add_edge("e2", ("B",), "C", cost=3.0)
        g.assert_seed("A")
        g.assert_seed("B")
        self.assertEqual(g.proof_cost("C"), 3.0)
        self.assertEqual(g.proof("C").via_edge, "e2")

    def test_underivable_cost_is_none(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("C",), "E")
        g.add_edge("e2", ("E",), "C")
        self.assertIsNone(g.proof_cost("C"))
        self.assertIsNone(g.proof("C"))

    def test_negative_cost_rejected(self):
        g = HyperProofGraph()
        with self.assertRaises(ValueError):
            g.add_edge("e1", ("A",), "B", cost=-1.0)

    def test_proof_tree_shape(self):
        g = HyperProofGraph()
        g.add_edge("e1", ("A", "B"), "C", cost=1.0)
        g.assert_seed("A")
        g.assert_seed("B")
        proof = g.proof("C")
        self.assertEqual(proof.via_edge, "e1")
        self.assertEqual({p.vertex for p in proof.premises}, {"A", "B"})
        self.assertTrue(all(p.via_edge is None for p in proof.premises))


if __name__ == "__main__":
    unittest.main(verbosity=2)
