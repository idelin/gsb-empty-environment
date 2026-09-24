import unittest
from fractions import Fraction

from point_location import Location, Polygon, PolygonSet, RingNode


class PointLocationTest(unittest.TestCase):
    def test_outer_hole_and_island(self):
        polygon = PolygonSet(
            [
                RingNode(
                    [(0, 0), (10, 0), (10, 10), (0, 10)],
                    [
                        RingNode(
                            [(3, 3), (7, 3), (7, 7), (3, 7)],
                            [RingNode([(4, 4), (6, 4), (6, 6), (4, 6)])],
                        )
                    ],
                )
            ]
        )

        self.assertIs(polygon.locate((1, 1)), Location.INSIDE)
        self.assertIs(polygon.locate((5, 5)), Location.INSIDE)
        self.assertIs(polygon.locate((Fraction(7, 2), 5)), Location.OUTSIDE)
        self.assertIs(polygon.locate((-1, 5)), Location.OUTSIDE)
        self.assertIs(polygon.locate((0, 5)), Location.BOUNDARY)
        self.assertIs(polygon.locate((7, 5)), Location.BOUNDARY)
        self.assertIs(polygon.locate((4, 4)), Location.BOUNDARY)

    def test_rational_coordinates_and_slanted_edge(self):
        polygon = Polygon([(0, 0), (4, 0), (0, 4)])
        point = (Fraction(1, 2), Fraction(7, 2))

        self.assertIs(polygon.locate(point), Location.BOUNDARY)
        self.assertIs(polygon.locate((Fraction(1, 3), Fraction(1, 3))), Location.INSIDE)
        self.assertIs(polygon.locate((Fraction(5, 2), Fraction(5, 2))), Location.OUTSIDE)

    def test_ray_through_vertex_is_counted_once(self):
        diamond = Polygon([(0, 1), (1, 0), (2, 1), (1, 2)])

        self.assertIs(diamond.locate((0, 1)), Location.BOUNDARY)
        self.assertIs(diamond.locate((Fraction(1, 2), 1)), Location.INSIDE)
        self.assertIs(diamond.locate((1, 1)), Location.INSIDE)
        self.assertIs(diamond.locate((Fraction(3, 2), 1)), Location.INSIDE)
        self.assertIs(diamond.locate((3, 1)), Location.OUTSIDE)

    def test_ray_through_local_vertex_extremum(self):
        notched = Polygon([(0, 0), (1, 0), (2, 1), (3, 0), (4, 0), (4, 4), (0, 4)])

        self.assertIs(notched.locate((Fraction(1, 2), 1)), Location.INSIDE)
        self.assertIs(notched.locate((Fraction(7, 2), 1)), Location.INSIDE)
        self.assertIs(notched.locate((2, 1)), Location.BOUNDARY)
        self.assertIs(notched.locate((2, 0)), Location.OUTSIDE)

    def test_edges_left_of_query_do_not_cross_rightward_ray(self):
        polygon = PolygonSet(
            [
                RingNode([(0, 0), (1, 0), (1, 4), (0, 4)]),
                RingNode([(3, 0), (4, 0), (4, 4), (3, 4)]),
            ],
            node_capacity=2,
        )

        self.assertIs(polygon.locate((1, 2)), Location.BOUNDARY)
        self.assertIs(polygon.locate((2, 2)), Location.OUTSIDE)
        self.assertIs(polygon.locate((3, 2)), Location.BOUNDARY)

    def test_repeated_closing_endpoint_and_consecutive_points(self):
        with_close = Polygon([(0, 0), (5, 0), (5, 5), (0, 5), (0, 0)])
        with_repeats = Polygon([(0, 0), (0, 0), (5, 0), (5, 5), (5, 5), (0, 5), (0, 0)])
        queries = [((1, 1), Location.INSIDE), ((5, 2), Location.BOUNDARY), ((6, 2), Location.OUTSIDE)]

        for point, expected in queries:
            with self.subTest(point=point):
                self.assertIs(with_close.locate(point), expected)
                self.assertIs(with_repeats.locate(point), expected)

    def test_collinear_vertex_on_straight_edge(self):
        polygon = Polygon([(0, 0), (2, 0), (4, 0), (4, 4), (0, 4)])

        self.assertIs(polygon.locate((2, 0)), Location.BOUNDARY)
        self.assertIs(polygon.locate((2, 2)), Location.INSIDE)

    def test_orientation_is_ignored_for_outer_and_hole(self):
        clockwise_outer = Polygon(
            [(0, 0), (0, 8), (8, 8), (8, 0)],
            [[(2, 2), (2, 6), (6, 6), (6, 2)]],
        )

        self.assertIs(clockwise_outer.locate((1, 1)), Location.INSIDE)
        self.assertIs(clockwise_outer.locate((4, 4)), Location.OUTSIDE)

    def test_batch_query_uses_one_structure(self):
        polygon = Polygon([(0, 0), (10, 0), (10, 10), (0, 10)], node_capacity=2)
        points = [(1, 1), (10, 5), (11, 5)]

        self.assertEqual(
            polygon.locate_all(points),
            [Location.INSIDE, Location.BOUNDARY, Location.OUTSIDE],
        )

    def test_multiple_disjoint_outer_rings(self):
        polygons = PolygonSet(
            [
                RingNode([(0, 0), (1, 0), (1, 1), (0, 1)]),
                RingNode([(3, 3), (4, 3), (4, 4), (3, 4)]),
            ]
        )

        self.assertIs(polygons.locate((0, 0)), Location.BOUNDARY)
        self.assertIs(polygons.locate((Fraction(7, 2), Fraction(7, 2))), Location.INSIDE)
        self.assertIs(polygons.locate((2, 2)), Location.OUTSIDE)

    def test_empty_polygon_set(self):
        empty = PolygonSet([])

        self.assertIs(empty.locate((0, 0)), Location.OUTSIDE)
        self.assertEqual(empty.locate_all([(1, 1), (2, 2)]), [Location.OUTSIDE, Location.OUTSIDE])

    def test_invalid_self_intersecting_ring(self):
        with self.assertRaisesRegex(ValueError, "self-intersecting"):
            Polygon([(0, 0), (3, 0), (3, 3), (1, -1), (0, 3)])

    def test_invalid_zero_area_ring(self):
        with self.assertRaisesRegex(ValueError, "zero area"):
            Polygon([(0, 0), (1, 1), (2, 2)])

    def test_hierarchy_must_match_geometry(self):
        with self.assertRaisesRegex(ValueError, "explicit hierarchy"):
            PolygonSet(
                [
                    RingNode(
                        [(0, 0), (4, 0), (4, 4), (0, 4)],
                        [RingNode([(5, 5), (6, 5), (6, 6)])],
                    )
                ]
            )

    def test_touching_rings_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "touch or cross"):
            PolygonSet(
                [
                    RingNode([(0, 0), (2, 0), (2, 2), (0, 2)]),
                    RingNode([(2, 1), (3, 1), (3, 3), (2, 3)]),
                ]
            )

    def test_floating_point_coordinates_rejected(self):
        with self.assertRaises(TypeError):
            Polygon([(0, 0), (1.5, 0), (1.5, 1.5), (0, 1.5)])


if __name__ == "__main__":
    unittest.main()
