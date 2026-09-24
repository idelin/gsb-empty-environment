import unittest
from fractions import Fraction
import random

from rectilinear_geometry import Rectangle, analyze_rectangles


class RectilinearGeometryTest(unittest.TestCase):
    def test_empty_input(self):
        result = analyze_rectangles([])
        self.assertEqual(result.area, 0)
        self.assertEqual(result.perimeter, 0)
        self.assertEqual(result.outer_perimeter, 0)
        self.assertEqual(result.outer_rings, ())
        self.assertEqual(result.hole_rings, ())

    def test_side_adjacent_unit_squares(self):
        result = analyze_rectangles([(0, 0, 1, 1), (1, 0, 2, 1)])
        self.assertEqual(result.area, 2)
        self.assertEqual(result.perimeter, 6)
        self.assertEqual(result.outer_perimeter, 6)
        self.assertEqual(
            result.outer_rings,
            (
                (
                    (Fraction(0), Fraction(0)),
                    (Fraction(2), Fraction(0)),
                    (Fraction(2), Fraction(1)),
                    (Fraction(0), Fraction(1)),
                ),
            ),
        )
        self.assertEqual(result.hole_rings, ())

    def test_corner_contact_keeps_two_rings(self):
        result = analyze_rectangles([(0, 0, 1, 1), (1, 1, 2, 2)])
        self.assertEqual(result.area, 2)
        self.assertEqual(result.perimeter, 8)
        self.assertEqual(result.outer_perimeter, 8)
        self.assertEqual(len(result.outer_rings), 2)
        self.assertEqual(result.hole_rings, ())
        for ring in result.outer_rings:
            points = set(ring)
            self.assertEqual(len(points), len(ring))

    def test_overlap_area_and_perimeter(self):
        result = analyze_rectangles([(0, 0, 2, 1), (1, 0, 3, 1)])
        self.assertEqual(result.area, 3)
        self.assertEqual(result.perimeter, 8)
        self.assertEqual(result.outer_perimeter, 8)
        self.assertEqual(len(result.outer_rings), 1)

    def test_containment_does_not_cut_a_hole(self):
        result = analyze_rectangles([(0, 0, 3, 3), (1, 1, 2, 2)])
        self.assertEqual(result.area, 9)
        self.assertEqual(result.perimeter, 12)
        self.assertEqual(result.outer_perimeter, 12)
        self.assertEqual(len(result.outer_rings), 1)
        self.assertEqual(result.hole_rings, ())

    def test_four_bars_form_a_hole(self):
        result = analyze_rectangles(
            [
                (0, 0, 3, 1),
                (0, 2, 3, 3),
                (0, 1, 1, 2),
                (2, 1, 3, 2),
            ]
        )
        self.assertEqual(result.area, 8)
        self.assertEqual(result.perimeter, 16)
        self.assertEqual(result.outer_perimeter, 12)
        self.assertEqual(len(result.outer_rings), 1)
        self.assertEqual(
            result.hole_rings,
            (
                (
                    (Fraction(1), Fraction(1)),
                    (Fraction(1), Fraction(2)),
                    (Fraction(2), Fraction(2)),
                    (Fraction(2), Fraction(1)),
                ),
            ),
        )

    def test_edge_contact_with_inner_boundary_touch(self):
        result = analyze_rectangles(
            [
                (0, 0, 4, 4),
                (1, 0, 3, 3),
            ]
        )
        self.assertEqual(result.area, 16)
        self.assertEqual(result.perimeter, 16)
        self.assertEqual(result.outer_perimeter, 16)
        self.assertEqual(len(result.outer_rings), 1)
        self.assertEqual(result.hole_rings, ())

    def test_t_junction_does_not_split_straight_outer_edge(self):
        result = analyze_rectangles([(0, 0, 3, 1), (1, 0, 2, 2)])
        self.assertEqual(result.area, 4)
        self.assertEqual(result.perimeter, 10)
        self.assertEqual(
            result.outer_rings[0],
            (
                (Fraction(0), Fraction(0)),
                (Fraction(3), Fraction(0)),
                (Fraction(3), Fraction(1)),
                (Fraction(2), Fraction(1)),
                (Fraction(2), Fraction(2)),
                (Fraction(1), Fraction(2)),
                (Fraction(1), Fraction(1)),
                (Fraction(0), Fraction(1)),
            ),
        )

    def test_rational_coordinates_are_exact(self):
        result = analyze_rectangles(
            [
                (0, 0, Fraction(1, 2), Fraction(2, 3)),
                (Fraction(1, 2), 0, 1, Fraction(2, 3)),
            ]
        )
        self.assertEqual(result.area, Fraction(2, 3))
        self.assertEqual(result.perimeter, Fraction(10, 3))
        self.assertEqual(result.outer_perimeter, Fraction(10, 3))

    def test_zero_area_rectangles_are_ignored(self):
        empty = analyze_rectangles([(0, 0, 0, 1), (0, 0, 1, 0), (2, 2, 2, 2)])
        self.assertEqual(empty.area, 0)
        self.assertEqual(empty.perimeter, 0)
        self.assertEqual(empty.outer_rings, ())

        with_solid = analyze_rectangles([(0, 0, 1, 1), (1, 0, 1, 1)])
        self.assertEqual(with_solid.area, 1)
        self.assertEqual(with_solid.perimeter, 4)
        self.assertEqual(len(with_solid.outer_rings), 1)

    def test_normalized_and_duplicate_rectangles(self):
        first = analyze_rectangles([(1, 1, 0, 0)])
        second = analyze_rectangles([(0, 0, 1, 1)])
        self.assertEqual(first, second)

        duplicated = analyze_rectangles([(0, 0, 1, 1), (0, 0, 1, 1)])
        self.assertEqual(duplicated.area, 1)
        self.assertEqual(duplicated.perimeter, 4)

    def test_ring_orientation(self):
        result = analyze_rectangles(
            [
                (0, 0, 3, 1),
                (0, 2, 3, 3),
                (0, 1, 1, 2),
                (2, 1, 3, 2),
            ]
        )
        outer = result.outer_rings[0]
        hole = result.hole_rings[0]

        def signed_area(ring):
            total = 0
            for index, (x, y) in enumerate(ring):
                nx, ny = ring[(index + 1) % len(ring)]
                total += x * ny - y * nx
            return Fraction(total, 2)

        self.assertGreater(signed_area(outer), 0)
        self.assertLess(signed_area(hole), 0)

    def test_rectangle_rejects_float(self):
        with self.assertRaises(TypeError):
            Rectangle(0, 0, 1.0, 1)

    def test_rectangle_instances_are_accepted(self):
        result = analyze_rectangles([Rectangle(0, 0, 1, 1), Rectangle(1, 0, 2, 1)])
        self.assertEqual(result.area, 2)
        self.assertEqual(result.perimeter, 6)

    def test_random_collections_have_consistent_boundary_invariants(self):
        rng = random.Random(20260924)
        for _ in range(100):
            values = []
            for _ in range(rng.randrange(1, 8)):
                x0 = rng.randrange(0, 4)
                y0 = rng.randrange(0, 4)
                values.append(
                    (
                        x0,
                        y0,
                        x0 + rng.randrange(0, 3),
                        y0 + rng.randrange(0, 3),
                    )
                )

            result = analyze_rectangles(values)
            signed_area = Fraction(0)
            ring_perimeter = Fraction(0)

            for ring in result.outer_rings + result.hole_rings:
                self.assertEqual(len(ring), len(set(ring)))
                signed_area += self._signed_area(ring)
                for index, point in enumerate(ring):
                    following = ring[(index + 1) % len(ring)]
                    self.assertTrue(point[0] == following[0] or point[1] == following[1])
                    ring_perimeter += abs(following[0] - point[0]) + abs(following[1] - point[1])

            self.assertEqual(signed_area, result.area)
            self.assertEqual(ring_perimeter, result.perimeter)
            self.assertTrue(all(self._signed_area(ring) > 0 for ring in result.outer_rings))
            self.assertTrue(all(self._signed_area(ring) < 0 for ring in result.hole_rings))

    @staticmethod
    def _signed_area(ring):
        total = Fraction(0)
        for index, (x, y) in enumerate(ring):
            nx, ny = ring[(index + 1) % len(ring)]
            total += x * ny - y * nx
        return total / 2


if __name__ == "__main__":
    unittest.main()
