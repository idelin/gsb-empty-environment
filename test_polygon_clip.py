"""polygon_clip 的 unittest 测试。"""

import unittest
from fractions import Fraction as F

from polygon_clip import clip_polygon, polygon_area

WINDOW_SQ = [(0, 0), (10, 0), (10, 10), (0, 10)]


def vset(region):
    return set(region.vertices)


class ClippingTests(unittest.TestCase):
    def test_square_overlap_and_sources(self):
        subject = [(0, 0), (4, 0), (4, 4), (0, 4)]
        window = [(1, 1), (5, 1), (5, 5), (1, 5)]
        result = clip_polygon(subject, window)
        self.assertEqual(len(result.regions), 1)
        r = result.regions[0]
        self.assertFalse(r.degenerate)
        self.assertEqual(r.vertices, ((1, 1), (4, 1), (4, 4), (1, 4)))
        self.assertEqual(r.area, 9)
        self.assertEqual(
            r.edge_sources,
            (("clip", 0), ("subject", 1), ("subject", 2), ("clip", 3)),
        )

    def test_disjoint_is_empty(self):
        subject = [(0, 0), (1, 0), (1, 1)]
        window = [(5, 5), (7, 5), (7, 7), (5, 7)]
        self.assertEqual(clip_polygon(subject, window).regions, ())

    def test_two_connected_regions(self):
        # 拱门形：底部连通、上方两根柱；窗口只保留柱子顶部
        subject = [(0, 0), (10, 0), (10, 4), (8, 4), (8, 2), (2, 2), (2, 4), (0, 4)]
        window = [(0, 3), (10, 3), (10, 8), (0, 8)]
        result = clip_polygon(subject, window)
        solids = result.solid
        self.assertEqual(len(solids), 2)
        self.assertEqual({r.area for r in solids}, {F(2), F(2)})
        self.assertEqual(vset(solids[0]), {(0, 3), (2, 3), (2, 4), (0, 4)})
        self.assertEqual(vset(solids[1]), {(8, 3), (10, 3), (10, 4), (8, 4)})
        self.assertEqual(len(result.degenerate), 0)
        self.assertEqual(result.total_area, 4)

    def test_subject_edge_exactly_on_window_edge_kept(self):
        subject = [(0, 0), (4, 0), (2, 3)]
        result = clip_polygon(subject, WINDOW_SQ)
        self.assertEqual(len(result.regions), 1)
        r = result.regions[0]
        self.assertEqual(r.vertices, ((0, 0), (4, 0), (2, 3)))
        self.assertEqual(r.edge_sources[0], ("subject", 0))
        self.assertEqual(r.area, 6)

    def test_point_touch_is_degenerate(self):
        subject = [(2, -2), (6, -2), (4, 0)]
        result = clip_polygon(subject, WINDOW_SQ)
        self.assertEqual(len(result.regions), 1)
        r = result.regions[0]
        self.assertTrue(r.degenerate)
        self.assertEqual(r.vertices, ((4, 0),))
        self.assertEqual(r.area, 0)

    def test_flush_edge_outside_is_degenerate_segment(self):
        subject = [(2, 0), (6, 0), (4, -3)]
        result = clip_polygon(subject, WINDOW_SQ)
        self.assertEqual(len(result.regions), 1)
        r = result.regions[0]
        self.assertTrue(r.degenerate)
        self.assertEqual(r.vertices, ((2, 0), (6, 0)))
        self.assertEqual(r.area, 0)

    def test_whisker_two_solids_plus_segment(self):
        # 两个实体矩形，加上一条贴在窗口边界上把它们“连通”起来的细线段
        subject = [(2, 0), (4, 0), (4, 3), (6, 3), (6, -2), (1, -2), (1, 3), (2, 3)]
        result = clip_polygon(subject, WINDOW_SQ)
        solids = result.solid
        degs = result.degenerate
        self.assertEqual(len(solids), 2)
        self.assertEqual(result.total_area, 9)
        self.assertEqual(vset(solids[0]), {(1, 0), (2, 0), (2, 3), (1, 3)})
        self.assertEqual(vset(solids[1]), {(4, 0), (6, 0), (6, 3), (4, 3)})
        self.assertEqual(len(degs), 1)
        self.assertEqual(degs[0].vertices, ((2, 0), (4, 0)))
        kinds = {k for k, _ in degs[0].edge_sources}
        self.assertIn("subject", kinds)

    def test_rational_coordinates_exact(self):
        subject = [(0, 0), (1, 0), (0, 1)]
        window = [(0, 0), (F(2, 3), 0), (F(2, 3), 1), (0, 1)]
        result = clip_polygon(subject, window)
        r = result.regions[0]
        self.assertEqual(
            r.vertices,
            ((0, 0), (F(2, 3), 0), (F(2, 3), F(1, 3)), (0, 1)),
        )
        self.assertEqual(r.area, F(4, 9))
        for vertex in r.vertices:
            self.assertIsInstance(vertex[0], F)

    def test_string_rationals_accepted(self):
        result = clip_polygon(
            [("0", "0"), ("3/2", "0"), ("3/2", "3/2"), ("0", "3/2")],
            WINDOW_SQ,
        )
        self.assertEqual(result.total_area, F(9, 4))

    def test_big_window_is_identity_and_area_equals(self):
        subject = [(1, 1), (7, 2), (6, 5), (2, 4)]
        result = clip_polygon(subject, [(-10, -10), (20, -10), (20, 20), (-10, 20)])
        self.assertEqual(len(result.regions), 1)
        self.assertEqual(result.regions[0].vertices, tuple(tuple(p) for p in subject))
        self.assertEqual(result.total_area, polygon_area(subject))

    def test_cw_subject_gives_same_ccw_regions(self):
        subject = [(0, 0), (4, 0), (4, 4), (0, 4)]
        result_ccw = clip_polygon(subject, [(1, 1), (5, 1), (5, 5), (1, 5)])
        result_cw = clip_polygon(list(reversed(subject)), [(1, 1), (5, 1), (5, 5), (1, 5)])
        # 几何完全一致；来源编号各自参照自己的输入顺序，故只比较顶点
        self.assertEqual(
            [r.vertices for r in result_ccw.regions],
            [r.vertices for r in result_cw.regions],
        )
        r = result_ccw.regions[0]
        self.assertGreater(r.signed_area, 0)

    def test_window_given_clockwise(self):
        subject = [(0, 0), (4, 0), (4, 4), (0, 4)]
        ccw = clip_polygon(subject, [(1, 1), (5, 1), (5, 5), (1, 5)])
        cw = clip_polygon(subject, [(1, 1), (1, 5), (5, 5), (5, 1)])
        self.assertEqual([r.vertices for r in ccw.regions], [r.vertices for r in cw.regions])

    def test_window_inside_subject(self):
        subject = [(0, 0), (10, 0), (10, 10), (0, 10)]
        window = [(2, 2), (4, 2), (4, 4), (2, 4)]
        r = clip_polygon(subject, window).regions[0]
        self.assertEqual(r.vertices, ((2, 2), (4, 2), (4, 4), (2, 4)))
        self.assertTrue(all(k == "clip" for k, _ in r.edge_sources))

    def test_equal_polygon_and_window(self):
        p = [(0, 0), (4, 0), (4, 4), (0, 4)]
        r = clip_polygon(p, p).regions[0]
        self.assertEqual(r.vertices, tuple(p))
        self.assertTrue(all(k == "subject" for k, _ in r.edge_sources))

    def test_area_never_grows_and_idempotence(self):
        subject = [(0, 0), (7, 0), (7, 3), (5, 3), (5, 5), (2, 5), (2, 3), (0, 3)]
        window = [(1, 1), (6, 1), (6, 6), (1, 6)]
        first = clip_polygon(subject, window)
        self.assertLessEqual(first.total_area, polygon_area(subject))
        for region in first.solid:
            again = clip_polygon(region.vertices, window)
            self.assertEqual(len(again.regions), 1)
            self.assertEqual(again.regions[0].vertices, region.vertices)
            self.assertEqual(again.total_area, region.area)
        for region in first.degenerate:
            for x, y in region.vertices:
                for j in range(len(window)):
                    a = window[j]
                    b = window[(j + 1) % len(window)]
                    self.assertGreaterEqual(
                        (b[0] - a[0]) * (y - a[1]) - (b[1] - a[1]) * (x - a[0]), 0
                    )

    def test_whisker_idempotence(self):
        subject = [(2, 0), (4, 0), (4, 3), (6, 3), (6, -2), (1, -2), (1, 3), (2, 3)]
        first = clip_polygon(subject, WINDOW_SQ)
        for region in first.solid:
            again = clip_polygon(region.vertices, WINDOW_SQ)
            self.assertEqual(again.regions[0].vertices, region.vertices)

    def test_weakly_convex_window_with_collinear_edges(self):
        subject = [(0, 0), (5, 0), (5, 5), (0, 5)]
        window = [(1, 1), (3, 1), (4, 1), (4, 4), (1, 4)]
        r = clip_polygon(subject, window).regions[0]
        self.assertEqual(r.area, 9)


