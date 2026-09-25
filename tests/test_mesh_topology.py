import random
import unittest
from fractions import Fraction

from mesh_topology import (
    Mesh,
    RepairPatch,
    analyze,
    suggest_repairs,
    triangle_is_degenerate,
    vertex_fan_components,
)

# 一致定向的闭合四面体
TETRA_COORDS = [(0, 0, 0), (1, 0, 0), (0, 1, 0), (0, 0, 1)]
TETRA_FACES = [(0, 2, 1), (0, 1, 3), (1, 2, 3), (0, 3, 2)]


def make_tetra():
    return Mesh(TETRA_COORDS, TETRA_FACES)


class TestBuildAndBoundary(unittest.TestCase):
    def test_open_quad_boundary(self):
        mesh = Mesh([(0, 0), (1, 0), (1, 1), (0, 1)], [(0, 1, 2), (0, 2, 3)])
        diag = analyze(mesh)
        self.assertEqual(len(diag.boundary_edges), 4)
        self.assertEqual(diag.boundary_loops, [[0, 1, 2, 3, 0]])
        self.assertFalse(diag.nonmanifold_edges)
        self.assertFalse(diag.orientation_conflicts)
        self.assertEqual(mesh.check_consistency(), [])

    def test_closed_tetra_is_clean(self):
        diag = analyze(make_tetra())
        self.assertTrue(diag.is_clean(), diag.summary())
        self.assertEqual(diag.boundary_loops, [])

    def test_halfedge_twin_links(self):
        mesh = make_tetra()
        hes = mesh.half_edges()
        for key, he in hes.items():
            self.assertIsNotNone(he.twin)
            self.assertEqual(hes[he.twin].twin, key)
            self.assertEqual(hes[he.next].prev, key)


class TestDefectDetection(unittest.TestCase):
    def test_orientation_conflict(self):
        mesh = make_tetra()
        mesh.flip_face(0)
        diag = analyze(mesh)
        self.assertEqual(len(diag.orientation_conflicts), 3)
        for (a, b), f1, f2 in diag.orientation_conflicts:
            self.assertIn(0, (f1, f2))

    def test_edge_with_three_faces_is_nonmanifold(self):
        mesh = Mesh(
            [(0, 0), (1, 0), (0, 1), (1, 1), (2, 0)],
            [(0, 1, 2), (0, 1, 3), (1, 0, 4)],
        )
        diag = analyze(mesh)
        self.assertEqual(diag.nonmanifold_edges, [((0, 1), (0, 1, 2))])

    def test_duplicate_faces_not_normal_adjacency(self):
        mesh = Mesh(
            [(0, 0), (1, 0), (1, 1), (0, 1)],
            [(0, 1, 2), (0, 2, 3), (0, 1, 2), (2, 1, 0)],
        )
        diag = analyze(mesh)
        self.assertEqual(diag.duplicate_face_groups, [(0, 2, 3)])
        # 重复面使共享边挂 3 个面，必须标非流形而不是当作正常相邻
        nm = dict(diag.nonmanifold_edges)
        self.assertEqual(nm[(0, 1)], (0, 2, 3))
        self.assertEqual(nm[(1, 2)], (0, 2, 3))

    def test_degenerate_and_isolated_reported_separately(self):
        mesh = Mesh(
            [(0, 0), (1, 1), (2, 2), (0, 0), (1, 0), (9, 9)],
            [(0, 1, 2), (3, 3, 4)],
        )
        diag = analyze(mesh)
        reasons = dict(diag.degenerate_faces)
        self.assertIn("零面积", reasons[0])
        self.assertIn("顶点索引重复", reasons[1])
        self.assertEqual(diag.isolated_vertices, [5])

    def test_bowtie_vertex(self):
        # 两个三角形仅共享顶点 0：每边至多两面，但顶点非流形
        mesh = Mesh(
            [(0, 0), (1, 0), (0, 1), (-1, 0), (0, -1)],
            [(0, 1, 2), (0, 3, 4)],
        )
        diag = analyze(mesh)
        self.assertFalse(diag.nonmanifold_edges)
        self.assertEqual(diag.bowtie_vertices, [(0, ((0,), (1,)))])
        self.assertEqual(vertex_fan_components(mesh, 0), ((0,), (1,)))

    def test_bowtie_two_tetra_sharing_apex(self):
        coords = TETRA_COORDS + [(0, 0, 2), (1, 0, 2), (0, 1, 2)]
        faces = TETRA_FACES + [(0, 4, 5), (0, 5, 6), (0, 6, 4), (4, 6, 5)]
        mesh = Mesh(coords, faces)
        diag = analyze(mesh)
        bowties = dict(diag.bowtie_vertices)
        self.assertIn(0, bowties)
        self.assertEqual(len(bowties[0]), 2)


