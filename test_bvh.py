from fractions import Fraction
import random
import unittest

from bvh import BVHIndex, Bounds, make_bounds


class BVHIndexTest(unittest.TestCase):
    def test_empty_tree_queries_are_defined(self):
        tree = BVHIndex(dimension=2)
        tree.check_invariants()
        self.assertEqual(tree.intersects([(0, 0), (1, 1)]), [])
        self.assertIsNone(tree.nearest((0, 0)))
        self.assertEqual(tree.nearest_k((0, 0), 3), [])
        self.assertEqual(tree.ids(), [])

    def test_missing_duplicate_and_invalid_operations(self):
        tree = BVHIndex(dimension=2)
        tree.insert("a", [(0, 0), (0, 0)])

        with self.assertRaises(KeyError):
            tree.remove("missing")
        with self.assertRaises(KeyError):
            tree.move("missing", [(0, 0), (0, 0)])
        with self.assertRaises(KeyError):
            tree.insert("a", [(1, 1), (2, 2)])

        self.assertIsNone(tree.discard("missing"))
        self.assertEqual(
            tree.discard("a"),
            Bounds(
                ((Fraction(0), Fraction(0)), (Fraction(0), Fraction(0)))
            ),
        )
        tree.check_invariants()

    def test_invalid_exact_coordinates_and_shapes(self):
        tree = BVHIndex(dimension=2)
        with self.assertRaises(TypeError):
            tree.insert("x", [(0, 0.5), (0, 0)])
        with self.assertRaises(ValueError):
            tree.insert("x", [(1, 0), (0, 0)])
        with self.assertRaises(ValueError):
            tree.insert("x", [(0, 1)])
        with self.assertRaises(ValueError):
            tree.nearest((0,))
        with self.assertRaises(ValueError):
            tree.nearest_k((0, 0), 0)

    def test_degenerate_boxes_and_boundary_touching(self):
        tree = BVHIndex(dimension=2)
        tree.insert("point", [(0, 0), (0, 0)])
        tree.insert("vertical", [(1, 1), (0, 2)])
        tree.insert("horizontal", [(0, 2), (1, 1)])

        self.assertEqual(
            tree.intersects([(0, 0), (0, 0)]),
            ["point"],
        )
        self.assertEqual(
            tree.intersects([(1, 1), (1, 1)]),
            ["horizontal", "vertical"],
        )
        self.assertEqual(
            tree.intersects([(0, 1), (0, 1)]),
            ["horizontal", "point", "vertical"],
        )
        self.assertEqual(
            tree.intersects([(1, 1), (0, 1)]),
            ["horizontal", "vertical"],
        )
        tree.check_invariants()

    def test_rational_coordinates_without_float_epsilon(self):
        tree = BVHIndex(dimension=2)
        tree.insert(
            "left",
            [(Fraction(1, 3), Fraction(2, 3)), (Fraction(1, 7), Fraction(2, 7))],
        )
        tree.insert(
            "right",
            [(Fraction(2, 3), Fraction(1)), (Fraction(1, 7), Fraction(2, 7))],
        )

        self.assertEqual(
            tree.intersects([(Fraction(2, 3), Fraction(2, 3)), (0, 1)]),
            ["left", "right"],
        )
        self.assertEqual(
            tree.intersects(
                [(Fraction(2, 3) + Fraction(1, 10**30), 1), (0, 1)]
            ),
            ["right"],
        )

        result = tree.nearest_with_distance((Fraction(2, 3), Fraction(1, 7)))
        self.assertEqual(result, ("left", Fraction(0)))

    def test_nearest_tie_is_sorted_by_id(self):
        tree = BVHIndex(dimension=1)
        for item, center in [(3, 0), (1, 2), (2, -2)]:
            tree.insert(item, [(center, center)])

        self.assertEqual(tree.nearest((1,)), 1)
        self.assertEqual(
            tree.nearest_k((1,), 3),
            [(1, Fraction(1)), (3, Fraction(1)), (2, Fraction(9))],
        )
        self.assertEqual(tree.nearest((100,)), 1)

    def test_move_removes_old_region_and_keeps_invariants(self):
        tree = BVHIndex(dimension=2)
        tree.insert("moved", [(0, 1), (0, 1)])
        tree.insert("stationary", [(5, 6), (5, 6)])

        tree.move("moved", [(10, 11), (10, 11)])
        self.assertEqual(tree.intersects([(0, 2), (0, 2)]), [])
        self.assertEqual(tree.intersects([(10, 12), (10, 12)]), ["moved"])
        self.assertEqual(tree.nearest((0, 0)), "stationary")
        self.assertEqual(tree.nearest((11, 11)), "moved")

        tree.move("moved", [(10, 11), (10, 11)])
        self.assertEqual(tree.bounds("moved"), make_bounds([(10, 11), (10, 11)], 2))
        tree.check_invariants()

    def test_move_during_rebuild_does_not_reattach_old_leaf(self):
        tree = BVHIndex(dimension=2)
        for item, x in enumerate([0, 2, 4, 6]):
            tree.insert(item, [(x, x + 1), (0, 1)])
        tree._rebalance_if_tall = lambda: tree._rebuild()

        tree.move(0, [(20, 21), (20, 21)])

        tree.check_invariants()
        self.assertEqual(len(tree), 4)
        self.assertEqual(tree.intersects([(0, 2), (0, 2)]), [1])
        self.assertEqual(tree.intersects([(20, 22), (20, 22)]), [0])

    def test_dynamic_insert_remove_sequence_against_brute_force(self):
        random.seed(7319)
        tree = BVHIndex(dimension=3)
        reference = {}

        for item in range(180):
            low = [random.randint(-10, 10) for _ in range(3)]
            high = [value + random.randint(0, 4) for value in low]
            box = list(zip(low, high))
            tree.insert(item, box)
            reference[item] = box
            if item % 4 == 0:
                tree.check_invariants()

        query = [(2, 7), (-1, 3), (4, 9)]
        expected = [
            item
            for item, box in reference.items()
            if all(box[axis][0] <= query[axis][1] and query[axis][0] <= box[axis][1]
                   for axis in range(3))
        ]
        self.assertEqual(tree.intersects(query), expected)

        for item in range(0, 180, 3):
            del reference[item]
            tree.remove(item)
        tree.check_invariants()

        point = (4, 1, 8)
        expected_nearest = sorted(
            (
                (
                    item,
                    sum(
                        (
                            Fraction(0)
                            if low <= coordinate <= high
                            else min(abs(low - coordinate), abs(high - coordinate))
                        )
                        ** 2
                        for coordinate, (low, high) in zip(point, box)
                    ),
                )
                for item, box in reference.items()
            ),
            key=lambda row: (row[1], row[0]),
        )[:10]
        actual = tree.nearest_k(point, 10)
        self.assertEqual(actual, expected_nearest)

    def test_rebuild_forces_logarithmic_height(self):
        tree = BVHIndex(dimension=2)
        count = 250
        for item in range(count):
            tree.insert(item, [(item, item), (0, 0)])
        tree._rebuild()
        self.assertLessEqual(tree._root.height, count.bit_length())
        self.assertEqual(sorted(tree._leaves), list(range(count)))
        for item in range(0, count, 7):
            self.assertEqual(tree.nearest((item, 0)), item)
        tree.check_invariants()


if __name__ == "__main__":
    unittest.main()
