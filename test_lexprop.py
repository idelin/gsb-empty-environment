"""字典序约束传播器的 unittest 测试套件。

运行：python3.11 -m unittest -v
"""
import unittest

import checker
from lexprop import Contradiction, LexConstraint, Propagator, Variable


def make(domains_by_name):
    """根据 {名字: 域} 创建变量并返回 (变量字典, 域字典)。"""
    variables = {name: Variable(name) for name in domains_by_name}
    domains = {variables[name]: set(dom) for name, dom in domains_by_name.items()}
    return variables, domains


class LexPropTest(unittest.TestCase):
    # ---------- 基础过滤与证明 ----------

    def test_prefix_equality_forces_pruning(self):
        """前缀被迫相等时，后位按序过滤：x1=3 无支持被删。"""
        v, domains = make({"x0": {2}, "x1": {1, 2, 3}, "y0": {2}, "y1": {2}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"], v["y1"]], strict=False)
        prop = Propagator(con, domains)
        result = prop.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(prop.domains[v["x1"]], {1, 2})
        self.assertEqual(prop.domains[v["y1"]], {2})
        # 独立检验：保留域恰好是所有支持值
        ok, _ = checker.verify_pruned_domains(con, domains, prop.domains)
        self.assertTrue(ok)

    def test_deleted_value_carries_proof(self):
        """每个被删值都附带由原约束导出的证明。"""
        v, domains = make({"x0": {2}, "x1": {1, 2, 3}, "y0": {2}, "y1": {2}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"], v["y1"]], strict=False)
        prop = Propagator(con, domains)
        prop.propagate()
        self.assertEqual(len(prop.deletions), 1)
        deletion = prop.deletions[0]
        self.assertEqual((deletion.var.name, deletion.value), ("x1", 3))
        self.assertGreater(len(deletion.explanation.failures), 0)
        text = deletion.explanation.render()
        self.assertIn("x1", text)
        self.assertIn("3", text)
        self.assertIn("情形", text)

    def test_cascade_until_fixpoint(self):
        """删除会级联：x0=2 被删后其余值才获得支持，需迭代到固定点。"""
        v, domains = make({"x0": {1, 2}, "x1": {1}, "y0": {2}, "y1": {1}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"], v["y1"]], strict=True)
        prop = Propagator(con, domains)
        result = prop.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(prop.domains[v["x0"]], {1})
        ok, _ = checker.verify_pruned_domains(con, domains, prop.domains)
        self.assertTrue(ok)

    def test_earlier_position_decided_frees_later(self):
        """前位已严格更小，则后位不受任何额外约束（无剪枝）。"""
        v, domains = make({"x0": {1}, "x1": {1, 2, 3}, "y0": {2}, "y1": {1, 2, 3}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"], v["y1"]], strict=True)
        prop = Propagator(con, domains)
        result = prop.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(result.deletions, [])
        self.assertEqual(prop.domains[v["x1"]], {1, 2, 3})
        self.assertEqual(prop.domains[v["y1"]], {1, 2, 3})

    # ---------- 相同向量 ----------

    def test_same_vector_strict_unsat(self):
        """相同变量向量只满足非严格：X < X 直接矛盾。"""
        v, domains = make({"a": {1, 2}, "b": {1, 2}})
        con = LexConstraint([v["a"], v["b"]], [v["a"], v["b"]], strict=True)
        prop = Propagator(con, domains)
        with self.assertRaises(Contradiction):
            prop.propagate()

    def test_same_vector_nonstrict_no_pruning(self):
        """X <= X 恒成立，任何值都不被删。"""
        v, domains = make({"a": {1, 2}, "b": {1, 2}})
        con = LexConstraint([v["a"], v["b"]], [v["a"], v["b"]], strict=False)
        prop = Propagator(con, domains)
        result = prop.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(result.deletions, [])

    # ---------- 不同长度与空向量 ----------

    def test_different_lengths_strict(self):
        """严格 + 不同长度：x0=2 因全等前缀交集为空被删。"""
        v, domains = make({"x0": {1, 2}, "y0": {1}, "y1": {1, 2}})
        con = LexConstraint([v["x0"]], [v["y0"], v["y1"]], strict=True)
        prop = Propagator(con, domains)
        result = prop.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(prop.domains[v["x0"]], {1})
        ok, _ = checker.verify_pruned_domains(con, domains, prop.domains)
        self.assertTrue(ok)

    def test_longer_left_nonstrict_unsat_when_forced_equal(self):
        """非严格但左长右短且前缀被迫全等：违反长度规则，矛盾。"""
        v, domains = make({"x0": {1}, "x1": {1, 2}, "y0": {1}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"]], strict=False)
        prop = Propagator(con, domains)
        with self.assertRaises(Contradiction):
            prop.propagate()

    def test_longer_left_ok_when_first_diff_smaller(self):
        """左长右短但首位已小：可行且后位不受约束。"""
        v, domains = make({"x0": {1}, "x1": {1, 2}, "y0": {2}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"]], strict=False)
        prop = Propagator(con, domains)
        result = prop.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(prop.domains[v["x1"]], {1, 2})

    def test_empty_vectors_length_rule(self):
        """空向量长度规则一致：[]<=[] 成立，[]<[] 矛盾，[]<[y] 成立。"""
        v, domains = make({"y0": {1, 2}})
        # [] <= [] 可行，无变量可删
        con_le = LexConstraint([], [], strict=False)
        prop_le = Propagator(con_le, {})
        self.assertTrue(prop_le.propagate().fixpoint)
        # [] < [] 矛盾
        con_lt = LexConstraint([], [], strict=True)
        with self.assertRaises(Contradiction):
            Propagator(con_lt, {}).propagate()
        # [] < [y0] 恒成立（0 < 1），y0 无剪枝
        con_lt_y = LexConstraint([], [v["y0"]], strict=True)
        prop_lt_y = Propagator(con_lt_y, domains)
        result = prop_lt_y.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(prop_lt_y.domains[v["y0"]], {1, 2})
        # [] <= [y0] 同样恒成立
        con_le_y = LexConstraint([], [v["y0"]], strict=False)
        self.assertTrue(Propagator(con_le_y, domains).propagate().fixpoint)

    # ---------- 共享变量 ----------

    def test_shared_variables(self):
        """共享变量：(a,b) < (b,a) 等价于 a < b，过滤到 a<={1,2}, b>={2,3}。"""
        v, domains = make({"a": {1, 2, 3}, "b": {1, 2, 3}})
        con = LexConstraint([v["a"], v["b"]], [v["b"], v["a"]], strict=True)
        prop = Propagator(con, domains)
        result = prop.propagate()
        self.assertTrue(result.fixpoint)
        self.assertEqual(prop.domains[v["a"]], {1, 2})
        self.assertEqual(prop.domains[v["b"]], {2, 3})
        ok, _ = checker.verify_pruned_domains(con, domains, prop.domains)
        self.assertTrue(ok)

    # ---------- push / pop ----------

    def test_push_pop_restores_domains_and_cache(self):
        """push/pop 恢复域与前缀支持缓存。"""
        v, domains = make({"x0": {2}, "x1": {1, 2, 3}, "y0": {2}, "y1": {2}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"], v["y1"]], strict=False)
        prop = Propagator(con, domains)
        prop.propagate()
        saved_domains = {var: set(dom) for var, dom in prop.domains.items()}
        saved_cache = dict(prop._prefix_cache)
        saved_deletions = len(prop.deletions)
        prop.push()
        # 破坏状态：手动删值并清空缓存
        prop.domains[v["x1"]].discard(1)
        prop._prefix_cache.clear()
        prop.deletions.clear()
        prop.pop()
        self.assertEqual(prop.domains, saved_domains)
        self.assertEqual(prop._prefix_cache, saved_cache)
        self.assertEqual(len(prop.deletions), saved_deletions)

    def test_pop_without_push_raises(self):
        v, domains = make({"a": {1}})
        prop = Propagator(LexConstraint([v["a"]], [v["a"]]), domains)
        with self.assertRaises(IndexError):
            prop.pop()

    # ---------- 预算 ----------

    def test_budget_exhausted_reports_not_fixpoint(self):
        """预算不足时必须明确说明尚未达到传播固定点。"""
        v, domains = make({"x0": {1, 2}, "x1": {1}, "y0": {2}, "y1": {1}})
        con = LexConstraint([v["x0"], v["x1"]], [v["y0"], v["y1"]], strict=True)
        prop = Propagator(con, domains)
        result = prop.propagate(budget=1)
        self.assertFalse(result.fixpoint)
        self.assertEqual(result.status, "budget_exhausted")
        self.assertIn("尚未达到传播固定点", result.message)
        # 预算充足时同一问题达到固定点
        prop2 = Propagator(con, domains)
        self.assertTrue(prop2.propagate().fixpoint)

    # ---------- 有解断言由独立检验器验证 ----------

    def test_sat_claims_verified_by_independent_checker(self):
        """多个场景：传播到固定点后，独立检验器找到并验证原约束的解。"""
        scenarios = []
        v1, d1 = make({"x0": {2}, "x1": {1, 2, 3}, "y0": {2}, "y1": {2}})
        scenarios.append((LexConstraint([v1["x0"], v1["x1"]], [v1["y0"], v1["y1"]]), d1))
        v2, d2 = make({"a": {1, 2, 3}, "b": {1, 2, 3}})
        scenarios.append((LexConstraint([v2["a"], v2["b"]], [v2["b"], v2["a"]], strict=True), d2))
        v3, d3 = make({"x0": {1, 2}, "y0": {1}, "y1": {1, 2}})
        scenarios.append((LexConstraint([v3["x0"]], [v3["y0"], v3["y1"]], strict=True), d3))
        for con, domains in scenarios:
            with self.subTest(con=con.describe()):
                prop = Propagator(con, domains)
                self.assertTrue(prop.propagate().fixpoint)
                witness = checker.find_witness(con, prop.domains)
                self.assertIsNotNone(witness)
                self.assertTrue(checker.evaluate(con, witness))
                ok, _ = checker.verify_pruned_domains(con, domains, prop.domains)
                self.assertTrue(ok)

    def test_contradiction_carries_proof(self):
        """矛盾时抛出的 Contradiction 携带各情形失败原因。"""
        v, domains = make({"x0": {1}, "y0": {1}})
        con = LexConstraint([v["x0"]], [v["y0"]], strict=True)
        prop = Propagator(con, domains)
        with self.assertRaises(Contradiction) as ctx:
            prop.propagate()
        self.assertGreater(len(ctx.exception.explanation.failures), 0)


if __name__ == "__main__":
    unittest.main()