class TestExactArithmetic(unittest.TestCase):
    def test_integer_collinear_is_degenerate(self):
        self.assertTrue(triangle_is_degenerate((0, 0), (1, 1), (2, 2)))
        self.assertTrue(triangle_is_degenerate((0, 0, 0), (1, 2, 3), (2, 4, 6)))

    def test_rational_collinear_is_degenerate(self):
        f = Fraction(1, 3)
        self.assertTrue(triangle_is_degenerate((0, 0), (f, f), (2 * f, 2 * f)))

    def test_near_miss_not_degenerate_no_epsilon(self):
        tiny = Fraction(1, 10**12)
        self.assertFalse(triangle_is_degenerate((0, 0), (1, 0), (0, tiny)))

    def test_float_coordinate_rejected(self):
        mesh = Mesh()
        with self.assertRaises(TypeError):
            mesh.add_vertex((0.5, 0.0))

    def test_exact_midpoint(self):
        mesh = Mesh([(0, 0), (1, 0), (0, 1)], [(0, 1, 2)])
        m = mesh.split_edge(0, 1)
        self.assertEqual(mesh.coords[m], (Fraction(1, 2), Fraction(0)))


class TestTransactionalEdits(unittest.TestCase):
    def test_insert_delete_face_updates_maps(self):
        mesh = make_tetra()
        mesh.delete_face(0)
        diag = analyze(mesh)
        self.assertEqual(len(diag.boundary_edges), 3)
        fid = mesh.insert_face(0, 2, 1)
        self.assertEqual(fid, 0)  # 复用空槽
        self.assertTrue(analyze(mesh).is_clean())
        self.assertEqual(mesh.check_consistency(), [])

    def test_split_boundary_edge(self):
        mesh = Mesh([(0, 0), (1, 0), (0, 1)], [(0, 1, 2)])
        m = mesh.split_edge(0, 1)
        self.assertEqual(len(mesh.live_faces()), 2)
        loops, _ = mesh.boundary_loops()
        self.assertEqual(loops, [[0, m, 1, 2, 0]])
        self.assertEqual(mesh.check_consistency(), [])

    def test_split_shared_edge(self):
        mesh = Mesh([(0, 0), (1, 0), (1, 1), (0, 1)], [(0, 1, 2), (0, 2, 3)])
        mesh.split_edge(0, 2)
        self.assertEqual(len(mesh.live_faces()), 4)
        self.assertTrue(analyze(mesh).is_clean() or not analyze(mesh).nonmanifold_edges)
        self.assertEqual(mesh.check_consistency(), [])

    def test_split_nonexistent_edge_raises(self):
        mesh = make_tetra()
        with self.assertRaises(IndexError):
            mesh.split_edge(0, 4)  # 顶点 4 不存在

    def test_split_missing_edge_keyerror(self):
        mesh = Mesh([(0, 0), (1, 0), (0, 1), (2, 2)], [(0, 1, 2)])
        with self.assertRaises(KeyError):
            mesh.split_edge(0, 3)

    def test_failed_transaction_preserves_snapshot(self):
        mesh = make_tetra()
        snap = (list(mesh.coords), list(mesh.faces))
        diag_before = analyze(mesh)
        with self.assertRaises(RuntimeError):
            with mesh.transaction():
                mesh.delete_face(0)
                mesh.split_edge(0, 1)
                mesh.insert_face(0, 1, 2)
                raise RuntimeError("模拟失败")
        self.assertEqual((mesh.coords, mesh.faces), snap)
        self.assertEqual(analyze(mesh), diag_before)
        self.assertEqual(mesh.check_consistency(), [])

    def test_committed_transaction_persists(self):
        mesh = make_tetra()
        with mesh.transaction():
            mesh.delete_face(0)
        self.assertEqual(len(mesh.live_faces()), 3)
        self.assertEqual(mesh.check_consistency(), [])


