"""Unit tests for affine2d: exact arithmetic, caching, reparenting,
snapshots, and randomized differential testing against naive composition."""

import random
import unittest
from fractions import Fraction

from affine2d import (
    HitResult,
    Matrix,
    Scene,
    SingularTransformError,
)

IDENTITY = Matrix.identity()


def naive_world(node):
    """Reference: compose locals from the root down, no caching."""
    chain = []
    cur = node
    while cur is not None:
        chain.append(cur.local)
        cur = cur.parent
    m = IDENTITY
    for local in reversed(chain):
        m = m @ local
    return m


def naive_hit(node, world_point):
    inv = naive_world(node).inverse()
    if inv is None:
        return None
    return inv.apply(world_point)


class MatrixTests(unittest.TestCase):
    def test_inverse_exact_rationals(self):
        rng = random.Random(7)
        for _ in range(200):
            entries = [Fraction(rng.randint(-5, 5), rng.randint(1, 4))
                       for _ in range(6)]
            m = Matrix(*entries)
            inv = m.inverse()
            if m.determinant() == 0:
                self.assertIsNone(inv)
            else:
                self.assertIsNotNone(inv)
                self.assertEqual(m @ inv, IDENTITY)
                self.assertEqual(inv @ m, IDENTITY)

    def test_singular_has_no_inverse(self):
        for m in (Matrix.scale(0), Matrix.scale(1, 0), Matrix.scale(0, 3),
                  Matrix(1, 1, 2, 2, 5, 6)):  # rank-1 rows
            self.assertTrue(m.is_singular())
            self.assertIsNone(m.inverse())

    def test_scale_zero_not_pseudo_inverted(self):
        # scale(0) collapses the plane; no exact inverse may be fabricated.
        m = Matrix.scale(0)
        self.assertIsNone(m.inverse())
        scene = Scene()
        n = scene.create_node("flat", local=m)
        res = scene.hit(n, (3, 4))
        self.assertFalse(res.ok)
        self.assertIsNone(res.point)
        self.assertIn("singular", res.reason)

    def test_translate_then_rotate_differs(self):
        t = Matrix.translation(10, 0)
        r = Matrix.rotation_quarter(1)
        self.assertNotEqual(t @ r, r @ t)
        p = (1, 0)
        self.assertNotEqual((t @ r).apply(p), (r @ t).apply(p))
        # exact expected values, no epsilon
        # t @ r applies r first: (1,0) -> rot90 -> (0,1) -> +10x -> (10,1)
        self.assertEqual((t @ r).apply(p), (Fraction(10), Fraction(1)))
        # r @ t applies t first: (1,0) -> +10x -> (11,0) -> rot90 -> (0,11)
        self.assertEqual((r @ t).apply(p), (Fraction(0), Fraction(11)))

    def test_exact_rational_points(self):
        m = Matrix(Fraction(2, 3), 0, 0, Fraction(1, 5), 7, -3)
        inv = m.inverse()
        p = (Fraction(11, 7), Fraction(-2, 9))
        self.assertEqual(inv.apply(m.apply(p)), p)


