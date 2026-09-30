"""敏感度推导与有限域核验的单元测试（仅合成数据）。"""

import unittest

from finite_check import check_on_finite_domain, format_dataset
from sensitivity import (
    Add,
    Clip,
    Count,
    GroupCount,
    GroupSum,
    Join,
    Scale,
    Sum,
    Table,
    UnboundedContributionError,
    analyze,
)

SUBJECTS = ("u1", "u2")
GRID01 = (0.0, 1.0)


class TestAnalysis(unittest.TestCase):
    def test_sum_bound_is_m_times_b(self):
        t = Table("T", max_rows_per_subject=3, value_lo=0.0, value_hi=2.0)
        bound, deriv = analyze(Sum(t))
        self.assertEqual(bound, 6.0)
        self.assertEqual(deriv.rule, "SUM")

    def test_sum_bound_sign_straddling_domain(self):
        # 值域跨零时替换邻接的紧界为 m*(hi-lo)，而非 m*max(|lo|,|hi|)。
        t = Table("T", max_rows_per_subject=2, value_lo=-1.0, value_hi=1.0)
        bound, _ = analyze(Sum(t))
        self.assertEqual(bound, 4.0)

    def test_count_bound_is_m(self):
        t = Table("T", max_rows_per_subject=4)
        bound, _ = analyze(Count(t))
        self.assertEqual(bound, 4.0)

    def test_clip_tightens_value_bound(self):
        t = Table("T", max_rows_per_subject=2, value_lo=0.0, value_hi=100.0)
        bound, deriv = analyze(Sum(Clip(t, 0.0, 1.5)))
        self.assertEqual(bound, 3.0)
        self.assertEqual(deriv.children[0].rule, "CLIP")

    def test_unbounded_table_rejected(self):
        t = Table("T", max_rows_per_subject=None)
        with self.assertRaises(UnboundedContributionError) as ctx:
            analyze(Sum(t))
        self.assertIsNotNone(ctx.exception.derivation)
        self.assertIn("无界", str(ctx.exception))

    def test_unbounded_count_rejected(self):
        t = Table("T", max_rows_per_subject=None)
        with self.assertRaises(UnboundedContributionError):
            analyze(Count(t))

    def test_join_multiplies_contribution_caps(self):
        left = Table("L", max_rows_per_subject=2, value_hi=1.0)
        right = Table("R", max_rows_per_subject=3, value_hi=2.0)
        bound, deriv = analyze(Sum(Join(left, right)))
        self.assertEqual(bound, 2 * 3 * 1.0 * 2.0)
        self.assertEqual(deriv.children[0].rule, "JOIN")
        cbound, _ = analyze(Count(Join(left, right)))
        self.assertEqual(cbound, 6.0)

    def test_join_with_unbounded_side_rejected(self):
        left = Table("L", max_rows_per_subject=1)
        right = Table("R", max_rows_per_subject=None)
        with self.assertRaises(UnboundedContributionError):
            analyze(Sum(Join(left, right)))
        with self.assertRaises(UnboundedContributionError):
            analyze(Sum(Join(right, left)))

    def test_group_sum_vector_l1_bound(self):
        t = Table(
            "T", max_rows_per_subject=2, value_hi=1.0, groups=("a", "b")
        )
        bound, deriv = analyze(GroupSum(t))
        # 替换邻接：旧 2 行与新 2 行可落入不同组，L1 相加 => 2*m*b = 4。
        self.assertEqual(bound, 4.0)
        self.assertEqual(deriv.rule, "GROUP-SUM")

    def test_group_count_bound(self):
        t = Table("T", max_rows_per_subject=3, groups=("a", "b"))
        bound, _ = analyze(GroupCount(t))
        self.assertEqual(bound, 6.0)

    def test_add_sums_correlated_contributions(self):
        t1 = Table("T1", max_rows_per_subject=2, value_hi=1.0)
        t2 = Table("T2", max_rows_per_subject=1, value_hi=3.0)
        bound, deriv = analyze(Add(Sum(t1), Sum(t2)))
        # 同一主体可同时驱动两侧：2*1 + 1*3 = 5，而非 max(2,3)=3。
        self.assertEqual(bound, 5.0)
        self.assertEqual(deriv.rule, "ADD-COMPOSE")

    def test_same_table_referenced_twice(self):
        t = Table("T", max_rows_per_subject=1, value_hi=1.0)
        bound, _ = analyze(Add(Sum(t), Sum(t)))
        self.assertEqual(bound, 2.0)

    def test_scale(self):
        t = Table("T", max_rows_per_subject=2, value_hi=1.0)
        bound, _ = analyze(Scale(Sum(t), -0.5))
        self.assertEqual(bound, 1.0)

    def test_derivation_tree_render(self):
        t = Table("T", max_rows_per_subject=1, value_hi=1.0)
        _, deriv = analyze(Add(Sum(t), Count(t)))
        text = deriv.render()
        for rule in ("ADD-COMPOSE", "SUM", "COUNT", "TABLE"):
            self.assertIn(rule, text)