class TestRepairPatches(unittest.TestCase):
    def test_suggestions_not_auto_applied(self):
        mesh = Mesh(
            [(0, 0), (1, 0), (1, 1), (0, 1)],
            [(0, 1, 2), (0, 2, 3), (0, 1, 2)],
        )
        before = list(mesh.faces)
        patches = suggest_repairs(mesh)
        self.assertTrue(patches)
        self.assertEqual(mesh.faces, before)

    def test_flip_patch_fixes_conflict(self):
        mesh = Mesh([(0, 0), (1, 0), (1, 1), (0, 1)], [(0, 1, 2), (0, 2, 3)])
        mesh.flip_face(1)
        self.assertEqual(len(analyze(mesh).orientation_conflicts), 1)
        patch = suggest_repairs(mesh)[0]
        self.assertEqual(patch.kind, "flip_face")
        self.assertEqual(patch.preconditions(mesh), [])
        patch.apply(mesh)
        self.assertFalse(analyze(mesh).orientation_conflicts)
        self.assertFalse(analyze(mesh).nonmanifold_edges)

    def test_delete_duplicate_patch(self):
        mesh = Mesh(
            [(0, 0), (1, 0), (1, 1), (0, 1)],
            [(0, 1, 2), (0, 2, 3), (0, 1, 2)],
        )
        patches = [p for p in suggest_repairs(mesh) if p.kind == "delete_duplicate"]
        self.assertEqual(len(patches), 1)
        patches[0].apply(mesh)
        diag = analyze(mesh)
        self.assertFalse(diag.duplicate_face_groups)
        self.assertFalse(diag.orientation_conflicts)

    def test_nonmanifold_edge_patch(self):
        mesh = Mesh(
            [(0, 0), (1, 0), (0, 1), (1, 1), (2, 1)],
            [(0, 1, 2), (0, 1, 3), (1, 0, 4)],
        )
        patches = [p for p in suggest_repairs(mesh) if p.kind == "delete_face"]
        self.assertEqual(len(patches), 3)  # 每个备选面一个独立补丁
        patches[0].apply(mesh)
        self.assertFalse(analyze(mesh).nonmanifold_edges)

    def test_split_vertex_patch_fixes_bowtie(self):
        mesh = Mesh(
            [(0, 0), (1, 0), (0, 1), (-1, 0), (0, -1)],
            [(0, 1, 2), (0, 3, 4)],
        )
        patches = [p for p in suggest_repairs(mesh) if p.kind == "split_vertex"]
        self.assertEqual(len(patches), 1)
        patches[0].apply(mesh)
        self.assertFalse(analyze(mesh).bowtie_vertices)
        self.assertEqual(len(mesh.coords), 6)
        self.assertEqual(mesh.check_consistency(), [])

    def test_precondition_failure_blocks_apply(self):
        mesh = Mesh(
            [(0, 0), (1, 0), (1, 1), (0, 1)],
            [(0, 1, 2), (0, 2, 3), (0, 1, 2)],
        )
        patch = [p for p in suggest_repairs(mesh) if p.kind == "delete_duplicate"][0]
        mesh.delete_face(0)  # 破坏前置条件：保留面已不存在
        self.assertTrue(patch.preconditions(mesh))
        with self.assertRaises(RuntimeError):
            patch.apply(mesh)

    def test_remove_isolated_vertex_patch(self):
        mesh = Mesh([(0, 0), (1, 0), (0, 1), (9, 9)], [(0, 1, 2)])
        patch = [p for p in suggest_repairs(mesh) if p.kind == "remove_isolated_vertex"][0]
        patch.apply(mesh)
        self.assertEqual(len(mesh.coords), 3)
        self.assertEqual(analyze(mesh).isolated_vertices, [])

    def test_remove_non_isolated_vertex_rejected(self):
        mesh = make_tetra()
        with self.assertRaises(ValueError):
            mesh.remove_isolated_vertex(0)


