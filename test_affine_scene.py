"""Unit tests for the exact 2D affine scene graph.

Run with:  python3.11 -m unittest test_affine_scene -v
"""

import random
import unittest
from fractions import Fraction

from affine2d import (
    IDENTITY, F, quarter_turn, rotation, scaling, shear, translation,
)
from scene import (
    CycleError, HitStatus, Scene, UnknownVersionError,
    UnsolvableReparentError,
)


def rand_frac(rng, lo=-4, hi=4):
    return Fraction(rng.randint(lo, hi), rng.choice([1, 1, 1, 2, 3, 4]))


def random_mat(rng):
    m = IDENTITY
    for _ in range(rng.randint(1, 3)):
        kind = rng.random()
        if kind < 0.30:
            t = translation(rand_frac(rng), rand_frac(rng))
        elif kind < 0.55:
            sx = rand_frac(rng)
            if rng.random() < 0.15:
                sx = Fraction(0)  # exercise degenerate scales
            t = scaling(sx, rand_frac(rng))
        elif kind < 0.75:
            t = quarter_turn(rng.randrange(4))
        elif kind < 0.90:
            t = rotation(Fraction(3, 5), Fraction(4, 5))
        else:
            t = shear(rand_frac(rng, -2, 2))
        m = t @ m
    return m


class TestMatrixCore(unittest.TestCase):
    def test_translate_then_rotate_differs_from_reverse(self):
        translate_then_rotate = translation(1, 0) @ quarter_turn(1)
        rotate_then_translate = quarter_turn(1) @ translation(1, 0)
        self.assertNotEqual(translate_then_rotate, rotate_then_translate)
        self.assertEqual(translate_then_rotate.apply(0, 0), (F(1), F(0)))
        self.assertEqual(rotate_then_translate.apply(0, 0), (F(0), F(1)))

    def test_exact_rational_inverse_roundtrip(self):
        m = translation(Fraction(7, 3), -2) @ rotation(Fraction(3, 5), Fraction(4, 5)) @ scaling(Fraction(2, 3))
        inv = m.inverse()
        self.assertIsNotNone(inv)
        p = (Fraction(-5, 7), Fraction(11, 13))
        self.assertEqual(inv.apply(*m.apply(*p)), p)
        self.assertEqual(m @ inv, IDENTITY)
        self.assertEqual(inv @ m, IDENTITY)

    def test_zero_scale_is_singular_and_has_no_inverse(self):
        m = scaling(0, 1)
        self.assertTrue(m.is_singular)
        self.assertEqual(m.det, 0)
        # No pseudo-inverse is offered: exact inverse simply does not exist.
        self.assertIsNone(m.inverse())

    def test_rotation_requires_exact_unit_pair(self):
        with self.assertRaises(ValueError):
            rotation(Fraction(1, 2), Fraction(1, 2))

    def test_float_coordinates_rejected(self):
        with self.assertRaises(TypeError):
            translation(0.5, 0)