class InvalidInputTests(unittest.TestCase):
    def assert_rejected(self, subject=NotImplemented, window=NotImplemented):
        s = [(0, 0), (4, 0), (4, 4), (0, 4)] if subject is NotImplemented else subject
        w = WINDOW_SQ if window is NotImplemented else window
        with self.assertRaises(ValueError):
            clip_polygon(s, w)

    def test_bowtie_self_intersection(self):
        self.assert_rejected(subject=[(0, 0), (2, 2), (2, 0), (0, 2)])

    def test_nonadjacent_vertex_touch(self):
        # 顶点 (2,2) 落在非邻接边上
        self.assert_rejected(subject=[(0, 2), (4, 2), (4, 0), (2, 2)])

    def test_spike_rejected(self):
        self.assert_rejected(subject=[(0, 0), (2, 0), (1, 0), (1, 1)])

    def test_duplicate_vertex_rejected(self):
        self.assert_rejected(subject=[(0, 0), (2, 0), (2, 0), (0, 2)])

    def test_too_few_vertices(self):
        self.assert_rejected(subject=[(0, 0), (1, 1)])

    def test_zero_area(self):
        self.assert_rejected(subject=[(0, 0), (1, 0), (2, 0)])

    def test_nonconvex_window(self):
        self.assert_rejected(window=[(0, 0), (4, 0), (4, 4), (2, 1), (0, 4)])

    def test_pentagram_window_rejected(self):
        self.assert_rejected(
            window=[(0, 0), (4, 0), (1, 3), (2, -1), (3, 3)]
        )

    def test_float_coordinates_rejected(self):
        with self.assertRaises(TypeError):
            clip_polygon([(0.0, 0), (1, 0), (1, 1)], WINDOW_SQ)

    def test_bad_string_rejected(self):
        with self.assertRaises(ValueError):
            clip_polygon([("x", 0), (1, 0), (1, 1)], WINDOW_SQ)


