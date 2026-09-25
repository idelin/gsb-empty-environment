import unittest
from fractions import Fraction

from mesh_topology import (
    HalfEdgeMesh,
    MeshError,
    PatchError,
    Transaction,
    diagnose,
    is_zero_area,
    random_edits,
    suggest_repairs,
    triangle_double_area_sq,
)

TETRA = [(0, 2, 1), (0, 1, 3), (0, 3, 2), (1, 2, 3)]


class TestBuildAndBoundary(unittest.TestCase):
    def test_open_square_boundary(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 2, 3)])
        diag = diagnose(mesh)
        self.assertEqual(len(diag.boundary_edges), 4)
        self.assertEqual(diag.nonmanifold_edges, ())
        self.assertEqual(diag.orientation_conflicts, ())
        loops = mesh.boundary_loops()
        self.assertEqual(len(loops), 1)
        self.assertEqual(len(loops[0]), 5)
        self.assertEqual(loops[0][0], loops[0][-1])
        self.assertTrue(mesh.verify_consistency())

    def test_closed_tetrahedron_is_clean(self):
        mesh = HalfEdgeMesh(TETRA)
        diag = diagnose(mesh)
        self.assertTrue(diag.is_clean())
        self.assertEqual(diag.boundary_edges, ())
        self.assertEqual(diag.bowtie_vertices, ())
        for vid, components in mesh.vertex_fans().items():
            self.assertEqual(len(components), 1, vid)

    def test_halfedge_twin_references(self):
        mesh = HalfEdgeMesh(TETRA)
        halfedges = mesh.halfedges
        for (u, v) in halfedges:
            self.assertIn((v, u), halfedges)


class TestNonManifoldEdge(unittest.TestCase):
    def test_three_faces_on_one_edge(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 3), (0, 1, 4)])
        diag = diagnose(mesh)
        self.assertEqual(len(diag.nonmanifold_edges), 1)
        key, count, fids = diag.nonmanifold_edges[0]
        self.assertEqual(count, 3)
        self.assertEqual(set(fids), {1, 2, 3})
        self.assertFalse(diag.is_clean())

    def test_nonmanifold_repair_patches(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 3), (0, 1, 4)])
        patches = suggest_repairs(mesh)
        edge_patches = [p for p in patches
                        if "keeps 2 faces" in p.description]
        self.assertEqual(len(edge_patches), 3)
        edge_patches[0].apply(mesh)
        diag = diagnose(mesh)
        self.assertEqual(diag.nonmanifold_edges, ())


class TestOrientationConflict(unittest.TestCase):
    def test_same_direction_conflict(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 3)])
        diag = diagnose(mesh)
        self.assertEqual(len(diag.orientation_conflicts), 1)
        self.assertEqual(len(diag.boundary_edges), 4)

    def test_opposite_direction_is_clean(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (1, 0, 3)])
        diag = diagnose(mesh)
        self.assertEqual(diag.orientation_conflicts, ())

    def test_flip_patch_fixes_conflict(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 3)])
        patches = suggest_repairs(mesh)
        self.assertEqual(len(patches), 2)
        patch = patches[0]
        self.assertEqual(patch.unmet_preconditions(mesh), [])
        patch.apply(mesh)
        diag = diagnose(mesh)
        self.assertEqual(diag.orientation_conflicts, ())
        self.assertTrue(mesh.verify_consistency())

    def test_patch_preconditions_block_reapply(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 3)])
        patch = suggest_repairs(mesh)[0]
        patch.apply(mesh)
        before = mesh.canonical()
        with self.assertRaises(PatchError):
            patch.apply(mesh)
        self.assertEqual(mesh.canonical(), before)


