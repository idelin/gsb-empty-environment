"""minrect 的 unittest 测试套件。

运行：python3.11 -m unittest test_minrect -v
"""

import itertools
import random
import unittest
from fractions import Fraction as F

from minrect import (
    PointCloud,
    as_point,
    convex_hull,
    min_area_rectangle,
    shoelace2,
    verify_certificate,
)


class ConvexHullTests(unittest.TestCase):
    def test_basic_ccw(self):
        hull = convex_hull([(0, 0), (1, 0), (1, 1), (0, 1), (F(1, 2), F(1, 2))])
        self.assertEqual(
            hull, [(F(0), F(0)), (F(1), F(0)), (F(1), F(1)), (F(0), F(1))]
        )

    def test_collinear_interior_removed(self):
        hull = convex_hull([(0, 0), (1, 0), (2, 0), (3, 0)])
        self.assertEqual(hull, [(F(0), F(0)), (F(3), F(0))])

    def test_duplicates_deduped(self):
        hull = convex_hull([(0, 0), (0, 0), (1, 1), (1, 1)])
        self.assertEqual(hull, [(F(0), F(0)), (F(1), F(1))])

    def test_single_point(self):
        self.assertEqual(convex_hull([(5, 6)]), [(F(5), F(6))])


class DegenerateTests(unittest.TestCase):
    def test_empty_raises(self):
        with self.assertRaises(ValueError):
            min_area_rectangle([])

    def test_single_point(self):
        cert = min_area_rectangle([(3, -2)])
        self.assertEqual(cert.area, 0)
        self.assertIsNone(cert.support_edge)
        self.assertEqual(set(cert.corners), {(F(3), F(-2))})
        self.assertTrue(verify_certificate([(3, -2)], cert))

    def test_two_points(self):
        pts = [(0, 0), (3, 4)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 0)
        self.assertEqual(set(cert.corners), {(F(0), F(0)), (F(3), F(4))})
        self.assertTrue(verify_certificate(pts, cert))

    def test_collinear_exact_zero_large_coords(self):
        pts = [(0, 0), (10**9, 1), (2 * 10**9, 2), (3 * 10**9, 3)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 0)  # 精确为 0，不是 epsilon 意义下的 0
        self.assertTrue(verify_certificate(pts, cert))

    def test_near_collinear_large_coords_no_negative_area(self):
        # 近共线大坐标：浮点叉积容易误判为共线或给出负面积
        N = 10**9
        pts = [(0, 0), (N, 1), (2 * N, 0)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, F(2 * N))  # 精确值
        self.assertGreater(cert.area, 0)
        self.assertTrue(verify_certificate(pts, cert))

    def test_near_collinear_rational(self):
        pts = [(0, 0), (F(10**12, 3), F(1, 7)), (F(2 * 10**12, 3), 0)]
        cert = min_area_rectangle(pts)
        self.assertGreater(cert.area, 0)
        self.assertTrue(verify_certificate(pts, cert))


class MinRectTests(unittest.TestCase):
    def test_axis_aligned_square(self):
        pts = [(0, 0), (2, 0), (2, 2), (0, 2)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 4)
        self.assertEqual(
            cert.corners,
            ((F(0), F(0)), (F(2), F(0)), (F(2), F(2)), (F(0), F(2))),
        )
        self.assertTrue(verify_certificate(pts, cert))

    def test_rotated_square_corners_recover_box(self):
        pts = [(0, 2), (2, 0), (0, -2), (-2, 0)]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 8)
        # 旋转 45 度的正方形：四角应精确恢复原等面积框
        self.assertEqual(set(cert.corners), {as_point(p) for p in pts})
        self.assertEqual(shoelace2(cert.corners), 2 * cert.area)
        self.assertTrue(verify_certificate(pts, cert))

    def test_rotated_rectangle_rational_corners(self):
        # 边长 5 与 2、方向 (3/5, 4/5) 的旋转矩形，角点为有理数
        pts = [(0, 0), (3, 4), (F(7, 5), F(26, 5)), (F(-8, 5), F(6, 5))]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, 10)
        self.assertEqual(set(cert.corners), {as_point(p) for p in pts})
        self.assertTrue(verify_certificate(pts, cert))

    def test_rational_points_exact_area(self):
        pts = [
            (F(1, 3), F(2, 7)),
            (F(5, 3), F(2, 7)),
            (F(5, 3), F(9, 7)),
            (F(1, 3), F(9, 7)),
        ]
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.area, F(4, 3))  # 宽 4/3，高 1
        self.assertTrue(verify_certificate(pts, cert))

    def test_certificate_contents(self):
        pts = [(0, 0), (4, 0), (1, 2)]
        cert = min_area_rectangle(pts)
        # 证书必须包含支撑边与四个极值点
        self.assertIsNotNone(cert.support_edge)
        self.assertEqual(len(cert.extremes), 4)
        pset = {as_point(p) for p in pts}
        for p in cert.extremes:
            self.assertIn(p, pset)
        # 可验证投影范围：所有点的双向投影都落在范围内
        ex, ey = cert.direction
        nx, ny = -ey, ex
        for p in map(as_point, pts):
            u = ex * p[0] + ey * p[1]
            v = nx * p[0] + ny * p[1]
            self.assertLessEqual(cert.proj_along[0], u)
            self.assertLessEqual(u, cert.proj_along[1])
            self.assertLessEqual(cert.proj_across[0], v)
            self.assertLessEqual(v, cert.proj_across[1])