class HierarchyTests(unittest.TestCase):
    def build(self):
        scene = Scene()
        a = scene.create_node("a", local=Matrix.translation(1, 2))
        b = scene.create_node("b", parent=a, local=Matrix.rotation_quarter(1))
        c = scene.create_node("c", parent=a, local=Matrix.scale(2))
        d = scene.create_node("d", parent=b, local=Matrix.translation(0, 5))
        return scene, a, b, c, d

    def test_world_matches_naive(self):
        scene, a, b, c, d = self.build()
        for n in (a, b, c, d):
            self.assertEqual(scene.world_matrix(n), naive_world(n))

    def test_cache_depends_on_ancestors_not_own_version(self):
        scene, a, b, c, d = self.build()
        for n in (a, b, c, d):
            scene.world_matrix(n)
        self.assertTrue(all(scene.is_cache_valid(n) for n in (a, b, c, d)))

        scene.set_local(a, Matrix.translation(9, 9))  # touch the ancestor
        # every node is a (transitive) child of a, so all are invalidated:
        # fingerprints track the whole ancestor path, not the own version.
        self.assertFalse(scene.is_cache_valid(a))
        self.assertFalse(scene.is_cache_valid(b))
        self.assertFalse(scene.is_cache_valid(c))
        self.assertFalse(scene.is_cache_valid(d))

        # local invalidation scope: touching b must not invalidate c.
        for n in (a, b, c, d):
            scene.world_matrix(n)
        scene.set_local(b, Matrix.scale(3))
        self.assertFalse(scene.is_cache_valid(b))
        self.assertFalse(scene.is_cache_valid(d))
        self.assertTrue(scene.is_cache_valid(a))
        self.assertTrue(scene.is_cache_valid(c))

    def test_parent_change_updates_child_hit(self):
        scene, a, b, c, d = self.build()
        world_point = (10, 20)
        before = scene.hit(d, world_point)
        self.assertTrue(before.ok)
        scene.set_local(a, Matrix.translation(100, 0))
        after = scene.hit(d, world_point)
        self.assertTrue(after.ok)
        self.assertNotEqual(before.point, after.point)
        # round-trip: local -> world reproduces the query point exactly
        self.assertEqual(scene.world_matrix(d).apply(after.point),
                         (Fraction(10), Fraction(20)))

    def test_reparent_plain_and_keep_world(self):
        scene, a, b, c, d = self.build()
        world_before = scene.world_matrix(d)
        scene.reparent(b, c, keep_world=True)
        self.assertIs(b.parent, c)
        self.assertEqual(scene.world_matrix(d), world_before)
        self.assertEqual(scene.world_matrix(b) @ b.local.inverse()
                         if False else scene.world_matrix(d), naive_world(d))

        # plain reparent: local kept, world changes
        local_b = b.local
        scene.reparent(b, a, keep_world=False)
        self.assertEqual(b.local, local_b)
        self.assertEqual(scene.world_matrix(b), naive_world(b))

    def test_reparent_to_singular_parent_rejected_atomically(self):
        scene, a, b, c, d = self.build()
        flat = scene.create_node("flat", parent=a, local=Matrix.scale(0))
        old_parent, old_local, old_version = b.parent, b.local, b.version
        with self.assertRaises(SingularTransformError):
            scene.reparent(b, flat, keep_world=True)
        # atomic: nothing changed
        self.assertIs(b.parent, old_parent)
        self.assertEqual(b.local, old_local)
        self.assertEqual(b.version, old_version)
        self.assertEqual(scene.world_matrix(b), naive_world(b))
        # plain reparent under a singular parent is allowed; the hit query
        # is what reports the undefined inverse.
        scene.reparent(b, flat, keep_world=False)
        res = scene.hit(b, (1, 1))
        self.assertFalse(res.ok)

    def test_reparent_cycle_rejected(self):
        scene, a, b, c, d = self.build()
        with self.assertRaises(ValueError):
            scene.reparent(a, d)
        with self.assertRaises(ValueError):
            scene.reparent(a, a)
        self.assertIs(a.parent, None)

    def test_hit_singular_world_reports_status(self):
        scene = Scene()
        n = scene.create_node("n", local=Matrix.scale(2, 0))
        res = scene.hit(n, (0, 0))
        self.assertFalse(res.ok)
        self.assertIsNone(res.point)
        self.assertIsInstance(res, HitResult)


