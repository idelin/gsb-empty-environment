"""Transaction, referential-integrity, rollback and patch tests."""
import unittest
from fractions import Fraction

from geomesh import GeometryError, Mesh, Patch, Transaction


def two_squares() -> Mesh:
    """Two square faces sharing edge B-C; B and C are shared vertices."""
    mesh = Mesh()
    for vid, x, y in [
        ("A", 0, 0), ("B", 4, 0), ("C", 4, 4),
        ("D", 0, 4), ("G", 8, 0), ("H", 8, 4),
    ]:
        mesh.add_vertex(vid, x, y)
    for eid, v1, v2 in [
        ("eAB", "A", "B"), ("eBC", "B", "C"), ("eCD", "C", "D"),
        ("eDA", "D", "A"), ("eBG", "B", "G"), ("eGH", "G", "H"),
        ("eHC", "H", "C"),
    ]:
        mesh.add_edge(eid, v1, v2)
    mesh.add_face("left", ["A", "B", "C", "D"])
    mesh.add_face("right", ["B", "G", "H", "C"])
    return mesh


def pentagon() -> Mesh:
    """Simple CCW pentagon where merging ePV+eVQ makes the chord P-Q cross R-S."""
    mesh = Mesh()
    for vid, x, y in [
        ("P", 0, 2), ("V", 2, 0), ("Q", 4, 2), ("R", 1, 3), ("S", 2, 1),
    ]:
        mesh.add_vertex(vid, x, y)
    for eid, v1, v2 in [
        ("ePV", "P", "V"), ("eVQ", "V", "Q"), ("eQR", "Q", "R"),
        ("eRS", "R", "S"), ("eSP", "S", "P"),
    ]:
        mesh.add_edge(eid, v1, v2)
    mesh.add_face("f", ["P", "V", "Q", "R", "S"])
    return mesh