class TestFiniteDomain(unittest.TestCase):
    """枚举相邻数据核验上界不被突破，并检查见证达到下界（紧性）。"""

    def test_sum_witness_achieves_bound(self):
        t = Table("T", max_rows_per_subject=2, value_hi=1.0)
        res = check_on_finite_domain(
            Sum(t), [t], SUBJECTS, {"T": GRID01}
        )
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 2.0)
        self.assertEqual(res.max_observed, 2.0)  # 见证达到界
        self.assertIsNotNone(res.witness)

    def test_count_check(self):
        t = Table("T", max_rows_per_subject=2)
        res = check_on_finite_domain(Count(t), [t], SUBJECTS, {"T": GRID01})
        self.assertTrue(res.ok)
        self.assertEqual(res.max_observed, 2.0)

    def test_clip_check(self):
        t = Table("T", max_rows_per_subject=2, value_lo=0.0, value_hi=100.0)
        q = Sum(Clip(t, 0.0, 2.0))
        res = check_on_finite_domain(q, [t], SUBJECTS, {"T": (0.0, 100.0)})
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 4.0)
        self.assertEqual(res.max_observed, 4.0)

    def test_group_sum_check(self):
        t = Table(
            "T", max_rows_per_subject=2, value_hi=1.0, groups=("a", "b")
        )
        res = check_on_finite_domain(
            GroupSum(t), [t], SUBJECTS, {"T": GRID01}
        )
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 4.0)
        self.assertEqual(res.max_observed, 4.0)

    def test_group_count_check(self):
        t = Table("T", max_rows_per_subject=1, groups=("a", "b"))
        res = check_on_finite_domain(
            GroupCount(t), [t], SUBJECTS, {"T": GRID01}
        )
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 2.0)
        self.assertEqual(res.max_observed, 2.0)

    def test_join_check(self):
        left = Table("L", max_rows_per_subject=1, value_hi=1.0)
        right = Table("R", max_rows_per_subject=1, value_hi=1.0)
        grids = {"L": GRID01, "R": GRID01}
        res = check_on_finite_domain(
            Sum(Join(left, right)), [left, right], SUBJECTS, grids
        )
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 1.0)
        self.assertEqual(res.max_observed, 1.0)

    def test_add_same_table_twice_check(self):
        # 同一主体被引用两次：邻接时该主体的行只变一次，但查询读两次，
        # 观测最大差为 2，验证“贡献关联保留、相加而非取 max”。
        t = Table("T", max_rows_per_subject=1, value_hi=1.0)
        res = check_on_finite_domain(
            Add(Sum(t), Sum(t)), [t], SUBJECTS, {"T": GRID01}
        )
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 2.0)
        self.assertEqual(res.max_observed, 2.0)

    def test_add_two_tables_correlated_subject(self):
        # 主体级邻接：同一主体在两张表中的行同时变化，贡献相加。
        t1 = Table("T1", max_rows_per_subject=1, value_hi=1.0)
        t2 = Table("T2", max_rows_per_subject=1, value_hi=1.0)
        grids = {"T1": GRID01, "T2": GRID01}
        res = check_on_finite_domain(
            Add(Sum(t1), Sum(t2)), [t1, t2], SUBJECTS, grids
        )
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 2.0)
        self.assertEqual(res.max_observed, 2.0)

    def test_sum_sign_straddling_check(self):
        t = Table("T", max_rows_per_subject=1, value_lo=-1.0, value_hi=1.0)
        res = check_on_finite_domain(
            Sum(t), [t], SUBJECTS, {"T": (-1.0, 0.0, 1.0)}
        )
        self.assertTrue(res.ok)
        self.assertEqual(res.bound, 2.0)
        self.assertEqual(res.max_observed, 2.0)

    def test_witness_is_valid_adjacent_pair(self):
        t = Table("T", max_rows_per_subject=1, value_hi=1.0)
        res = check_on_finite_domain(Sum(t), [t], SUBJECTS, {"T": GRID01})
        d1, d2, subject = res.witness
        self.assertIn(subject, SUBJECTS)
        text = format_dataset(d1, [t], SUBJECTS)
        self.assertIn("T=", text)


if __name__ == "__main__":
    unittest.main(verbosity=2)
