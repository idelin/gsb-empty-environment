import unittest
from fractions import Fraction as F

from arcsample import (
    BREAK,
    NonRationalLengthError,
    Path,
    PathSampler,
    SeamSampler,
    sample_path,
)


def mileages(samples):
    return [s.mileage for s in samples]


def ids(samples):
    return [s.id for s in samples]


class FixedGridTest(unittest.TestCase):
    def test_length10_spacing3(self):
        """长度10间距3 -> 里程 0、3、6、9、10。"""
        path = Path([(0, 0), (10, 0)])
        result = sample_path(path, 3)
        self.assertTrue(result.complete)
        self.assertEqual(mileages(result.samples), [F(0), F(3), F(6), F(9), F(10)])
        self.assertEqual(ids(result.samples), ["p:v0", "p:g1", "p:g2", "p:g3", "p:v1"])
        for sample, x in zip(result.samples, [0, 3, 6, 9, 10]):
            self.assertEqual(sample.point, (F(x), F(0)))
            self.assertEqual(sample.direction, (F(1), F(0)))
        # 来源参数：g2 在第一段上，t = 3/5
        g2 = result.samples[2]
        self.assertEqual(g2.source.segment, 0)
        self.assertEqual(g2.source.t, F(3, 5))
        self.assertEqual(g2.source.edge, ("v0", "v1"))
        # 关键点保留
        self.assertEqual(result.samples[0].kind, "keypoint")
        self.assertEqual(result.samples[-1].kind, "keypoint")

    def test_keypoint_at_non_grid_mileage_is_kept(self):
        path = Path([(0, 0), (5, 0), (14, 0)])  # 顶点里程 5 不在 3 的网格上
        samples = sample_path(path, 3).samples
        self.assertEqual(mileages(samples), [0, 3, 5, 6, 9, 12, 14])
        self.assertEqual(samples[2].id, "p:v1")
        self.assertEqual(samples[2].kind, "keypoint")


class RationalMileageTest(unittest.TestCase):
    def test_cross_segment_accumulation_is_exact(self):
        """跨段累计用有理里程：1/3 + 1/3 + 1/3 恰好等于 1，无浮点误差。"""
        path = Path([(0, 0), (F(1, 3), 0), (F(2, 3), 0), (1, 0)])
        sub = path.subpaths[0]
        self.assertEqual(sub.cum[-1], F(1))
        samples = sample_path(path, F(1, 2)).samples
        by_id = {s.id: s for s in samples}
        self.assertEqual(by_id["p:g1"].point, (F(1, 2), F(0)))
        self.assertEqual(by_id["p:v3"].mileage, F(1))

    def test_nonrational_length_raises(self):
        """退化定义：长度为 sqrt(2) 的段不属于有理里程域，明确报错而非舍入。"""
        with self.assertRaises(NonRationalLengthError):
            Path([(0, 0), (1, 1)])


class DegenerateTest(unittest.TestCase):
    def test_zero_length_segment_no_cross_interpolation(self):
        """零长段不产生跨越插值：样本只落在非退化段内部或顶点上。"""
        path = Path([(0, 0), (6, 0), (6, 0), (14, 0)])
        sub = path.subpaths[0]
        self.assertEqual(sub.cum, (F(0), F(6), F(6), F(14)))
        samples = sample_path(path, 4).samples
        self.assertEqual(mileages(samples), [0, 4, 6, 6, 8, 12, 14])
        by_id = {s.id: s for s in samples}
        # 里程 8 的样本在第 2 段（零长段之后）内插值，而不是跨越零长段
        self.assertEqual(by_id["p:g2"].source.segment, 2)
        self.assertEqual(by_id["p:g2"].point, (F(8), F(0)))
        # 两个重合顶点各自保留为关键点
        self.assertEqual(by_id["p:v1"].point, (F(6), F(0)))
        self.assertEqual(by_id["p:v2"].point, (F(6), F(0)))
        # 所有样本点都在实际几何上
        for s in samples:
            self.assertTrue(F(0) <= s.point[0] <= F(14))
            self.assertEqual(s.point[1], F(0))

    def test_breakpoint_no_cross_interpolation(self):
        """断点两侧独立采样，里程各自从 0 开始，绝不跨断点插值。"""
        path = Path([(0, 0), (6, 0), BREAK, (100, 0), (106, 0)])
        self.assertEqual(len(path.subpaths), 2)
        samples = sample_path(path, 4).samples
        sub0 = [s for s in samples if s.source.subpath == 0]
        sub1 = [s for s in samples if s.source.subpath == 1]
        self.assertEqual(mileages(sub0), [0, 4, 6])
        self.assertEqual(mileages(sub1), [0, 4, 6])
        self.assertEqual(sub1[0].point, (F(100), F(0)))
        # 不存在位于 (6, 100) 之间的插值点
        for s in samples:
            self.assertFalse(F(6) < s.point[0] < F(100))

    def test_single_point_and_all_zero_path(self):
        single = sample_path(Path([(3, 4)]), 2).samples
        self.assertEqual(len(single), 1)
        self.assertIsNone(single[0].direction)  # 孤立点方向无定义 -> None
        zero = sample_path(Path([(1, 1), (1, 1), (1, 1)]), 2).samples
        self.assertEqual([s.kind for s in zero], ["keypoint"] * 3)
        self.assertEqual(mileages(zero), [0, 0, 0])

    def test_invalid_spacing_and_budget(self):
        path = Path([(0, 0), (1, 0)])
        with self.assertRaises(ValueError):
            sample_path(path, 0)
        with self.assertRaises(ValueError):
            sample_path(path, -1)
        with self.assertRaises(ValueError):
            sample_path(path, 1, budget=-1)


