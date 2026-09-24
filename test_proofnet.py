import unittest

from proofnet import Interval, ProofNetwork


class TestAndSemantics(unittest.TestCase):
    """超边为 AND 语义：所有前置成立才产生结论。"""

    def test_single_premise_not_enough(self):
        net = ProofNetwork()
        net.add_edge("e1", ("A", "B"), "C")
        net.add_seed("sA", "A")
        self.assertFalse(net.holds("C"))  # 只有 A 不能推 C
        net.add_seed("sB", "B")
        self.assertTrue(net.holds("C"))  # A、B 齐备才成立

    def test_retract_one_premise_breaks_edge(self):
        net = ProofNetwork()
        net.add_edge("e1", ("A", "B"), "C")
        net.add_seed("sA", "A")
        net.add_seed("sB", "B")
        self.assertTrue(net.holds("C"))
        net.retract_seed("sB")
        self.assertFalse(net.holds("C"))


class TestAlternativeProofsAndRetraction(unittest.TestCase):
    """替代证明：撤回一条路径后，结论可由另一条路径维持。"""

    def test_alternative_proof_survives_retraction(self):
        net = ProofNetwork()
        net.add_edge("e1", ("A", "B"), "C")
        net.add_edge("e2", ("D",), "C")
        net.add_seed("sA", "A")
        net.add_seed("sB", "B")
        net.add_seed("sD", "D")
        self.assertTrue(net.holds("C"))
        net.retract_seed("sB")  # 撤回 B，e1 失效
        self.assertTrue(net.holds("C"))  # D->C 仍维持 C
        proof = net.proof("C")
        self.assertEqual(proof.source_id, "e2")
        net.retract_seed("sD")
        self.assertFalse(net.holds("C"))  # 两条路径都断了才不成立

    def test_retract_edge(self):
        net = ProofNetwork()
        net.add_edge("e1", ("A",), "B")
        net.add_seed("sA", "A")
        self.assertTrue(net.holds("B"))
        net.retract_edge("e1")
        self.assertFalse(net.holds("B"))

    def test_retract_unknown_id_raises(self):
        net = ProofNetwork()
        with self.assertRaises(KeyError):
            net.retract_edge("nope")
        with self.assertRaises(KeyError):
            net.retract_seed("nope")


class TestCyclesNeedSeed(unittest.TestCase):
    """纯循环 C->E->C 无外部种子时均不成立。"""

    def test_cycle_without_seed(self):
        net = ProofNetwork()
        net.add_edge("e1", ("C",), "E")
        net.add_edge("e2", ("E",), "C")
        self.assertFalse(net.holds("C"))
        self.assertFalse(net.holds("E"))
        self.assertEqual(net.derived(), frozenset())

    def test_cycle_with_seed_then_retracted(self):
        net = ProofNetwork()
        net.add_edge("e1", ("C",), "E")
        net.add_edge("e2", ("E",), "C")
        net.add_seed("sC", "C")
        self.assertTrue(net.holds("C"))
        self.assertTrue(net.holds("E"))
        net.retract_seed("sC")
        self.assertFalse(net.holds("C"))
        self.assertFalse(net.holds("E"))


class TestIntervals(unittest.TestCase):
    """时间区间左闭右开 [start, end)。"""

    def test_seed_interval_boundaries(self):
        net = ProofNetwork()
        net.add_seed("s", "A", Interval(2, 5))
        self.assertFalse(net.holds("A", t=1))
        self.assertTrue(net.holds("A", t=2))   # 左闭
        self.assertTrue(net.holds("A", t=4.999))
        self.assertFalse(net.holds("A", t=5))  # 右开

    def test_edge_interval_boundaries(self):
        net = ProofNetwork()
        net.add_seed("s", "A")  # A 始终成立
        net.add_edge("e", ("A",), "B", interval=Interval(10, 20))
        self.assertFalse(net.holds("B", t=9))
        self.assertTrue(net.holds("B", t=10))
        self.assertTrue(net.holds("B", t=19))
        self.assertFalse(net.holds("B", t=20))

    def test_invalid_interval_raises(self):
        with self.assertRaises(ValueError):
            Interval(5, 5)
        with self.assertRaises(ValueError):
            Interval(6, 5)


