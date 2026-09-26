"""minwitness 的 unittest 测试。所有数据均为合成数据。"""

import io
import unittest

from minwitness import Circuit, format_witness, run_commands


def acceptance_circuit() -> Circuit:
    """root = (a OR b) AND (b OR c)，验收边界用例。"""
    c = Circuit()
    for leaf in ("a", "b", "c"):
        c.add_leaf(leaf)
    c.add_or("o1", ["a", "b"])
    c.add_or("o2", ["b", "c"])
    c.add_and("r", ["o1", "o2"])
    c.add_root("r")
    return c


class TestAcceptanceBoundary(unittest.TestCase):
    def test_min_witness_is_b_only(self):
        c = acceptance_circuit()
        self.assertTrue(c.value("r"))
        self.assertEqual(c.min_witness("r"), frozenset({"b"}))

    def test_greedy_per_subtree_is_not_optimal(self):
        # 逐子树局部最优拼接：左子树 {a}，右子树 {b}，得到 {a, b}（2 个事实）。
        c = acceptance_circuit()
        left = c.min_witness("o1")
        right = c.min_witness("o2")
        greedy = left | right
        self.assertEqual(greedy, frozenset({"a", "b"}))
        # 全局最优必须严格优于贪心拼接。
        self.assertLess(len(c.min_witness("r")), len(greedy))

    def test_revoke_b_forces_two_facts(self):
        c = acceptance_circuit()
        c.revoke("b")
        self.assertTrue(c.value("r"))
        self.assertEqual(c.min_witness("r"), frozenset({"a", "c"}))

    def test_revoke_b_and_a_makes_root_false(self):
        c = acceptance_circuit()
        c.revoke("b")
        c.revoke("a")
        self.assertFalse(c.value("r"))
        self.assertIsNone(c.min_witness("r"))

    def test_restore_b_recovers_single_fact_witness(self):
        c = acceptance_circuit()
        c.revoke("b")
        c.assert_fact("b")
        self.assertTrue(c.value("r"))
        self.assertEqual(c.min_witness("r"), frozenset({"b"}))


class TestWitnessSemantics(unittest.TestCase):
    def test_and_unions_children(self):
        c = Circuit()
        c.add_leaf("a")
        c.add_leaf("b")
        c.add_and("r", ["a", "b"])
        self.assertEqual(c.min_witness("r"), frozenset({"a", "b"}))

    def test_lexicographic_tiebreak(self):
        # 数量相同（均为 1）时按 ID 字典序取最小。
        c = Circuit()
        c.add_leaf("b")
        c.add_leaf("a")
        c.add_or("r", ["b", "a"])
        self.assertEqual(c.min_witness("r"), frozenset({"a"}))

    def test_shared_leaf_counted_once(self):
        # r = x AND (x OR y)：x 重复出现，见证只计一次。
        c = Circuit()
        c.add_leaf("x")
        c.add_leaf("y")
        c.add_or("o", ["x", "y"])
        c.add_and("r", ["x", "o"])
        self.assertEqual(c.min_witness("r"), frozenset({"x"}))

    def test_empty_witness_for_and_without_children(self):
        c = Circuit()
        c.add_and("r", [])
        self.assertTrue(c.value("r"))
        self.assertEqual(c.min_witness("r"), frozenset())

    def test_global_optimum_beats_local_stitching(self):
        # c1 = x OR (a AND b)；c2 = (x AND y) OR (a AND b)；r = c1 AND c2。
        # 逐子树贪心：c1 取 {x}，c2 取 {a,b}，拼接 {a,b,x}（3 个事实）。
        # 全局最优：{a,b}（2 个事实）。
        c = Circuit()
        for leaf in ("a", "b", "x", "y"):
            c.add_leaf(leaf)
        c.add_and("ab", ["a", "b"])
        c.add_or("c1", ["x", "ab"])
        c.add_and("xy", ["x", "y"])
        c.add_or("c2", ["xy", "ab"])
        c.add_and("r", ["c1", "c2"])
        greedy = c.min_witness("c1") | c.min_witness("c2")
        self.assertEqual(greedy, frozenset({"a", "b", "x"}))
        self.assertEqual(c.min_witness("r"), frozenset({"a", "b"}))

    def test_witness_uses_only_true_leaves(self):
        c = Circuit()
        c.add_leaf("a")
        c.add_leaf("b", asserted=False)
        c.add_or("r", ["a", "b"])
        self.assertEqual(c.min_witness("r"), frozenset({"a"}))


class TestIncrementalUpdates(unittest.TestCase):
    def test_value_propagates_through_dag(self):
        c = Circuit()
        c.add_leaf("a", asserted=False)
        c.add_leaf("b", asserted=False)
        c.add_or("o", ["a", "b"])
        c.add_and("r", ["o", "a"])
        self.assertFalse(c.value("r"))
        c.assert_fact("a")
        self.assertTrue(c.value("r"))
        self.assertEqual(c.min_witness("r"), frozenset({"a"}))
        c.revoke("a")
        self.assertFalse(c.value("r"))
        self.assertIsNone(c.min_witness("r"))

    def test_witness_updates_when_value_unchanged(self):
        # OR(a,b) 撤销 a 后值仍为真，但见证必须从 {a} 变为 {b}。
        c = Circuit()
        c.add_leaf("a")
        c.add_leaf("b")
        c.add_or("r", ["a", "b"])
        self.assertEqual(c.min_witness("r"), frozenset({"a"}))
        c.revoke("a")
        self.assertTrue(c.value("r"))
        self.assertEqual(c.min_witness("r"), frozenset({"b"}))

    def test_multiple_roots(self):
        c = Circuit()
        c.add_leaf("a")
        c.add_leaf("b")
        c.add_or("o", ["a", "b"])
        c.add_and("r1", ["a", "b"])
        c.add_root("o")
        c.add_root("r1")
        self.assertEqual(c.min_witness("o"), frozenset({"a"}))
        self.assertEqual(c.min_witness("r1"), frozenset({"a", "b"}))
        c.revoke("a")
        self.assertEqual(c.min_witness("o"), frozenset({"b"}))
        self.assertIsNone(c.min_witness("r1"))

    def test_revoke_non_leaf_rejected(self):
        c = Circuit()
        c.add_leaf("a")
        c.add_or("o", ["a"])
        with self.assertRaises(ValueError):
            c.revoke("o")


class TestCli(unittest.TestCase):
    def test_acceptance_script(self):
        script = "\n".join([
            "# 合成示例：root = (a OR b) AND (b OR c)",
            "leaf a", "leaf b", "leaf c",
            "or o1 a b", "or o2 b c", "and r o1 o2",
            "root r",
            "witness r",
            "revoke b",
            "witness r",
            "value r",
            "assert b",
            "witness r",
        ])
        out = io.StringIO()
        run_commands(script.splitlines(), out)
        self.assertEqual(out.getvalue().splitlines(), [
            "r = {b}",
            "r = {a, c}",
            "r = true",
            "r = {b}",
        ])

    def test_format_witness(self):
        self.assertEqual(format_witness(None), "none")
        self.assertEqual(format_witness(frozenset({"b", "a"})), "{a, b}")
        self.assertEqual(format_witness(frozenset()), "{}")


if __name__ == "__main__":
    unittest.main()