class ClosedPathTest(unittest.TestCase):
    def test_closed_square_fixed_start(self):
        """闭合路径有固定起点；接缝处不重复采样。"""
        path = Path([(0, 0), (4, 0), (4, 4), (0, 4)], closed=True)
        samples = sample_path(path, 3).samples
        self.assertEqual(samples[0].id, "p:v0")
        self.assertTrue(samples[0].seam)
        self.assertEqual(samples[0].point, (F(0), F(0)))
        # 周长 16：网格 3,6,9,12,15；12 与顶点 v3 里程重合被关键点覆盖
        self.assertEqual(mileages(samples), [0, 3, 4, 6, 8, 9, 12, 15])
        self.assertNotIn(F(16), mileages(samples))  # 接缝不重复
        by_id = {s.id: s for s in samples}
        self.assertEqual(by_id["p:g1"].direction, (F(1), F(0)))
        self.assertEqual(by_id["p:v1"].direction, (F(0), F(1)))  # 离开顶点的方向
        self.assertEqual(by_id["p:g5"].point, (F(0), F(1)))


class LocalRepairTest(unittest.TestCase):
    def setUp(self):
        # A(0,0) B(6,8) C(12,8) D(12,0)：段长 10, 6, 8，总长 24
        self.path = Path([(0, 0), (6, 8), (12, 8), (12, 0)])
        self.sampler = PathSampler(self.path, 3)

    def test_move_point_patch_add_move_undo(self):
        """移动中点：校验新增、移动补丁，撤销后完全还原。"""
        before = self.sampler.samples
        self.assertEqual(mileages(before), [0, 3, 6, 9, 10, 12, 15, 16, 18, 21, 24])
        # B(6,8) -> (15,8)：段长变为 17, 3, 8，总长 28，网格新增 g8@24、g9@27
        patch = self.sampler.move_point("v1", (15, 8))
        self.assertEqual(patch.length_delta, F(4))
        self.assertEqual(patch.affected, (F(0), F(16)))  # 旧里程 [cum0, cum2]
        self.assertEqual(patch.new_affected, (F(0), F(20)))

        self.assertEqual(ids(patch.added), ["p:g8", "p:g9"])
        self.assertEqual(patch.removed, ())
        self.assertEqual(
            sorted(m.id for m in patch.moved),
            ["p:g1", "p:g2", "p:g3", "p:g4", "p:g5", "p:g6", "p:g7",
             "p:v0", "p:v1", "p:v2", "p:v3"],
        )
        # 新增样本 g8：第 2 段 t = (24-20)/8 = 1/2；g9：t = (27-20)/8 = 7/8
        g8, g9 = patch.added
        self.assertEqual(g8.mileage, F(24))
        self.assertEqual(g8.point, (F(12), F(4)))
        self.assertEqual(g8.source.segment, 2)
        self.assertEqual(g8.source.t, F(1, 2))
        self.assertEqual(g9.point, (F(12), F(1)))
        # 移动样本：v1 顶点本身
        move_v1 = next(m for m in patch.moved if m.id == "p:v1")
        self.assertEqual(move_v1.old.point, (F(6), F(8)))
        self.assertEqual(move_v1.new.point, (F(15), F(8)))
        self.assertEqual(move_v1.new.mileage, F(17))

        # 补丁应用 == 采样器当前状态 == 独立全量采样
        self.assertEqual(patch.apply(before), self.sampler.samples)
        independent = PathSampler(self.sampler.path, 3)
        self.assertEqual(self.sampler.samples, independent.samples)
        # 撤销
        restored = patch.inverse().apply(self.sampler.samples)
        self.assertEqual(restored, before)

    def test_local_repair_only_touches_affected_range(self):
        """只修复受影响弧长范围；范围外样本保持原样且 ID 稳定。"""
        # A(0,0) B(5,0) C(11,0) D(11,8) E(20,8)：段长 5,6,8,9
        sampler = PathSampler(
            Path([(0, 0), (5, 0), (11, 0), (11, 8), (20, 8)]), 3
        )
        before = sampler.samples
        patch = sampler.move_point("v2", (5, 8))  # 段长变 8,6；总长不变
        self.assertEqual(patch.affected, (F(5), F(19)))  # [cum1, cum3]
        self.assertEqual(patch.length_delta, F(0))
        self.assertEqual(patch.added, ())
        self.assertEqual(patch.removed, ())
        self.assertEqual(
            sorted(m.id for m in patch.moved),
            ["p:g2", "p:g3", "p:g4", "p:g5", "p:g6", "p:v1", "p:v2"],
        )
        # 受影响范围之外（里程 < 5 或 > 19 的样本）完全不动
        touched = {s.id for s in patch.removed} | {m.id for m in patch.moved}
        for s in before:
            if s.mileage < F(5) or s.mileage > F(19):
                self.assertNotIn(s.id, touched)
        # 下游样本 ID 依据原始锚点稳定：g7/g8/g9、v3/v4 原样保留
        after_ids = ids(sampler.samples)
        for stable in ("p:g7", "p:g8", "p:g9", "p:v3", "p:v4"):
            self.assertIn(stable, after_ids)
        # 独立全量采样核验局部更新
        self.assertEqual(patch.apply(before), sampler.samples)
        self.assertEqual(sampler.samples, PathSampler(sampler.path, 3).samples)

    def test_grid_ids_stable_across_edit(self):
        """后续样本 ID 依据原始锚点（间距网格）稳定映射。"""
        patch = self.sampler.move_point("v1", (15, 8))
        after_ids = ids(self.sampler.samples)
        # 网格样本 g1..g7 身份不变（内容可能移动），仅尾部新增 g8
        for k in range(1, 10):
            self.assertIn(f"p:g{k}", after_ids)
        self.assertNotIn("p:g10", after_ids)