class SnapshotTests(unittest.TestCase):
    def test_old_version_hit_query(self):
        scene = Scene()
        a = scene.create_node("a", local=Matrix.translation(5, 0))
        b = scene.create_node("b", parent=a, local=Matrix.scale(2))
        p = (Fraction(9), Fraction(4))

        snap1 = scene.snapshot()
        original = scene.hit(b, p)
        scene.set_local(a, Matrix.translation(100, 100))
        scene.set_local(b, Matrix.rotation_quarter(1))
        snap2 = scene.snapshot()

        # old snapshot still answers for the old version
        self.assertEqual(snap1.hit(b, p), original)
        self.assertEqual(snap1.world_matrix(b),
                         Matrix.translation(5, 0) @ Matrix.scale(2))
        # new snapshot and live scene agree with each other, not with snap1
        self.assertEqual(snap2.hit(b, p), scene.hit(b, p))
        self.assertNotEqual(snap1.hit(b, p).point, scene.hit(b, p).point)

    def test_multi_view_snapshots_independent(self):
        scene = Scene()
        a = scene.create_node("a", local=Matrix.translation(1, 0))
        views = []
        for i in range(5):
            views.append(scene.snapshot())
            scene.set_local(a, Matrix.translation(i + 2, 0))
        p = (Fraction(0), Fraction(0))
        for i, view in enumerate(views):
            self.assertEqual(view.hit(a, p).point, (Fraction(-(i + 1)), Fraction(0)))
        self.assertEqual(scene.hit(a, p).point, (Fraction(-6), Fraction(0)))

    def test_snapshot_covers_reparent(self):
        scene = Scene()
        a = scene.create_node("a", local=Matrix.translation(3, 0))
        b = scene.create_node("b", local=Matrix.translation(0, 7))
        c = scene.create_node("c", parent=a, local=Matrix.scale(1))
        snap = scene.snapshot()
        scene.reparent(c, b)
        self.assertEqual(snap.world_matrix(c),
                         Matrix.translation(3, 0))
        self.assertEqual(scene.world_matrix(c),
                         Matrix.translation(0, 7))


class FuzzTests(unittest.TestCase):
    def test_random_ops_match_naive(self):
        rng = random.Random(20260925)
        scene = Scene()
        nodes = [scene.create_node(f"n{i}") for i in range(12)]

        def random_matrix():
            if rng.random() < 0.15:
                # deliberately singular: zero scale on one or both axes
                return Matrix.scale(rng.choice([0, 0, 1, 2]),
                                    rng.choice([0, 1, 3]))
            kind = rng.randrange(4)
            if kind == 0:
                return Matrix.translation(rng.randint(-9, 9), rng.randint(-9, 9))
            if kind == 1:
                return Matrix.scale(rng.randint(-3, 3) or 1,
                                    rng.randint(1, 4))
            if kind == 2:
                return Matrix.rotation_quarter(rng.randrange(4))
            return Matrix(Fraction(rng.randint(-3, 3), rng.randint(1, 3)),
                          0, 0,
                          Fraction(rng.randint(-3, 3), rng.randint(1, 3)),
                          rng.randint(-5, 5), rng.randint(-5, 5))

        for step in range(400):
            op = rng.random()
            if op < 0.45:
                n = rng.choice(nodes)
                scene.set_local(n, random_matrix())
            elif op < 0.75:
                n = rng.choice(nodes)
                candidates = [m for m in nodes
                              if m is not n and not n.is_ancestor_of(m)]
                target = rng.choice(candidates + [None])
                keep = rng.random() < 0.5
                try:
                    scene.reparent(n, target, keep_world=keep)
                except SingularTransformError:
                    pass  # singular keep-world target: rejection is the spec
            else:
                pass  # query-only step

            # differential check: every node, against root-down composition
            for n in nodes:
                self.assertEqual(scene.world_matrix(n), naive_world(n),
                                 f"step {step}, node {n.name}")
                wp = (Fraction(rng.randint(-20, 20)), Fraction(rng.randint(-20, 20)))
                got = scene.hit(n, wp)
                expected = naive_hit(n, wp)
                if expected is None:
                    self.assertFalse(got.ok, f"step {step}, node {n.name}")
                else:
                    self.assertTrue(got.ok, f"step {step}, node {n.name}")
                    self.assertEqual(got.point, expected,
                                     f"step {step}, node {n.name}")

    def test_random_cache_invalidation_scope(self):
        rng = random.Random(99)
        scene = Scene()
        nodes = [scene.create_node(f"n{i}") for i in range(10)]
        for n in nodes[1:]:
            scene.reparent(n, rng.choice(nodes[:nodes.index(n)]))
        for n in nodes:
            scene.world_matrix(n)

        victim = rng.choice(nodes)
        scene.set_local(victim, Matrix.translation(1, 1))
        affected = {victim}
        frontier = [victim]
        while frontier:
            cur = frontier.pop()
            for child in cur.children:
                affected.add(child)
                frontier.append(child)
        for n in nodes:
            self.assertEqual(scene.is_cache_valid(n), n not in affected,
                             f"cache scope wrong for {n.name}")


if __name__ == "__main__":
    unittest.main()