class TestProofCost(unittest.TestCase):
    """证明成本：结论成本 = 边成本 + 前置最小成本之和，取最小。"""

    def test_cost_sums_premises_and_edge(self):
        net = ProofNetwork()
        net.add_seed("sA", "A", cost=1.0)
        net.add_seed("sB", "B", cost=2.0)
        net.add_edge("e1", ("A", "B"), "C", cost=10.0)
        self.assertEqual(net.proof_cost("C"), 13.0)

    def test_min_cost_over_alternatives(self):
        net = ProofNetwork()
        net.add_seed("sA", "A")
        net.add_seed("sD", "D")
        net.add_edge("cheap", ("A",), "C", cost=1.0)
        net.add_edge("pricey", ("D",), "C", cost=5.0)
        self.assertEqual(net.proof_cost("C"), 1.0)
        net.retract_edge("cheap")  # 撤回便宜路径后退化到贵路径
        self.assertEqual(net.proof_cost("C"), 5.0)

    def test_unprovable_cost_is_inf(self):
        net = ProofNetwork()
        net.add_edge("e1", ("C",), "E")
        net.add_edge("e2", ("E",), "C")
        self.assertEqual(net.proof_cost("C"), float("inf"))

    def test_negative_cost_rejected(self):
        net = ProofNetwork()
        with self.assertRaises(ValueError):
            net.add_edge("e", ("A",), "B", cost=-1)
        with self.assertRaises(ValueError):
            net.add_seed("s", "A", cost=-1)


class TestGraphStructure(unittest.TestCase):
    """字符串 ID、孤立顶点、多重边。"""

    def test_isolated_vertex_preserved(self):
        net = ProofNetwork()
        net.add_vertex("lonely")
        net.add_edge("e1", ("A",), "B")
        net.add_seed("sA", "A")
        self.assertIn("lonely", net.vertices)
        self.assertFalse(net.holds("lonely"))  # 存在但不成立
        self.assertEqual(net.derived(), frozenset({"A", "B"}))

    def test_parallel_multi_edges(self):
        net = ProofNetwork()
        net.add_edge("e1", ("A",), "B", cost=3.0)
        net.add_edge("e2", ("A",), "B", cost=7.0)  # 平行边，同前置同结论
        net.add_seed("sA", "A")
        self.assertEqual(len(net.edges), 2)
        self.assertEqual(net.proof_cost("B"), 3.0)
        net.retract_edge("e1")
        self.assertTrue(net.holds("B"))  # 平行边 e2 仍维持
        self.assertEqual(net.proof_cost("B"), 7.0)

    def test_duplicate_ids_rejected(self):
        net = ProofNetwork()
        net.add_edge("e1", ("A",), "B")
        net.add_seed("s1", "A")
        with self.assertRaises(ValueError):
            net.add_edge("e1", ("A",), "C")
        with self.assertRaises(ValueError):
            net.add_seed("s1", "B")

    def test_empty_premises_edge_rejected(self):
        net = ProofNetwork()
        with self.assertRaises(ValueError):
            net.add_edge("e", (), "A")


class TestProofTree(unittest.TestCase):
    def test_proof_tree_structure(self):
        net = ProofNetwork()
        net.add_seed("sA", "A")
        net.add_seed("sB", "B")
        net.add_edge("e1", ("A", "B"), "C", cost=2.0)
        proof = net.proof("C")
        self.assertIsNotNone(proof)
        self.assertEqual(proof.kind, "edge")
        self.assertEqual(proof.source_id, "e1")
        self.assertEqual(proof.cost, 2.0)
        self.assertEqual({s.vertex for s in proof.subproofs}, {"A", "B"})
        self.assertTrue(all(s.kind == "seed" for s in proof.subproofs))

    def test_proof_none_when_unprovable(self):
        net = ProofNetwork()
        net.add_edge("e1", ("A", "B"), "C")
        self.assertIsNone(net.proof("C"))

    def test_zero_cost_cycle_proof_terminates(self):
        # 零成本环 + 种子：证明树应回溯到种子而非在环上死循环
        net = ProofNetwork()
        net.add_seed("sC", "C")
        net.add_edge("e1", ("C",), "E", cost=0.0)
        net.add_edge("e2", ("E",), "C", cost=0.0)
        proof = net.proof("C")
        self.assertIsNotNone(proof)
        self.assertEqual(proof.kind, "seed")


if __name__ == "__main__":
    unittest.main()
