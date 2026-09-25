import unittest

from timemap import Interval, IntervalSet, PiecewiseAffineMap, Segment

from .demo_map import build_demo_map


class TestPointMapping(unittest.TestCase):
    def setUp(self):
        self.m = build_demo_map()

    def test_value_at(self):
        self.assertEqual(self.m.value_at(0), 0)
        self.assertEqual(self.m.value_at(25), 10)   # 反向段中点
        self.assertEqual(self.m.value_at(35), 15)   # 倍速段

    def test_pause_collapses_endpoints(self):
        # 暂停 [5,10)：5 和 10 映射到同一逻辑点
        self.assertEqual(self.m.value_at(5), self.m.value_at(10))
        self.assertEqual(self.m.value_at(5), 5)
        self.assertEqual(self.m.value_at(7), 5)     # 暂停期间逻辑时间不动

    def test_unmapped_point(self):
        self.assertIsNone(self.m.value_at(40))


class TestImage(unittest.TestCase):
    def setUp(self):
        self.m = build_demo_map()

    def test_full_image(self):
        res = self.m.image(IntervalSet.of(Interval(0, 40, True, False)))
        self.assertEqual(res.values, IntervalSet.of(Interval(0, 25, True, False)))

    def test_pause_image_is_point(self):
        res = self.m.image(IntervalSet.of(Interval(5, 10, True, False)))
        self.assertEqual(res.values, IntervalSet.point(5))

    def test_reverse_image_swaps_openness(self):
        res = self.m.image(IntervalSet.of(Interval(20, 30, True, False)))
        self.assertEqual(
            res.values, IntervalSet.of(Interval(5, 15, False, True))
        )

    def test_image_evidence(self):
        res = self.m.image(IntervalSet.of(Interval(0, 40, True, False)))
        seg_ids = {c.seg_id for c in res.evidence}
        self.assertEqual(seg_ids, {"s1", "p1", "s2", "r1", "v1"})
        for c in res.evidence:
            self.assertIsNotNone(c.source)
            self.assertIsNotNone(c.target)


class TestPreimage(unittest.TestCase):
    def setUp(self):
        self.m = build_demo_map()

    def test_constant_segment_returns_whole_domain(self):
        # 逻辑点 5 的原像：暂停段整段 [5,10)，加上相邻段贡献的边界点
        res = self.m.preimage(IntervalSet.point(5))
        self.assertEqual(
            res.values,
            IntervalSet.of(Interval(5, 10, True, True), Interval.point(30)),
        )
        # 边界选择：暂停段给出 [5,10)，s2 给出点 10，v1 给出点 30
        by_seg = {c.seg_id: c.source for c in res.evidence}
        self.assertEqual(by_seg["p1"], Interval(5, 10, True, False))
        self.assertEqual(by_seg["s2"], Interval.point(10))
        self.assertEqual(by_seg["v1"], Interval.point(30))

    def test_open_interval_preimage(self):
        res = self.m.preimage(IntervalSet.of(Interval(5, 15, False, False)))
        self.assertEqual(
            res.values,
            IntervalSet.of(
                Interval(10, 20, False, False),
                Interval(20, 30, False, False),
                Interval(30, 35, False, False),
            ),
        )

    def test_pause_not_hit(self):
        # 目标不含逻辑点 5 时，暂停段不贡献原像
        res = self.m.preimage(IntervalSet.of(Interval(6, 8, True, True)))
        seg_ids = {c.seg_id for c in res.evidence}
        self.assertNotIn("p1", seg_ids)

    def test_preimage_evidence_records_target(self):
        res = self.m.preimage(IntervalSet.point(5))
        for c in res.evidence:
            self.assertEqual(c.target, Interval.point(5))


class TestHugeIntervals(unittest.TestCase):
    def test_huge_domain_by_breakpoints(self):
        m = build_demo_map()
        big = IntervalSet.of(Interval(0, 10**15, True, False))
        # 像只覆盖映射定义域 [0,40)，与巨大输入求交即可，不逐 tick 展开
        res = m.image(big)
        self.assertEqual(res.values, IntervalSet.of(Interval(0, 25, True, False)))
        pre = m.preimage(IntervalSet.of(Interval(0, 10**15, True, False)))
        self.assertEqual(
            pre.values, IntervalSet.of(Interval(0, 40, True, False))
        )

    def test_huge_pause(self):
        m = PiecewiseAffineMap(
            [Segment(Interval(0, 10**18, True, False), 0, 42, "big", "pause")]
        )
        res = m.preimage(IntervalSet.point(42))
        self.assertEqual(
            res.values, IntervalSet.of(Interval(0, 10**18, True, False))
        )
        self.assertEqual(m.value_at(10**18 - 1), 42)


if __name__ == "__main__":
    unittest.main()
