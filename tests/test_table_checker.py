import unittest

from timemap import Interval, OffsetTable, PiecewiseAffineMap, Segment, sample_points

from .demo_map import build_demo_map, build_second_map


class TestOffsetTable(unittest.TestCase):
    def test_table_matches_point_map(self):
        m = build_demo_map()
        table = OffsetTable(m)
        for x in range(0, 45):
            self.assertEqual(table.convert(x), m.value_at(x), f"x={x}")

    def test_table_of_composed_map(self):
        f, g = build_demo_map(), build_second_map()
        h = f.compose(g)
        table = h.offset_table()
        for x in range(0, 40):
            fv = f.value_at(x)
            expect = g.value_at(fv) if fv is not None else None
            self.assertEqual(table.convert(x), expect, f"x={x}")

    def test_table_respects_openness(self):
        m = build_demo_map()
        table = OffsetTable(m)
        self.assertIsNone(table.convert(40))   # 定义域 [0,40) 之外
        self.assertEqual(table.convert(10), 5)  # 边界归属右侧段


class TestCheckerSampling(unittest.TestCase):
    def test_samples_are_breakpoint_based(self):
        big = PiecewiseAffineMap(
            [Segment(Interval(0, 10**18, True, False), 1, 0, "big", "normal")]
        )
        pts = sample_points(big)
        # 单段只产生常数个采样点，绝不逐 tick 展开
        self.assertLessEqual(len(pts), 5)
        self.assertIn(0, pts)

    def test_samples_cover_closed_endpoints(self):
        m = build_demo_map()
        pts = sample_points(m)
        self.assertIn(5, pts)   # 暂停段闭端点
        self.assertIn(10, pts)  # 下一段闭端点


if __name__ == "__main__":
    unittest.main()