class TestSceneBehaviour(unittest.TestCase):
    def make_scene(self):
        scene = Scene()
        root = scene.create_node("root", local=translation(10, 0))
        arm = scene.create_node("arm", parent=root,
                                local=quarter_turn(1) @ translation(1, 0))
        tip = scene.create_node("tip", parent=arm,
                                local=scaling(2) @ translation(0, 3))
        return scene, root, arm, tip

    def test_parent_change_updates_child_hit(self):
        scene, root, arm, tip = self.make_scene()
        local_pt = (F(1), F(1))
        world_pt = scene.local_to_world(tip, *local_pt)
        hit = scene.hit(tip, *world_pt)
        self.assertTrue(hit.ok)
        self.assertEqual(hit.point, local_pt)

        # Move the root; the same world point must now map elsewhere.
        scene.set_local(root, translation(100, 0))
        hit_after = scene.hit(tip, *world_pt)
        self.assertTrue(hit_after.ok)
        self.assertNotEqual(hit_after.point, local_pt)
        # And the local point now lives at a shifted world position.
        self.assertEqual(
            scene.local_to_world(tip, *local_pt),
            (world_pt[0] + 90, world_pt[1]),
        )

    def test_singular_world_returns_not_invertible(self):
        scene = Scene()
        flat = scene.create_node("flat", local=scaling(0, 3))
        result = scene.hit(flat, 5, 6)
        self.assertEqual(result.status, HitStatus.NOT_INVERTIBLE)
        self.assertIsNone(result.point)
        self.assertFalse(result.ok)

    def test_reparent_keep_world_preserves_pose(self):
        scene, root, arm, tip = self.make_scene()
        world_before = scene.world_matrix(tip)
        other = scene.create_node("other", local=translation(-4, 7) @ rotation(Fraction(3, 5), Fraction(4, 5)))
        scene.reparent(tip, other, keep_world=True)
        self.assertIs(tip.parent, other)
        self.assertEqual(scene.world_matrix(tip), world_before)

    def test_reparent_to_singular_parent_is_rejected_atomically(self):
        scene, root, arm, tip = self.make_scene()
        flat = scene.create_node("flat", local=scaling(0, 0))
        version_before = scene.version
        parent_before, local_before = tip.parent, tip.local
        with self.assertRaises(UnsolvableReparentError):
            scene.reparent(tip, flat, keep_world=True)
        # Atomic: nothing changed, not even the version counter.
        self.assertIs(tip.parent, parent_before)
        self.assertEqual(tip.local, local_before)
        self.assertEqual(scene.version, version_before)

    def test_reparent_cycle_rejected(self):
        scene, root, arm, tip = self.make_scene()
        with self.assertRaises(CycleError):
            scene.reparent(root, tip)

    def test_snapshot_hit_query_for_old_version(self):
        scene, root, arm, tip = self.make_scene()
        snap = scene.snapshot()
        local_pt = (F(2), F(-3))
        world_pt = snap.local_to_world(tip, *local_pt)

        # Mutate ancestors and reparent after taking the snapshot.
        scene.set_local(root, translation(-50, 25))
        scene.reparent(arm, None)
        scene.set_local(tip, scaling(9))

        old_hit = snap.hit(tip, *world_pt)
        self.assertTrue(old_hit.ok)
        self.assertEqual(old_hit.point, local_pt)
        self.assertEqual(old_hit.version, snap.version)
        # Current view disagrees with the snapshot, as it should.
        self.assertNotEqual(scene.world_matrix(tip), snap.world_matrix(tip))

    def test_multiple_snapshots_are_independent_views(self):
        scene, root, arm, tip = self.make_scene()
        snaps = [scene.snapshot()]
        worlds = [scene.world_matrix(tip)]
        for shift in (1, 2, 3):
            scene.set_local(root, translation(10 + shift, 0))
            snaps.append(scene.snapshot())
            worlds.append(scene.world_matrix(tip))
        for snap, expected in zip(snaps, worlds):
            self.assertEqual(snap.world_matrix(tip), expected)

    def test_cache_binds_ancestor_versions_not_just_own(self):
        scene, root, arm, tip = self.make_scene()
        scene.world_matrix(tip)  # warm cache
        tip_version = tip.local_version
        misses = scene.cache_misses
        scene.set_local(root, translation(0, 99))  # touch ancestor only
        self.assertEqual(tip.local_version, tip_version)  # own version unchanged
        scene.world_matrix(tip)
        self.assertEqual(scene.cache_misses, misses + 1)  # still invalidated

    def test_cache_invalidation_is_local_to_subtree(self):
        scene, root, arm, tip = self.make_scene()
        sibling = scene.create_node("sibling", parent=root, local=scaling(3))
        for node in (root, arm, tip, sibling):
            scene.world_matrix(node)  # warm all caches
        misses = scene.cache_misses
        hits = scene.cache_hits

        scene.set_local(arm, translation(5, 5))
        scene.world_matrix(sibling)  # unaffected branch: cache hit
        self.assertEqual(scene.cache_misses, misses)
        self.assertEqual(scene.cache_hits, hits + 1)

        scene.world_matrix(tip)  # descendant of mutated node: miss
        self.assertEqual(scene.cache_misses, misses + 1)

    def test_unknown_version_rejected(self):
        scene, root, arm, tip = self.make_scene()
        with self.assertRaises(UnknownVersionError):
            scene.world_matrix(tip, version=scene.version + 1)


