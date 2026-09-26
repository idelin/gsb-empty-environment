"""度量校验：充分条件的接受/拒绝，以及破坏三角不等式的成本构造。"""
import unittest

from uniedit import CostModel, check_metric, distance


class TestMetricAcceptance(unittest.TestCase):
    def test_uniform_no_trans_is_metric(self):
        rep = check_metric(CostModel.uniform(), "abc中é")
        self.assertTrue(rep.ok, rep.violations)

    def test_dominated_trans_is_metric(self):
        # trans=2 == d_notrans("ab","ba")，被支配 => 度量
        rep = check_metric(CostModel.uniform(trans=2.0), "abc")
        self.assertTrue(rep.ok, rep.violations)

    def test_weighted_symmetric_is_metric(self):
        costs = CostModel.from_tables(
            {"a": 1.0, "b": 2.0}, {"a": 1.0, "b": 2.0},
            {("a", "b"): 1.5},
            default_ins=3.0, default_delete=3.0, default_sub=2.0,
        )
        rep = check_metric(costs, "ab")
        self.assertTrue(rep.ok, rep.violations)


class TestMetricRejection(unittest.TestCase):
    def test_cheap_transpose_rejected(self):
        rep = check_metric(CostModel.uniform(trans=1.0), "ab")
        self.assertFalse(rep.ok)
        self.assertTrue(any("换位未被支配" in v for v in rep.violations))

    def test_asymmetric_ins_del_rejected(self):
        costs = CostModel.uniform(ins=1.0, delete=2.0)
        rep = check_metric(costs, "ab")
        self.assertFalse(rep.ok)
        self.assertTrue(any("不对称" in v for v in rep.violations))

    def test_asymmetric_substitute_rejected(self):
        costs = CostModel(
            insert=lambda c: 1.0,
            delete=lambda c: 1.0,
            substitute=lambda a, b: 0.0 if a == b else (0.5 if (a, b) == ("a", "b") else 1.0),
            transpose=lambda a, b: float("inf"),
            min_insert=1.0,
            min_delete=1.0,
        )
        rep = check_metric(costs, "ab")
        self.assertFalse(rep.ok)

    def test_symbol_level_triangle_violation_rejected(self):
        # ins(c)=3 而 ins(b)+sub(b,c)=2：脚本"插入 b 再替换"更便宜，
        # DP 距离本身违反三角不等式（d('','c')=3 > d('','b')+d('b','c')=2）
        costs = CostModel(
            insert=lambda c: 3.0 if c == "c" else 1.0,
            delete=lambda c: 3.0 if c == "c" else 1.0,
            substitute=lambda a, b: 0.0 if a == b else 1.0,
            transpose=lambda a, b: float("inf"),
            min_insert=1.0,
            min_delete=1.0,
        )
        self.assertEqual(distance("", "c", costs), 3.0)
        self.assertGreater(distance("", "c", costs),
                           distance("", "b", costs) + distance("b", "c", costs))
        rep = check_metric(costs, "abc")
        self.assertFalse(rep.ok)
        self.assertTrue(any("符号级三角不等式" in v for v in rep.violations))

    def test_zero_cost_warns_but_ok(self):
        costs = CostModel.uniform(ins=0.0, sub=1.0)
        rep = check_metric(costs, "ab")
        self.assertTrue(rep.ok)  # 伪度量，剪枝仍安全
        self.assertTrue(rep.warnings)


class TestTriangleBreakingCosts(unittest.TestCase):
    def test_constructed_costs_break_triangle(self):
        """构造破坏三角不等式的成本：单位成本 + 便宜相邻换位（OSA 风格）。"""
        costs = CostModel.uniform(trans=1.0)
        d1 = distance("ca", "ac", costs)    # 1：一次换位
        d2 = distance("ac", "abc", costs)   # 1：一次插入
        d3 = distance("ca", "abc", costs)   # 3：OSA 限制下无法组合两步
        self.assertEqual((d1, d2, d3), (1.0, 1.0, 3.0))
        self.assertGreater(d3, d1 + d2)  # 三角不等式确实被破坏
        # 且校验器必须拒绝该成本模型
        self.assertFalse(check_metric(costs, "abc").ok)


if __name__ == "__main__":
    unittest.main()
