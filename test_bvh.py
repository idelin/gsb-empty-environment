"""Unittest suite for the dynamic BVH: exactness, degeneracies, dynamics."""

import random
import unittest
from fractions import Fraction

from bvh import AABB, DynamicBVH


class BruteForceIndex:
    """Naive O(n) reference implementation using the same exact predicates."""

    def __init__(self):
        self.items = {}

    def insert(self, obj, box):
        assert obj not in self.items
        self.items[obj] = box

    def remove(self, obj):
        del self.items[obj]

    def move(self, obj, box):
        self.items[obj] = box

    def query(self, box):
        return sorted(o for o, b in self.items.items() if b.intersects(box))

    def nearest(self, point, k=1):
        scored = [(b.distance_sq_to_point(point), o) for o, b in self.items.items()]
        scored.sort()
        return [(o, d) for d, o in scored[:k]]


class TestAABB(unittest.TestCase):
    def test_degenerate_box_allowed(self):
        p = AABB((1, 2), (1, 2))
        self.assertEqual(p.perimeter(), 0)
        self.assertEqual(p.distance_sq_to_point((4, 6)), 25)

    def test_invalid_box_rejected(self):
        with self.assertRaises(ValueError):
            AABB((1, 2), (1, 1))

    def test_closed_intersection_touching_counts(self):
        self.assertTrue(AABB((0, 0), (1, 1)).intersects(AABB((1, 1), (2, 2))))
        self.assertTrue(AABB((0, 0), (1, 1)).intersects(AABB((1, -5), (1, 5))))
        self.assertFalse(AABB((0, 0), (1, 1)).intersects(AABB((2, 2), (3, 3))))

    def test_exact_fraction_distance(self):
        box = AABB((Fraction(0),), (Fraction(1),))
        d = box.distance_sq_to_point((Fraction(4, 3),))
        self.assertEqual(d, Fraction(1, 9))  # no float epsilon involved

    def test_union_invariant(self):
        a = AABB((0, 0), (1, 1))
        b = AABB((-1, 2), (2, 3))
        u = a.union(b)
        self.assertEqual((u.lo, u.hi), ((-1, 0), (2, 3)))


class TestEmptyTree(unittest.TestCase):
    def setUp(self):
        self.tree = DynamicBVH()

    def test_query_empty_is_empty(self):
        self.assertEqual(self.tree.query(AABB((0, 0), (1, 1))), [])

    def test_nearest_empty_is_empty(self):
        self.assertEqual(self.tree.nearest((0, 0)), [])
        self.assertEqual(self.tree.nearest((0, 0), k=5), [])

    def test_nearest_nonpositive_k(self):
        self.assertEqual(self.tree.nearest((0, 0), k=0), [])
        self.assertEqual(self.tree.nearest((0, 0), k=-2), [])

    def test_remove_missing_raises(self):
        with self.assertRaises(KeyError):
            self.tree.remove(7)

    def test_move_missing_raises(self):
        with self.assertRaises(KeyError):
            self.tree.move(7, AABB((0,), (1,)))

    def test_invariants_empty(self):
        self.tree.check_invariants()


class TestBasicOps(unittest.TestCase):
    def test_insert_duplicate_rejected(self):
        tree = DynamicBVH()
        tree.insert(1, AABB((0,), (1,)))
        with self.assertRaises(ValueError):
            tree.insert(1, AABB((2,), (3,)))

    def test_dimension_mismatch_rejected(self):
        tree = DynamicBVH()
        tree.insert(1, AABB((0, 0), (1, 1)))
        with self.assertRaises(ValueError):
            tree.insert(2, AABB((0, 0, 0), (1, 1, 1)))
        with self.assertRaises(ValueError):
            tree.query(AABB((0,), (1,)))
        with self.assertRaises(ValueError):
            tree.nearest((0, 0, 0))

    def test_containment_and_touch(self):
        tree = DynamicBVH()
        tree.insert("a", AABB((0, 0), (10, 10)))
        tree.insert("b", AABB((10, 10), (20, 20)))
        tree.insert("c", AABB((11, 11), (12, 12)))
        self.assertEqual(tree.query(AABB((10, 10), (10, 10))), ["a", "b"])
        self.assertEqual(tree.query(AABB((11, 11), (11, 11))), ["b", "c"])

    def test_remove_updates_queries(self):
        tree = DynamicBVH()
        for i in range(8):
            tree.insert(i, AABB((i,), (i + 1,)))
        tree.remove(3)
        self.assertNotIn(3, tree)
        self.assertEqual(tree.query(AABB((3,), (3,))), [2])  # closed box (2,3) touches 3
        self.assertEqual(tree.query(AABB((2,), (3,))), [1, 2])  # (1,2) touches at 2
        self.assertEqual(len(tree), 7)
        tree.check_invariants()

    def test_remove_all_to_empty(self):
        tree = DynamicBVH()
        boxes = {i: AABB((i, i), (i, i)) for i in range(12)}
        for o, b in boxes.items():
            tree.insert(o, b)
        for o in boxes:
            tree.remove(o)
            tree.check_invariants()
        self.assertEqual(len(tree), 0)
        self.assertEqual(tree.query(AABB((0, 0), (100, 100))), [])
        self.assertEqual(tree.nearest((0, 0)), [])