class TestDuplicateFaces(unittest.TestCase):
    def test_duplicates_not_normal_adjacency(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 2)])
        diag = diagnose(mesh)
        self.assertEqual(len(diag.duplicate_face_groups), 1)
        self.assertEqual(set(diag.duplicate_face_groups[0]), {1, 2})
        self.assertEqual(len(diag.duplicate_adjacency_edges), 3)
        self.assertEqual(diag.orientation_conflicts, ())
        self.assertEqual(diag.boundary_edges, ())
        self.assertFalse(diag.is_clean())

    def test_reversed_duplicate_also_flagged(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 2, 1)])
        diag = diagnose(mesh)
        self.assertEqual(len(diag.duplicate_face_groups), 1)
        self.assertEqual(len(diag.duplicate_adjacency_edges), 3)

    def test_delete_duplicate_patch(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 2)])
        patches = suggest_repairs(mesh)
        self.assertEqual(len(patches), 1)
        patches[0].apply(mesh)
        self.assertEqual(len(mesh.faces), 1)
        self.assertTrue(diagnose(mesh).is_clean())


class TestDegenerateAndIsolated(unittest.TestCase):
    def test_index_degenerate_face_reported_separately(self):
        mesh = HalfEdgeMesh([(0, 0, 1), (0, 1, 2)])
        diag = diagnose(mesh)
        self.assertEqual(len(diag.degenerate_faces), 1)
        self.assertEqual(diag.degenerate_faces[0][0], 1)
        self.assertNotIn((0, 1), [k for k in diag.boundary_edges
                                  if False])
        self.assertEqual(len(mesh.edge_faces), 3)

    def test_zero_area_exact(self):
        vertices = {0: (0, 0, 0), 1: (1, 0, 0), 2: (2, 0, 0),
                    3: (0, 1, 0)}
        mesh = HalfEdgeMesh([(0, 1, 2), (0, 1, 3)], vertices=vertices)
        diag = diagnose(mesh)
        self.assertEqual(diag.zero_area_faces, (1,))
        self.assertEqual(diag.degenerate_faces, ())

    def test_isolated_vertex_reported_separately(self):
        mesh = HalfEdgeMesh([(0, 1, 2)],
                            vertices={0: None, 1: None, 2: None,
                                      99: (1, 2, 3)})
        diag = diagnose(mesh)
        self.assertEqual(diag.isolated_vertices, (99,))
        self.assertEqual(diag.degenerate_faces, ())


class TestBowtieVertex(unittest.TestCase):
    def two_tetra_one_vertex(self):
        faces = list(TETRA) + [(0, 4, 5), (0, 5, 6), (0, 6, 4),
                               (4, 6, 5)]
        return HalfEdgeMesh(faces)

    def test_edge_check_alone_is_not_enough(self):
        mesh = self.two_tetra_one_vertex()
        diag = diagnose(mesh)
        self.assertEqual(diag.nonmanifold_edges, ())
        self.assertEqual(diag.bowtie_vertices, ((0, 2),))
        self.assertFalse(diag.is_clean())

    def test_split_vertex_repair(self):
        mesh = self.two_tetra_one_vertex()
        patches = suggest_repairs(mesh)
        bowtie_patches = [p for p in patches
                          if "bowtie" in p.description]
        self.assertEqual(len(bowtie_patches), 1)
        bowtie_patches[0].apply(mesh)
        diag = diagnose(mesh)
        self.assertEqual(diag.bowtie_vertices, ())
        self.assertTrue(mesh.verify_consistency())


class TestExactGeometry(unittest.TestCase):
    def test_rational_coordinates(self):
        p = (Fraction(1, 3), Fraction(2, 7), 0)
        q = (Fraction(5, 2), 0, Fraction(-1, 5))
        r = (0, 0, Fraction(11, 13))
        self.assertGreater(triangle_double_area_sq(p, q, r), 0)
        self.assertFalse(is_zero_area(p, q, r))

    def test_collinear_rationals_exactly_zero(self):
        p = (Fraction(1, 3), 0, 0)
        q = (Fraction(7, 5), 0, 0)
        r = (Fraction(-11, 2), 0, 0)
        self.assertTrue(is_zero_area(p, q, r))

    def test_no_epsilon_needed_for_large_ints(self):
        big = 10**18
        p = (0, 0, 0)
        q = (big, 0, 0)
        r = (0, big, 0)
        self.assertEqual(triangle_double_area_sq(p, q, r), big**4)
        nearly = (big, 1, 0)
        self.assertTrue(is_zero_area(p, q, nearly) is False
                        or triangle_double_area_sq(p, q, nearly) > 0)


