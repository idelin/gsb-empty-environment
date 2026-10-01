import unittest

from table_constraints import (
    Engine, Infeasible, NegativeTable, PositiveTable, SharedPrefixTable,
    TableConstraint, check_solution, solve,
)


class TestSharedPrefixTable(unittest.TestCase):
    def test_duplicate_tuples_stored_once(self):
        t = SharedPrefixTable(2)
        t.add((1, 2)); t.add((1, 2)); t.add((1, 3))
        self.assertEqual(len(t), 2)
        self.assertEqual(t.duplicates, 1)

    def test_shared_prefix_compression(self):
        t = SharedPrefixTable(3)
        for tup in [(0, 0, 0), (0, 0, 1), (0, 1, 0), (0, 1, 1)]:
            t.add(tup)
        # 不压缩需 4*3=12 槽；trie 共享前缀后节点更少
        self.assertLess(t.node_count(), t.naive_slots())

    def test_arity_mismatch_rejected(self):
        t = SharedPrefixTable(2)
        with self.assertRaises(ValueError):
            t.add((1, 2, 3))


class TestEdgeCases(unittest.TestCase):
    def test_empty_positive_table_infeasible(self):
        self.assertIsNone(solve({"x": {0, 1}}, [PositiveTable(["x"], [])]))

    def test_empty_negative_table_no_restriction(self):
        cons = [NegativeTable(["x"], [])]
        sol = solve({"x": {0, 1, 2}}, cons)
        self.assertIsNotNone(sol)
        self.assertTrue(check_solution(sol, cons))

    def test_domain_exhaustion_raises(self):
        e = Engine({"x": {0}}, [PositiveTable(["x"], [(0,)])])
        with self.assertRaises(Infeasible):
            e.remove_value("x", 0)


class TestSupportMaintenance(unittest.TestCase):
    def test_value_removal_invalidates_supports_synchronously(self):
        c = PositiveTable(["x", "y"], [(0, 0), (0, 1), (1, 1)])
        e = Engine({"x": {0, 1}, "y": {0, 1}}, [c])
        self.assertEqual(c.live_tuple_count(), 3)
        e.remove_value("x", 0)
        self.assertEqual(c.live_tuple_count(), 1)          # 两条元组同步失效
        self.assertEqual(c.supports[("x", 0)], 0)
        self.assertEqual(c.supports[("y", 1)], 1)

    def test_cascade_pruning_when_support_lost(self):
        c = PositiveTable(["x", "y"], [(0, 0), (1, 1)])
        e = Engine({"x": {0, 1}, "y": {0, 1}}, [c])
        e.remove_value("x", 0)
        self.assertEqual(e.domains["y"], {1})              # y=0 失去支持被剪枝

    def test_duplicates_not_double_counted_in_supports(self):
        c = PositiveTable(["x", "y"], [(0, 0), (0, 0), (0, 0), (1, 1)])
        e = Engine({"x": {0, 1, 2}, "y": {0, 1}}, [c])
        self.assertEqual(len(c.table), 2)                 # 重复元组只存一次
        self.assertEqual(c.supports[("x", 0)], 1)         # 3 份重复只计一次
        e.remove_value("x", 1)
        # (1,1) 的两个位置均失效：x=1 被删，y=1 因失去支持被级联剪枝
        self.assertEqual(c.bad, [0, 2])
        self.assertEqual(c.live_tuple_count(), 1)

    def test_undo_restores_domains_and_supports(self):
        c = PositiveTable(["x", "y"], [(0, 0), (0, 1), (1, 1)])
        e = Engine({"x": {0, 1}, "y": {0, 1}}, [c])
        snapshot_supports = dict(c.supports)
        mark = e.mark()
        e.remove_value("x", 0)
        e.undo(mark)
        self.assertEqual(e.domains["x"], {0, 1})
        self.assertEqual(c.supports, snapshot_supports)
        self.assertEqual(c.live_tuple_count(), 3)


class TestNegativeTable(unittest.TestCase):
    def test_forced_forbidden_tuple_is_conflict(self):
        cons = [NegativeTable(["x", "y"], [(1, 1)])]
        self.assertIsNone(solve({"x": {1}, "y": {1}}, cons))

    def test_last_free_value_pruned(self):
        c = NegativeTable(["a", "b"], [(1, 1)])
        e = Engine({"a": {0, 1}, "b": {0, 1}}, [c])
        e.remove_value("a", 0)                              # a=1 确定
        self.assertEqual(e.domains["b"], {0})               # b=1 被禁表剪枝

    def test_unary_negative_table(self):
        sol = solve({"x": {0, 1, 2}}, [NegativeTable(["x"], [(1,)])])
        self.assertIsNotNone(sol)
        self.assertNotEqual(sol["x"], 1)


class TestSolverAndChecker(unittest.TestCase):
    def test_solution_passes_independent_checker(self):
        cons = [PositiveTable(["x", "y"], [(0, 1), (1, 0), (1, 1)]),
                NegativeTable(["x", "y"], [(1, 1)])]
        sol = solve({"x": {0, 1}, "y": {0, 1}}, cons)
        self.assertIsNotNone(sol)
        self.assertTrue(check_solution(sol, cons))

    def test_checker_rejects_forged_solution(self):
        cons = [PositiveTable(["x", "y"], [(0, 1), (1, 0)]),
                NegativeTable(["x", "y"], [(1, 1)])]
        self.assertFalse(check_solution({"x": 1, "y": 1}, cons))  # 命中禁表
        self.assertFalse(check_solution({"x": 0, "y": 0}, cons))  # 不在正表

    def test_unsat_problem(self):
        cons = [PositiveTable(["x"], [(0,)]),
                NegativeTable(["x"], [(0,)])]
        self.assertIsNone(solve({"x": {0, 1}}, cons))

    def test_multi_constraint_problem(self):
        # 图着色：3 个节点一条路径，相邻不同色
        edge = [(0, 1), (1, 0), (0, 2), (2, 0), (1, 2), (2, 1)]
        cons = [PositiveTable(["a", "b"], edge),
                PositiveTable(["b", "c"], edge),
                NegativeTable(["a", "c"], [(0, 0), (1, 1), (2, 2)])]
        sol = solve({"a": {0, 1, 2}, "b": {0, 1, 2}, "c": {0, 1, 2}}, cons)
        self.assertIsNotNone(sol)
        self.assertTrue(check_solution(sol, cons))
        self.assertNotEqual(sol["a"], sol["b"])
        self.assertNotEqual(sol["b"], sol["c"])

    def test_scope_must_have_distinct_vars(self):
        with self.assertRaises(ValueError):
            TableConstraint(["x", "x"], [(0, 1)])


if __name__ == "__main__":
    unittest.main(verbosity=2)