class TestRandomizedAgainstReference(unittest.TestCase):
    """Random ancestor edits + reparents, checked against an independent
    from-the-root composition of a parallel model kept by the test."""

    def test_fuzz(self):
        rng = random.Random(20260924)
        scene = Scene()
        nodes = []
        model = {}  # index -> list of (version, parent_index_or_None, local)

        def ref_world(index, version):
            m = IDENTITY
            i = index
            while i is not None:
                record = None
                for rec in reversed(model[i]):
                    if rec[0] <= version:
                        record = rec
                        break
                self.assertIsNotNone(record)
                _, parent_i, local = record
                m = local @ m
                i = parent_i
            return m

        for i in range(10):
            parent_i = rng.randrange(len(nodes)) if nodes and rng.random() < 0.7 else None
            local = random_mat(rng)
            node = scene.create_node(
                f"n{i}",
                parent=nodes[parent_i] if parent_i is not None else None,
                local=local,
            )
            model[i] = [(scene.version, parent_i, local)]
            nodes.append(node)

        snapshots = []
        for _ in range(400):
            op = rng.random()
            idx = rng.randrange(len(nodes))
            if op < 0.45:
                local = random_mat(rng)
                scene.set_local(nodes[idx], local)
                parent_i = model[idx][-1][1]
                model[idx].append((scene.version, parent_i, local))
            elif op < 0.80:
                choice = rng.randrange(len(nodes) + 1)
                new_parent_i = choice if choice < len(nodes) else None
                keep = rng.random() < 0.5
                try:
                    scene.reparent(
                        nodes[idx],
                        nodes[new_parent_i] if new_parent_i is not None else None,
                        keep_world=keep,
                    )
                except (CycleError, UnsolvableReparentError):
                    pass
                else:
                    model[idx].append(
                        (scene.version, new_parent_i, nodes[idx].local)
                    )
            else:
                snapshots.append(scene.snapshot())

            # 1. Current world matrices match from-the-root composition.
            for i, node in enumerate(nodes):
                self.assertEqual(scene.world_matrix(node), ref_world(i, scene.version))

            # 2. A random multi-view snapshot matches historical composition.
            if snapshots:
                snap = rng.choice(snapshots)
                for i, node in enumerate(nodes):
                    if model[i][0][0] > snap.version:
                        continue  # node did not exist yet
                    self.assertEqual(snap.world_matrix(node), ref_world(i, snap.version))

            # 3. Inverse hit roundtrips exactly, or reports NOT_INVERTIBLE.
            probe = rng.randrange(len(nodes))
            world = scene.world_matrix(nodes[probe])
            point = (rand_frac(rng), rand_frac(rng))
            result = scene.hit(nodes[probe], *point)
            if world.is_singular:
                self.assertEqual(result.status, HitStatus.NOT_INVERTIBLE)
            else:
                self.assertTrue(result.ok)
                self.assertEqual(scene.local_to_world(nodes[probe], *result.point), point)

            # 4. Snapshot hit queries agree with the historical inverse.
            if snapshots:
                snap = rng.choice(snapshots)
                probe = rng.randrange(len(nodes))
                if model[probe][0][0] <= snap.version:
                    old_world = ref_world(probe, snap.version)
                    old_hit = snap.hit(nodes[probe], *point)
                    if old_world.is_singular:
                        self.assertEqual(old_hit.status, HitStatus.NOT_INVERTIBLE)
                    else:
                        self.assertTrue(old_hit.ok)
                        self.assertEqual(
                            snap.local_to_world(nodes[probe], *old_hit.point), point
                        )


if __name__ == "__main__":
    unittest.main()
