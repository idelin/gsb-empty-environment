"""校正主流程测试：最少删除、冲突核、查询界、外推、连续性声明。"""

import unittest
from fractions import Fraction as F

from clockalign import (
    ExtrapolationError,
    JunctionSideError,
    WallTime,
    correct,
    make_anchor,
    reconcile,
)
from clockalign.model import make_model
from clockalign.reconcile import UndecidedError
from clockalign.synthetic import (
    BREAKPOINTS,
    MAX_DRIFT,
    TRUE_RATES,
    make_synthetic_table,
)


class TestSyntheticTable(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.table = make_synthetic_table()
        cls.recon = reconcile(
            cls.table.model,
            cls.table.anchors,
            extrapolation_margin=cls.table.extrapolation_margin,
        )

    def test_min_removal_finds_exactly_outliers(self):
        self.assertEqual(self.recon.status, "optimal")
        self.assertEqual(self.recon.min_removed, 2)
        self.assertEqual(len(self.recon.scenarios), 1)
        removed = self.recon.scenarios[0].removed
        self.assertEqual(removed, frozenset(self.table.outlier_indices))

    def test_optimality_proof_mentions_lower_budget(self):
        self.assertIn("预算 1", self.recon.optimality_proof)
        self.assertIn("最少删除数 = 2", self.recon.optimality_proof)

    def test_conflict_cores_point_at_outliers(self):
        cores = [set(c) for c in self.recon.conflict_cores]
        self.assertIn(set(self.table.outlier_indices), cores)
        for core in cores:
            self.assertTrue(core & set(self.table.outlier_indices))

    def test_representative_respects_declared_constraints(self):
        for sc in self.recon.scenarios:
            for seg, (alpha, _beta) in enumerate(sc.representative):
                self.assertGreaterEqual(alpha, 1 - MAX_DRIFT)
                self.assertLessEqual(alpha, 1 + MAX_DRIFT)
            # 分段交界连续性必须显式满足
            for j, bp in enumerate(BREAKPOINTS):
                a_l, b_l = sc.representative[j]
                a_r, b_r = sc.representative[j + 1]
                self.assertEqual(a_l * bp + b_l, a_r * bp + b_r)

    def test_recovered_rates_close_to_truth(self):
        sc = self.recon.scenarios[0]
        for seg, (alpha, _beta) in enumerate(sc.representative):
            self.assertLess(abs(alpha - TRUE_RATES[seg]), F(1, 500))

    def test_query_bounds_cover_truth(self):
        for p in (0, 300, 600, 999, 1000, 1001, 2000, 2500, 3000, 3600, 4000):
            c = correct(self.recon, WallTime(p))
            truth = self.table.true_reference_at(F(p))
            self.assertLessEqual(c.lower.seconds, truth, f"p={p}")
            self.assertGreaterEqual(c.upper.seconds, truth, f"p={p}")
            self.assertLessEqual(c.lower.seconds, c.upper.seconds)

    def test_extrapolation_margin_enforced(self):
        # 段0 锚点覆盖 [0, 950]，允许外推 200 -> -200 可以，-201 拒绝
        correct(self.recon, WallTime(-200))
        with self.assertRaises(ExtrapolationError):
            correct(self.recon, WallTime(-201))
        correct(self.recon, WallTime(4200))
        with self.assertRaises(ExtrapolationError):
            correct(self.recon, WallTime(4201))

    def test_budget_undecided_is_separate(self):
        recon = reconcile(
            self.table.model, self.table.anchors, budget=1,
            extrapolation_margin=self.table.extrapolation_margin,
        )
        self.assertEqual(recon.status, "undecided_within_budget")
        self.assertIsNone(recon.min_removed)
        self.assertEqual(recon.scenarios, [])
        self.assertTrue(recon.conflict_cores)
        with self.assertRaises(UndecidedError):
            correct(recon, WallTime(100))


class TestDuplicateLocalConflict(unittest.TestCase):
    def test_same_local_different_ref_conflict(self):
        anchors = [
            make_anchor(0, 100, "0.1", name="P0"),
            make_anchor(10, 110, "0.1", name="P1"),
            make_anchor(10, 115, "0.1", name="P1'"),
            make_anchor(20, 120, "0.1", name="P2"),
        ]
        model = make_model([], rate=(F(1, 2), F(3, 2)))
        recon = reconcile(model, anchors)
        self.assertEqual(recon.min_removed, 1)
        self.assertIn([1, 2], [sorted(c) for c in recon.conflict_cores])
        # 唯一最优方案是删除 P1'（删 P1 会让 P1' 与 P0/P2 漂移超限）
        self.assertEqual(len(recon.scenarios), 1)
        self.assertEqual(recon.scenarios[0].removed, frozenset({2}))

    def test_same_local_overlapping_ref_no_conflict(self):
        anchors = [
            make_anchor(0, 100, 1, name="Q0"),
            make_anchor(10, 110, 1, name="Q1"),
            make_anchor(10, "110.5", 1, name="Q1'"),  # 区间相交，不冲突
            make_anchor(20, 120, 1, name="Q2"),
        ]
        model = make_model([], rate=(F(1, 2), F(3, 2)))
        recon = reconcile(model, anchors)
        self.assertEqual(recon.min_removed, 0)


class TestDeclaredJunctions(unittest.TestCase):
    def setUp(self):
        self.anchors = [
            make_anchor(0, 0, "0.1", name="J0"),
            make_anchor(10, 10, "0.1", name="J1"),
            make_anchor(20, "20.3", "0.1", name="J2"),
            make_anchor(30, "30.3", "0.1", name="J3"),
        ]

    def test_declared_jump_feasible_without_removal(self):
        model = make_model([15], rate=(1, 1), continuous=False, max_jump="0.5")
        recon = reconcile(model, self.anchors, extrapolation_margin=5)
        self.assertEqual(recon.min_removed, 0)
        left = correct(recon, WallTime(15), side="left")
        right = correct(recon, WallTime(15), side="right")
        self.assertEqual(
            (left.lower.seconds, left.upper.seconds), (F(149, 10), F(151, 10))
        )
        self.assertEqual(
            (right.lower.seconds, right.upper.seconds), (F(76, 5), F(77, 5))
        )

    def test_junction_query_requires_side(self):
        model = make_model([15], rate=(1, 1), continuous=False, max_jump="0.5")
        recon = reconcile(model, self.anchors, extrapolation_margin=5)
        with self.assertRaises(JunctionSideError):
            correct(recon, WallTime(15))

    def test_jump_limit_enforced(self):
        # 所需跳变至少 0.1（beta1-beta0 的最小可行值），允许 0.05 -> 必须删锚点
        model = make_model([15], rate=(1, 1), continuous=False, max_jump="0.05")
        recon = reconcile(model, self.anchors)
        self.assertEqual(recon.min_removed, 2)

    def test_forced_continuity_requires_removals(self):
        model = make_model([15], rate=(1, 1), continuous=True)
        recon = reconcile(model, self.anchors)
        self.assertEqual(recon.min_removed, 2)

    def test_continuous_junction_holds_exactly(self):
        model = make_model([15], rate=(F(9, 10), F(11, 10)), continuous=True)
        anchors = [
            make_anchor(0, 0, "0.5"),
            make_anchor(10, 10, "0.5"),
            make_anchor(20, 20, "0.5"),
            make_anchor(30, 30, "0.5"),
        ]
        recon = reconcile(model, anchors)
        self.assertEqual(recon.min_removed, 0)
        for sc in recon.scenarios:
            (a_l, b_l), (a_r, b_r) = sc.representative
            self.assertEqual(a_l * 15 + b_l, a_r * 15 + b_r)


class TestAsymmetricIntervals(unittest.TestCase):
    def test_asymmetric_error_interval(self):
        anchors = [
            make_anchor(0, 100, minus=2, plus="0.5", name="A"),
            make_anchor(10, 110, minus="0.5", plus=2, name="B"),
        ]
        model = make_model([], rate=(F(1, 2), F(3, 2)))
        recon = reconcile(model, anchors)
        self.assertEqual(recon.min_removed, 0)
        c = correct(recon, WallTime(5))
        # 中点处的可行区间由两锚点区间线性插值的包络决定
        self.assertLessEqual(c.lower.seconds, F(105))
        self.assertGreaterEqual(c.upper.seconds, F(105))


if __name__ == "__main__":
    unittest.main()