class BudgetCursorTest(unittest.TestCase):
    def test_insufficient_budget_returns_resumable_cursor(self):
        """预算不足返回可续游标而非静默减点。"""
        sampler = PathSampler(
            Path([(0, 0), (5, 0), (11, 0), (11, 8), (20, 8)]), 3
        )
        full = sampler.sample()
        self.assertTrue(full.complete)
        self.assertIsNone(full.cursor)
        self.assertEqual(len(full.samples), 14)  # 9 网格 + 5 关键点

        first = sampler.sample(budget=6)
        self.assertFalse(first.complete)  # 明确标记不完整，不静默减点
        self.assertIsNotNone(first.cursor)
        self.assertEqual(len(first.samples), 6)

        rest = sampler.sample(cursor=first.cursor)
        self.assertTrue(rest.complete)
        self.assertEqual(first.samples + rest.samples, full.samples)

    def test_stale_cursor_rejected(self):
        sampler = PathSampler(Path([(0, 0), (10, 0)]), 3)
        partial = sampler.sample(budget=2)
        sampler.move_point("v1", (6, 0))  # 长度 6，有理
        with self.assertRaises(ValueError):
            sampler.sample(cursor=partial.cursor)


class SeamTest(unittest.TestCase):
    def test_shared_edge_reverse_traversal(self):
        """共享边反向遍历产生逆序同点；只计算一次，不各自舍入。"""
        seam = SeamSampler()
        fwd = seam.samples((0, 0), (10, 0), 3)
        rev = seam.samples((10, 0), (0, 0), 3)
        self.assertEqual(seam.cache_size, 1)  # 同一条规范边只算一次
        self.assertIs(fwd, seam.samples((0, 0), (10, 0), 3))

        self.assertEqual([s.mileage for s in fwd], [F(0), F(3), F(6), F(9), F(10)])
        # 反向点序列 == 正向点序列的逆序（完全相同的有理点）
        self.assertEqual([s.point for s in rev], [s.point for s in reversed(fwd)])
        self.assertEqual([s.mileage for s in rev], [F(0), F(1), F(4), F(7), F(10)])
        self.assertEqual(rev[1].t, F(1, 10))
        self.assertEqual(rev[1].direction, (F(-1), F(0)))
        for s in fwd + rev:
            self.assertIsInstance(s.point[0], F)

    def test_shared_edge_identical_across_polylines(self):
        """两条折线共享一条边（方向相反），边上采样逐点一致。"""
        seam = SeamSampler()
        # 折线 1：... -> A -> B -> ...；折线 2：... -> B -> A -> ...
        a, b = (3, 4), (9, 4)
        edge_from_p1 = seam.samples(a, b, 2)
        edge_from_p2 = seam.samples(b, a, 2)
        self.assertEqual(seam.cache_size, 1)
        self.assertEqual(
            [s.point for s in edge_from_p1],
            [s.point for s in reversed(edge_from_p2)],
        )
        # 斜边共享（3-4-5），同样逐点一致
        c, d = (0, 0), (3, 4)
        self.assertEqual(
            [s.point for s in seam.samples(c, d, 2)],
            [s.point for s in reversed(seam.samples(d, c, 2))],
        )

    def test_degenerate_shared_edge(self):
        seam = SeamSampler()
        samples = seam.samples((2, 2), (2, 2), 3)
        self.assertEqual(len(samples), 1)
        self.assertIsNone(samples[0].direction)
        self.assertEqual(samples[0].point, (F(2), F(2)))


if __name__ == "__main__":
    unittest.main()