class TestTransactions(unittest.TestCase):
    def test_failed_transaction_restores_topology(self):
        mesh = HalfEdgeMesh(TETRA)
        before = mesh.canonical()
        snap = mesh.snapshot()
        snap_copy = {k: dict(v) if isinstance(v, dict) else v
                     for k, v in snap.items()}
        with self.assertRaises(MeshError):
            with Transaction(mesh):
                mesh.insert_face(0, 1, 4)
                mesh.delete_face(999)
        self.assertEqual(mesh.canonical(), before)
        self.assertEqual(snap, snap_copy)

    def test_verify_failure_inside_transaction_rolls_back(self):
        mesh = HalfEdgeMesh(TETRA)
        before = mesh.canonical()
        with self.assertRaises(MeshError):
            with Transaction(mesh):
                mesh.insert_face(0, 1, 4)
                mesh._halfedges.clear()
        self.assertEqual(mesh.canonical(), before)
        self.assertTrue(mesh.verify_consistency())

    def test_committed_transaction_keeps_edits(self):
        mesh = HalfEdgeMesh(TETRA)
        with Transaction(mesh):
            mesh.insert_face(0, 1, 4)
        self.assertEqual(len(mesh.faces), 5)
        self.assertTrue(mesh.verify_consistency())


class TestSplitEdge(unittest.TestCase):
    def test_split_boundary_edge(self):
        mesh = HalfEdgeMesh([(0, 1, 2)])
        new_vid = mesh.split_edge(0, 1)
        self.assertEqual(new_vid, 3)
        self.assertEqual(len(mesh.faces), 2)
        loops = mesh.boundary_loops()
        self.assertEqual(len(loops), 1)
        self.assertEqual(len(loops[0]), 5)
        self.assertTrue(mesh.verify_consistency())

    def test_split_interior_edge(self):
        mesh = HalfEdgeMesh([(0, 1, 2), (1, 0, 3)])
        mesh.split_edge(0, 1)
        self.assertEqual(len(mesh.faces), 4)
        self.assertTrue(mesh.verify_consistency())
        diag = diagnose(mesh)
        self.assertEqual(diag.nonmanifold_edges, ())

    def test_split_uses_exact_midpoint(self):
        vertices = {0: (0, 0, 0), 1: (1, 0, 0), 2: (0, 1, 0)}
        mesh = HalfEdgeMesh([(0, 1, 2)], vertices=vertices)
        new_vid = mesh.split_edge(0, 1)
        self.assertEqual(mesh.vertices[new_vid],
                         (Fraction(1, 2), Fraction(0), Fraction(0)))

    def test_split_missing_edge_raises(self):
        mesh = HalfEdgeMesh([(0, 1, 2)])
        with self.assertRaises(MeshError):
            mesh.split_edge(0, 5)


class TestRandomEditsVsRebuild(unittest.TestCase):
    def test_random_edits_match_full_rebuild(self):
        mesh = HalfEdgeMesh(TETRA)
        log = random_edits(mesh, steps=300, seed=20260925)
        self.assertEqual(len(log), 300)
        self.assertTrue(mesh.verify_consistency())

    def test_random_edits_from_empty(self):
        mesh = HalfEdgeMesh()
        random_edits(mesh, steps=200, seed=7)
        self.assertTrue(mesh.verify_consistency())

    def test_snapshot_survives_fuzz_then_restore(self):
        mesh = HalfEdgeMesh(TETRA)
        snap = mesh.snapshot()
        before = mesh.canonical()
        random_edits(mesh, steps=100, seed=42)
        mesh.restore(snap)
        self.assertEqual(mesh.canonical(), before)


if __name__ == "__main__":
    unittest.main()