class TestMoveVertex(unittest.TestCase):
    def test_shared_vertex_updates_all_referencing_faces(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        tx.move_vertex("B", Fraction(3), Fraction(1))
        tx.commit()
        self.assertEqual(mesh.point_of("B"), (Fraction(3), Fraction(1)))
        for fid in ("left", "right"):
            self.assertIn((Fraction(3), Fraction(1)), mesh.face_coords(fid))
        self.assertEqual(mesh.face_orientation("left"), 1)
        self.assertEqual(mesh.face_orientation("right"), 1)
        mesh.validate()

    def test_move_with_rational_coordinates(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        tx.move_vertex("B", Fraction(7, 2), Fraction(1, 3))
        tx.commit()
        self.assertEqual(mesh.point_of("B"), (Fraction(7, 2), Fraction(1, 3)))
        mesh.validate()

    def test_move_flipping_orientation_is_rejected(self):
        mesh = two_squares()
        before = mesh.to_dict()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.move_vertex("B", -1, 5)  # turns the left face inside out
        self.assertEqual(mesh.to_dict(), before)

    def test_move_causing_self_intersection_is_rejected(self):
        mesh = two_squares()
        before = mesh.to_dict()
        tx = Transaction(mesh)
        # (0,0)->(4,0)->(2,-1)->(0,4): edge C-D crosses edge A-B, same orientation.
        with self.assertRaises(GeometryError):
            tx.move_vertex("C", 2, -1)
        self.assertEqual(mesh.to_dict(), before)

    def test_move_collapsing_edge_is_rejected(self):
        mesh = two_squares()
        before = mesh.to_dict()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.move_vertex("A", 4, 0)  # lands exactly on B, collapsing eAB
        self.assertEqual(mesh.to_dict(), before)

    def test_move_unknown_vertex(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        with self.assertRaises(KeyError):
            tx.move_vertex("ZZ", 0, 0)


class TestSplitEdge(unittest.TestCase):
    def test_split_updates_all_referencing_faces(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        mid, e1, e2 = tx.split_edge("eBC", 4, 2)  # shared by both faces
        tx.commit()
        self.assertEqual(mesh.point_of(mid), (Fraction(4), Fraction(2)))
        self.assertNotIn("eBC", mesh.edges)
        self.assertEqual(mesh.vertex_degree(mid), 2)
        for fid in ("left", "right"):
            vids = mesh.faces[fid].vids
            self.assertIn(mid, vids)
            self.assertEqual(len(vids), 5)
        self.assertEqual(mesh.face_area2("left"), 32)  # area unchanged
        self.assertEqual(mesh.face_area2("right"), 32)
        mesh.validate()

    def test_split_with_rational_point(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        mid, _, _ = tx.split_edge("eAB", Fraction(3, 2), 0)
        tx.commit()
        self.assertEqual(mesh.point_of(mid), (Fraction(3, 2), Fraction(0)))

    def test_split_outside_segment_rejected(self):
        mesh = two_squares()
        before = mesh.to_dict()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.split_edge("eAB", 5, 0)  # beyond endpoint B
        self.assertEqual(mesh.to_dict(), before)

    def test_split_at_endpoint_rejected(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.split_edge("eAB", 0, 0)  # exactly endpoint A

    def test_split_off_line_rejected(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.split_edge("eAB", 2, 1)  # not collinear with the edge


class TestMergeEdges(unittest.TestCase):
    def test_merge_removes_degree2_vertex(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        mid, e1, e2 = tx.split_edge("eAB", 2, 0)
        merged, removed = tx.merge_edges(e1, e2)
        tx.commit()
        self.assertEqual(removed, mid)
        self.assertNotIn(mid, mesh.vertices)
        self.assertNotIn(e1, mesh.edges)
        self.assertNotIn(e2, mesh.edges)
        self.assertEqual(mesh.edges[merged].v1, "A")
        self.assertEqual(mesh.edges[merged].v2, "B")
        self.assertEqual(mesh.faces["left"].vids, ["A", "B", "C", "D"])
        self.assertEqual(mesh.face_area2("left"), 32)
        mesh.validate()

    def test_merge_self_intersection_rejected(self):
        mesh = pentagon()
        before = mesh.to_dict()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.merge_edges("ePV", "eVQ")  # chord P-Q crosses edge R-S
        self.assertEqual(mesh.to_dict(), before)
        mesh.validate()

    def test_merge_requires_shared_vertex(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.merge_edges("eAB", "eCD")  # disjoint edges

    def test_merge_requires_degree_two(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.merge_edges("eAB", "eBC")  # B has degree 3 (eAB, eBC, eBG)

    def test_merge_same_edge_rejected(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        with self.assertRaises(GeometryError):
            tx.merge_edges("eAB", "eAB")


class TestTransactionAtomicity(unittest.TestCase):
    def test_failing_op_rolls_back_whole_transaction(self):
        mesh = two_squares()
        before = mesh.to_dict()
        tx = Transaction(mesh)
        tx.move_vertex("A", 1, 1)
        tx.split_edge("eBC", 4, 2)
        with self.assertRaises(GeometryError):
            tx.move_vertex("A", 4, 0)  # collapses eAB -> whole tx rolls back
        self.assertEqual(mesh.to_dict(), before)
        mesh.validate()

    def test_explicit_rollback(self):
        mesh = two_squares()
        before = mesh.to_dict()
        tx = Transaction(mesh)
        tx.move_vertex("B", 3, 1)
        tx.split_edge("eCD", 2, 4)
        tx.rollback()
        self.assertEqual(mesh.to_dict(), before)

    def test_transaction_cannot_be_reused(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        tx.move_vertex("B", 3, 1)
        tx.commit()
        with self.assertRaises(RuntimeError):
            tx.move_vertex("B", 0, 0)
        tx2 = Transaction(mesh)
        tx2.rollback()
        with self.assertRaises(RuntimeError):
            tx2.commit()


class TestPatch(unittest.TestCase):
    def test_forward_and_inverse_restore_ids_and_coordinates(self):
        mesh = two_squares()
        original = mesh.to_dict()
        tx = Transaction(mesh, description="move + split + merge")
        tx.move_vertex("B", Fraction(3), Fraction(1))
        mid, e1, e2 = tx.split_edge("eCD", Fraction(2), 4)
        tx.merge_edges(e1, e2)
        patch = tx.commit()
        edited = mesh.to_dict()
        self.assertNotEqual(edited, original)

        patch.revert(mesh)
        self.assertEqual(mesh.to_dict(), original)  # IDs and coords restored
        self.assertIn("eCD", mesh.edges)
        self.assertNotIn(mid, mesh.vertices)
        self.assertEqual(mesh.point_of("B"), (Fraction(4), Fraction(0)))

        patch.apply(mesh)
        self.assertEqual(mesh.to_dict(), edited)
        mesh.validate()

    def test_patch_json_round_trip(self):
        mesh = two_squares()
        original = mesh.to_dict()
        tx = Transaction(mesh, description="json")
        tx.move_vertex("C", Fraction(9, 2), Fraction(7, 2))
        tx.split_edge("eAB", Fraction(1, 3), 0)
        patch = tx.commit()
        edited = mesh.to_dict()

        clone = Patch.from_json(patch.to_json())
        self.assertEqual(clone.to_dict(), patch.to_dict())
        clone.revert(mesh)
        self.assertEqual(mesh.to_dict(), original)
        clone.apply(mesh)
        self.assertEqual(mesh.to_dict(), edited)

    def test_patch_is_plain_json_data(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        tx.move_vertex("B", 3, 1)
        patch = tx.commit()
        data = patch.to_dict()
        self.assertEqual(set(data), {"description", "forward", "inverse"})
        self.assertEqual(
            data["forward"],
            [{"op": "set_vertex", "vid": "B", "x": 3, "y": 1}],
        )
        self.assertEqual(
            data["inverse"],
            [{"op": "set_vertex", "vid": "B", "x": 4, "y": 0}],
        )

    def test_rational_coordinates_encoded_as_strings(self):
        mesh = two_squares()
        tx = Transaction(mesh)
        tx.move_vertex("B", Fraction(7, 2), Fraction(1, 3))
        patch = tx.commit()
        entry = patch.forward[0]
        self.assertEqual(entry["x"], "7/2")
        self.assertEqual(entry["y"], "1/3")


if __name__ == "__main__":
    unittest.main()