if __name__ == "__main__":
    unittest.main()


class GeometryInvariantTests(unittest.TestCase):
    def _inside_convex(self, p, window):
        x, y = p
        for j in range(len(window)):
            ax, ay = window[j]
            bx, by = window[(j + 1) % len(window)]
            if (bx - ax) * (y - ay) - (by - ay) * (x - ax) < 0:
                return False
        return True

    def _on_boundary_of_simple(self, p, poly):
        from polygon_clip import _on_segment
        x, y = p
        for j in range(len(poly)):
            a = poly[j]
            b = poly[(j + 1) % len(poly)]
            if _on_segment(a, b, p):
                return True
        inside = False
        for j in range(len(poly)):
            ax, ay = poly[j]
            bx, by = poly[(j + 1) % len(poly)]
            if (ay > y) != (by > y) and x < ax + (bx - ax) * (y - ay) / (by - ay):
                inside = not inside
        return inside

    def test_triangle_window_rational(self):
        subject = [(-2, -1), (3, -1), (3, 4), (-2, 4)]
        window = [(0, 0), (4, 0), (0, 4)]
        result = clip_polygon(subject, window)
        r = result.regions[0]
        self.assertEqual(r.vertices, ((0, 0), (3, 0), (3, 1), (0, 4)))
        self.assertEqual(r.area, F(15, 2))

    def test_nested_windows_area_monotone(self):
        subject = [(1, 0), (6, 1), (7, 6), (0, 5), (-1, 2)]
        big = [(-2, -2), (8, -2), (8, 8), (-2, 8)]
        mid = [(0, 0), (6, 0), (6, 6), (0, 6)]
        small = [(1, 1), (5, 1), (5, 5), (1, 5)]
        areas = [clip_polygon(subject, w).total_area for w in (big, mid, small)]
        self.assertEqual(areas[0], polygon_area(subject))
        self.assertGreaterEqual(areas[0], areas[1])
        self.assertGreaterEqual(areas[1], areas[2])

    def test_degenerate_points_lie_on_both_boundaries(self):
        cases = [
            ([(2, -2), (6, -2), (4, 0)], WINDOW_SQ),
            ([(2, 0), (6, 0), (4, -3)], WINDOW_SQ),
        ]
        for subject, window in cases:
            result = clip_polygon(subject, window)
            self.assertTrue(result.degenerate)
            for region in result.degenerate:
                for p in region.vertices:
                    self.assertTrue(self._inside_convex(p, window))
                    self.assertTrue(self._on_boundary_of_simple(p, subject))

    def test_fuzz_invariants(self):
        import random
        from polygon_clip import _validate_simple_polygon

        rng = random.Random(20260924)

        def convex_window():
            cx, cy = rng.randint(0, 6), rng.randint(0, 6)
            angles = sorted(rng.uniform(0, 6.283) for _ in range(rng.randint(3, 7)))
            pts = []
            for ang in angles:
                r = rng.randint(2, 6)
                pts.append((int(cx + r * 2 * __import__("math").cos(ang)),
                            int(cy + r * 2 * __import__("math").sin(ang))))
            return pts

        def star_subject():
            cx, cy = rng.randint(-4, 10), rng.randint(-4, 10)
            k = rng.randint(4, 9)
            angles = [2 * __import__("math").pi * i / k + rng.uniform(-0.2, 0.2)
                      for i in range(k)]
            angles.sort()
            pts = []
            for ang in angles:
                r = rng.randint(1, 7)
                pts.append((int(cx + r * __import__("math").cos(ang)),
                            int(cy + r * __import__("math").sin(ang))))
            return pts

        for _ in range(300):
            window = convex_window()
            subject = star_subject()
            try:
                result = clip_polygon(subject, window)
            except ValueError:
                continue
            subj_area = polygon_area(subject)
            self.assertLessEqual(result.total_area, subj_area)
            for region in result.regions:
                for p in region.vertices:
                    self.assertTrue(self._inside_convex(p, result.window))
            for region in result.solid:
                _validate_simple_polygon(region.vertices, name="输出区域")
                again = clip_polygon(region.vertices, window)
                self.assertEqual(
                    [r.vertices for r in again.solid], [region.vertices]
                )
            rotated = list(window[1:]) + [window[0]]
            result2 = clip_polygon(subject, rotated)
            self.assertEqual(result.total_area, result2.total_area)
            self.assertEqual(
                sorted(r.vertices for r in result.solid),
                sorted(r.vertices for r in result2.solid),
            )


if __name__ == "__main__":
    unittest.main()
