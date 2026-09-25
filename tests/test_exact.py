import unittest
from fractions import Fraction as F

from polytri.exact import (
    BOUNDARY,
    INSIDE,
    OUTSIDE,
    area2,
    classify_point,
    collinear_relation,
    on_segment,
    orient,
    proper_cross,
    segments_intersect,
    to_point,
)


class TestExactPredicates(unittest.TestCase):
    def test_orient_signs(self):
        self.assertGreater(orient(to_point((0, 0)), to_point((4, 0)), to_point((0, 3))), 0)
        self.assertLess(orient(to_point((0, 0)), to_point((0, 3)), to_point((4, 0))), 0)
        self.assertEqual(orient(to_point((0, 0)), to_point((1, 1)), to_point((2, 2))), 0)

    def test_orient_exact_rational(self):
        a = to_point((F(1, 3), F(1, 7)))
        b = to_point((F(5, 2), F(2, 9)))
        c = to_point((F(-4, 5), F(8, 3)))
        v = orient(a, b, c)
        self.assertIsInstance(v, F)
        self.assertEqual(v, (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0]))

    def test_area2(self):
        sq = [to_point(p) for p in [(0, 0), (10, 0), (10, 10), (0, 10)]]
        self.assertEqual(area2(sq), F(200))
        self.assertEqual(area2(list(reversed(sq))), F(-200))

    def test_on_segment_exact(self):
        a, b = to_point((0, 0)), to_point((4, 2))
        self.assertTrue(on_segment(a, b, to_point((2, 1))))
        self.assertTrue(on_segment(a, b, to_point((F(7, 2), F(7, 4)))))
        self.assertFalse(on_segment(a, b, to_point((F(7, 2), F(7, 4) + F(1, 10**9)))))
        self.assertFalse(on_segment(a, b, to_point((5, 3))))

    def test_proper_cross(self):
        p1, p2 = to_point((0, 0)), to_point((4, 4))
        q1, q2 = to_point((0, 4)), to_point((4, 0))
        self.assertTrue(proper_cross(p1, p2, q1, q2))
        # touching at an endpoint is not a proper crossing
        self.assertFalse(proper_cross(p1, p2, p2, to_point((8, 0))))
        # collinear overlap is not a proper crossing
        self.assertFalse(proper_cross(to_point((0, 0)), to_point((2, 0)),
                                      to_point((1, 0)), to_point((3, 0))))

    def test_segments_intersect(self):
        self.assertTrue(segments_intersect(to_point((0, 0)), to_point((2, 0)),
                                           to_point((1, 0)), to_point((3, 0))))
        self.assertTrue(segments_intersect(to_point((0, 0)), to_point((2, 2)),
                                           to_point((2, 2)), to_point((3, 1))))
        self.assertFalse(segments_intersect(to_point((0, 0)), to_point((1, 0)),
                                            to_point((0, 1)), to_point((1, 1))))

    def test_collinear_relation(self):
        p, q = to_point((0, 0)), to_point((4, 0))
        self.assertEqual(collinear_relation(p, q, to_point((2, 0)), to_point((6, 0)))[0], "segment")
        kind, r = collinear_relation(p, q, to_point((4, 0)), to_point((6, 0)))
        self.assertEqual(kind, "point")
        self.assertEqual(r, to_point((4, 0)))
        self.assertEqual(collinear_relation(p, q, to_point((5, 0)), to_point((6, 0)))[0], "none")

    def test_classify_point(self):
        ring = [to_point(p) for p in [(0, 0), (10, 0), (10, 10), (0, 10)]]
        self.assertEqual(classify_point(ring, to_point((5, 5))), INSIDE)
        self.assertEqual(classify_point(ring, to_point((15, 5))), OUTSIDE)
        self.assertEqual(classify_point(ring, to_point((10, 5))), BOUNDARY)
        self.assertEqual(classify_point(ring, to_point((10, 10))), BOUNDARY)
        # rational point exactly on the boundary
        self.assertEqual(classify_point(ring, to_point((F(10, 3), 0))), BOUNDARY)
        # ray from this point passes exactly through vertex (10, 10)
        self.assertEqual(classify_point(ring, to_point((5, 10))), BOUNDARY)
        self.assertEqual(classify_point(ring, to_point((F(11, 2), 10))), BOUNDARY)

    def test_classify_point_concave(self):
        ring = [to_point(p) for p in [(0, 0), (8, 0), (8, 4), (4, 4), (4, 8), (0, 8)]]
        self.assertEqual(classify_point(ring, to_point((6, 2))), INSIDE)
        self.assertEqual(classify_point(ring, to_point((6, 6))), OUTSIDE)
        self.assertEqual(classify_point(ring, to_point((2, 6))), INSIDE)
        # ray exactly through the reflex vertex (4, 4)
        self.assertEqual(classify_point(ring, to_point((2, 4))), INSIDE)
        self.assertEqual(classify_point(ring, to_point((6, 4))), BOUNDARY)

    def test_reject_float_coordinates(self):
        with self.assertRaises(TypeError):
            to_point((0.5, 1))


if __name__ == "__main__":
    unittest.main()