class TieBreakTests(unittest.TestCase):
    # 三角形 (0,0),(4,0),(1,2) 的三条边各给出一个面积均为 8 的
    # 最优矩形，是天然的等面积多解用例。
    PTS = [(0, 0), (4, 0), (1, 2)]
    # 规范决胜：取规范四角元组字典序最小者（手工推出）
    EXPECTED_CORNERS = (
        (F(-3, 13), F(2, 13)),
        (F(36, 13), F(-24, 13)),
        (F(4), F(0)),
        (F(1), F(2)),
    )

    def test_canonical_choice(self):
        cert = min_area_rectangle(self.PTS)
        self.assertEqual(cert.area, 8)
        self.assertEqual(cert.corners, self.EXPECTED_CORNERS)
        self.assertEqual(cert.support_edge, ((F(4), F(0)), (F(1), F(2))))
        self.assertTrue(verify_certificate(self.PTS, cert))

    def test_input_order_irrelevant(self):
        for perm in itertools.permutations(self.PTS):
            cert = min_area_rectangle(list(perm))
            self.assertEqual(cert.corners, self.EXPECTED_CORNERS)
            self.assertEqual(cert.area, 8)

    def test_duplicates_irrelevant(self):
        pts = self.PTS * 5
        cert = min_area_rectangle(pts)
        self.assertEqual(cert.corners, self.EXPECTED_CORNERS)


class PointCloudTests(unittest.TestCase):
    def test_duplicate_coordinate_identity(self):
        cloud = PointCloud()
        handles = cloud.add_batch([(0, 0), (2, 0), (2, 0), (0, 2), (0, 2), (0, 2)])
        self.assertEqual(len(set(handles)), 6)  # 同坐标也有独立身份
        cert = cloud.min_area_rectangle()
        self.assertEqual(cert.area, 4)
        # 几何结果与去重后的点集一致
        self.assertEqual(cert.corners, min_area_rectangle([(0, 0), (2, 0), (0, 2)]).corners)
        # 极值点句柄组汇总了同坐标的所有身份（同一坐标可兼任多个极值角色）
        flat = {h for group in cert.extreme_handles for h in group}
        self.assertEqual(flat, set(handles))
        groups = {ext: grp for ext, grp in zip(cert.extremes, cert.extreme_handles)}
        self.assertEqual(groups[as_point((2, 0))], (1, 2))
        self.assertEqual(groups[as_point((0, 2))], (3, 4, 5))

    def test_batch_removal(self):
        cloud = PointCloud([(0, 0), (4, 0), (4, 3), (0, 3), (1, 1)])
        cert0 = cloud.min_area_rectangle()
        self.assertEqual(cert0.area, 12)
        cloud.remove_batch([1, 3])  # 批量撤回 (4,0) 与 (0,3)
        self.assertEqual(cloud.points(), [as_point(p) for p in [(0, 0), (4, 3), (1, 1)]])
        cert1 = cloud.min_area_rectangle()
        self.assertEqual(cert1.area, 1)
        self.assertTrue(verify_certificate(cloud.points(), cert1))

    def test_remove_unique_extreme_point(self):
        pts = [(0, 0), (2, 0), (2, 2), (0, 2), (5, 1)]
        cloud = PointCloud(pts)
        cert0 = cloud.min_area_rectangle()
        self.assertEqual(cert0.area, 10)
        # (5,1) 是唯一达到最大 x 的极值点，句柄 4 必出现在证书中
        self.assertIn((4,), cert0.extreme_handles)
        cloud.remove(4)
        cert1 = cloud.min_area_rectangle()
        self.assertEqual(cert1.area, 4)  # 退化为单位正方形的包围盒
        self.assertTrue(verify_certificate(cloud.points(), cert1))
        flat = [h for group in cert1.extreme_handles for h in group]
        self.assertNotIn(4, flat)

    def test_invalid_handle_and_empty(self):
        cloud = PointCloud([(0, 0)])
        with self.assertRaises(KeyError):
            cloud.remove(99)
        cloud.remove(0)
        with self.assertRaises(KeyError):
            cloud.remove(0)  # 重复撤回同一身份
        with self.assertRaises(ValueError):
            cloud.min_area_rectangle()  # 撤空后报错


class RandomizedCrossCheckTests(unittest.TestCase):
    def test_random_against_edge_enumeration(self):
        rng = random.Random(20260925)
        for trial in range(300):
            pts = [
                (rng.randint(-50, 50), rng.randint(-50, 50))
                for _ in range(rng.randint(1, 15))
            ]
            cert = min_area_rectangle(pts)
            self.assertGreaterEqual(cert.area, 0, msg=str(pts))
            self.assertEqual(shoelace2(cert.corners), 2 * cert.area, msg=str(pts))
            self.assertTrue(verify_certificate(pts, cert), msg=str(pts))

    def test_random_rational_points(self):
        rng = random.Random(7)
        for trial in range(100):
            pts = [
                (F(rng.randint(-100, 100), rng.randint(1, 9)),
                 F(rng.randint(-100, 100), rng.randint(1, 9)))
                for _ in range(rng.randint(1, 10))
            ]
            cert = min_area_rectangle(pts)
            self.assertTrue(verify_certificate(pts, cert), msg=str(pts))

    def test_large_coordinate_random(self):
        rng = random.Random(99)
        base = 10**12
        for trial in range(100):
            pts = [
                (base + rng.randint(-1000, 1000), base + rng.randint(-1000, 1000))
                for _ in range(rng.randint(2, 10))
            ]
            cert = min_area_rectangle(pts)
            self.assertGreaterEqual(cert.area, 0, msg=str(pts))
            self.assertTrue(verify_certificate(pts, cert), msg=str(pts))


if __name__ == "__main__":
    unittest.main()