class TestFuzzAgainstRebuild(unittest.TestCase):
    def test_random_edits_match_full_rebuild(self):
        rng = random.Random(20260925)
        mesh = Mesh()
        for _ in range(6):
            mesh.add_vertex(tuple(rng.randrange(3) for _ in range(3)))
        for step in range(400):
            op = rng.randrange(5)
            try:
                if op == 0 and len(mesh.coords) < 30:
                    mesh.add_vertex(tuple(rng.randrange(4) for _ in range(3)))
                elif op == 1 and mesh.coords:
                    mesh.insert_face(*[rng.randrange(len(mesh.coords)) for _ in range(3)])
                elif op == 2:
                    live = list(mesh.live_faces())
                    if live:
                        mesh.delete_face(rng.choice(live))
                elif op == 3:
                    keys = list(mesh.undirected_edge_faces())
                    if keys:
                        a, b = rng.choice(keys)
                        if a != b:
                            mesh.split_edge(a, b)
                else:
                    live = list(mesh.live_faces())
                    if live:
                        mesh.flip_face(rng.choice(live))
            except (KeyError, ValueError, IndexError):
                pass
            # 增量维护的映射必须等于全量重建
            self.assertEqual(mesh.check_consistency(), [], f"step {step}")
        # 诊断结果必须与全新重建网格一致（面 id 经压缩映射后比较）
        fresh = Mesh(list(mesh.coords), [t for t in mesh.faces if t is not None])
        self.assertEqual(_canon(analyze(mesh), mesh), analyze(fresh))

    def test_random_transactions_with_failures(self):
        rng = random.Random(7)
        mesh = Mesh([(i, i * i % 5, 0) for i in range(6)], [(0, 1, 2), (2, 3, 4)])
        for step in range(120):
            snap = (list(mesh.coords), list(mesh.faces))
            diag_before = analyze(mesh)
            fail = rng.random() < 0.5
            try:
                with mesh.transaction():
                    for _ in range(rng.randrange(1, 5)):
                        live = list(mesh.live_faces())
                        choice = rng.randrange(3)
                        if choice == 0 and len(mesh.coords) > 0:
                            mesh.insert_face(*[rng.randrange(len(mesh.coords)) for _ in range(3)])
                        elif choice == 1 and live:
                            mesh.delete_face(rng.choice(live))
                        elif choice == 2:
                            keys = [k for k in mesh.undirected_edge_faces() if k[0] != k[1]]
                            if keys:
                                mesh.split_edge(*rng.choice(keys))
                    if fail:
                        raise RuntimeError("注入失败")
            except (RuntimeError, KeyError, ValueError, IndexError):
                if fail:
                    # 失败必须完整保留原拓扑与旧快照
                    self.assertEqual((mesh.coords, mesh.faces), snap, f"step {step}")
                    self.assertEqual(analyze(mesh), diag_before, f"step {step}")
            self.assertEqual(mesh.check_consistency(), [], f"step {step}")


def _canon(diag, mesh):
    """把诊断中的面 id 映射为压缩后的新 id，以便与重建网格对照。"""
    fid_map = {old: new for new, old in enumerate(sorted(mesh.live_faces()))}

    def mf(f):
        return fid_map[f]

    return type(diag)(
        boundary_edges=diag.boundary_edges,
        boundary_loops=diag.boundary_loops,
        ambiguous_boundary_vertices=diag.ambiguous_boundary_vertices,
        nonmanifold_edges=[(k, tuple(mf(f) for f in fs)) for k, fs in diag.nonmanifold_edges],
        orientation_conflicts=[(k, mf(a), mf(b)) for k, a, b in diag.orientation_conflicts],
        duplicate_face_groups=[tuple(mf(f) for f in g) for g in diag.duplicate_face_groups],
        degenerate_faces=[(mf(f), r) for f, r in diag.degenerate_faces],
        isolated_vertices=diag.isolated_vertices,
        bowtie_vertices=[(v, tuple(tuple(mf(f) for f in c) for c in comps)) for v, comps in diag.bowtie_vertices],
    )


if __name__ == "__main__":
    unittest.main()