class TestMove(unittest.TestCase):
    def test_old_region_unreachable(self):
        tree = DynamicBVH()
        tree.insert("car", AABB((0, 0), (1, 1)))
        tree.move("car", AABB((10, 10), (11, 11)))
        self.assertEqual(tree.query(AABB((0, 0), (1, 1))), [])
        self.assertEqual(tree.query(AABB((10, 10), (11, 11))), ["car"])
        self.assertEqual(tree.get("car"), AABB((10, 10), (11, 11)))
        tree.check_invariants()

    def test_move_preserves_others_and_nearest(self):
        tree = DynamicBVH()
        for i in range(20):
            tree.insert(i, AABB((i,), (i + 1,)))
        tree.move(5, AABB((100,), (101,)))
        tree.check_invariants()
        self.assertEqual(tree.query(AABB((5,), (5,))), [4])  # box 4 is (4,5), closed
        self.assertEqual(tree.nearest((5,), k=3), [(4, 0), (3, 1), (6, 1)])

    def test_move_missing_leaves_tree_intact(self):
        tree = DynamicBVH()
        tree.insert(1, AABB((0,), (1,)))
        with self.assertRaises(KeyError):
            tree.move(2, AABB((2,), (3,)))
        self.assertEqual(tree.query(AABB((0,), (1,))), [1])


class TestNearest(unittest.TestCase):
    def test_point_to_degenerate_boxes(self):
        tree = DynamicBVH()
        for i, x in enumerate((0, 3, 6)):
            tree.insert(i, AABB((x, 0), (x, 0)))
        self.assertEqual(tree.nearest((4, 0)), [(1, 1)])
        self.assertEqual(tree.nearest((-10, 0)), [(0, 100)])

    def test_ties_broken_by_id(self):
        tree = DynamicBVH()
        # three point boxes equidistant from the query point
        for i, (x, y) in enumerate(((0, 1), (2, 1), (1, 0))):
            tree.insert(i, AABB((x, y), (x, y)))
        hit = tree.nearest((1, 1))
        self.assertEqual(hit, [(0, 1)])
        top3 = tree.nearest((1, 1), k=3)
        self.assertEqual(top3, [(0, 1), (1, 1), (2, 1)])
        self.assertEqual([o for o, _ in top3], [0, 1, 2])

    def test_non_numeric_ids_sorted(self):
        tree = DynamicBVH()
        for name in ("charlie", "alpha", "bravo"):
            tree.insert(name, AABB((0,), (0,)))
        hits = tree.nearest((0,), k=3)
        self.assertEqual([o for o, _ in hits], ["alpha", "bravo", "charlie"])

    def test_k_larger_than_n(self):
        tree = DynamicBVH()
        tree.insert(1, AABB((0,), (0,)))
        self.assertEqual(tree.nearest((5,), k=10), [(1, 25)])

    def test_fraction_exact_ordering(self):
        tree = DynamicBVH()
        tree.insert(1, AABB((Fraction(1, 3),), (Fraction(1, 3),)))
        tree.insert(2, AABB((Fraction(2, 3),), (Fraction(2, 3),)))
        obj, dist = tree.nearest((Fraction(1, 2),))[0]
        self.assertEqual(obj, 1)
        self.assertEqual(dist, Fraction(1, 36))
        self.assertIsInstance(dist, Fraction)


class TestRandomizedComparison(unittest.TestCase):
    """Compare the tree against brute force under random dynamic workloads."""

    def _run(self, dim, rng, use_fractions, n_steps=400):
        tree = DynamicBVH()
        ref = BruteForceIndex()

        def make_box():
            vals = []
            for _ in range(dim):
                if use_fractions:
                    a = Fraction(rng.randint(-5, 5), rng.choice((1, 2, 3)))
                    b = Fraction(rng.randint(-5, 5), rng.choice((1, 2, 3)))
                else:
                    a, b = rng.randint(-5, 5), rng.randint(-5, 5)
                if a > b:
                    a, b = b, a
                vals.append((a, b))
            return AABB(tuple(a for a, _ in vals), tuple(b for _, b in vals))

        def make_point():
            if use_fractions:
                return tuple(Fraction(rng.randint(-8, 8), rng.choice((1, 2, 3))) for _ in range(dim))
            return tuple(rng.randint(-8, 8) for _ in range(dim))

        next_id = 0
        for step in range(n_steps):
            if not ref.items or rng.random() < 0.55:
                oid = next_id
                next_id += 1
                box = make_box()
                tree.insert(oid, box)
                ref.insert(oid, box)
            elif rng.random() < 0.7:
                oid = rng.choice(list(ref.items))
                box = make_box()
                tree.move(oid, box)
                ref.move(oid, box)
            else:
                oid = rng.choice(list(ref.items))
                tree.remove(oid)
                ref.remove(oid)

            tree.check_invariants()
            if step % 17 == 0:
                qb = make_box()
                self.assertEqual(tree.query(qb), ref.query(qb))
                p = make_point()
                for k in (1, 2, 5):
                    self.assertEqual(tree.nearest(p, k), ref.nearest(p, k))

        # final exhaustive sweep over a grid of query boxes and points
        for o in range(-6, 7, 2):
            qb = AABB((o,) * dim, (o + 1,) * dim)
            self.assertEqual(tree.query(qb), ref.query(qb))
            p = (o,) * dim
            for k in (1, 3):
                self.assertEqual(tree.nearest(p, k), ref.nearest(p, k))

    def test_2d_ints(self):
        self._run(2, random.Random(20260924), False)

    def test_3d_fractions(self):
        self._run(3, random.Random(4242), True)

    def test_1d_mixed_scenario(self):
        self._run(1, random.Random(7), True, n_steps=250)


if __name__ == "__main__":
    unittest.main(verbosity=2)
