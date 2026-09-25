import json
import unittest
from fractions import Fraction

from geom import Point, orient, segments_intersect, signed_area2
from mesh import (Mesh, Transaction, ValidationError, decode_patch,
                  encode_patch, invert_patch)


def two_triangle_mesh():
    """Two triangles sharing edge v2-v3 (shared vertices)."""
    m = Mesh()
    m.add_vertex("v1", 0, 0)
    m.add_vertex("v2", 4, 0)
    m.add_vertex("v3", 2, 3)
    m.add_vertex("v4", 6, 0)
    m.add_face("f1", ["v1", "v2", "v3"])
    m.add_face("f2", ["v2", "v4", "v3"])
    return m


class TestExactGeometry(unittest.TestCase):
    def test_orient_exact_large_integers(self):
        a = Point(0, 0)
        b = Point(10 ** 18, 1)
        self.assertEqual(orient(a, b, Point(10 ** 18, 1)), 0)
        self.assertEqual(orient(a, b, Point(0, 1)), 1)
        self.assertEqual(orient(a, b, Point(0, -1)), -1)

    def test_rational_collinearity_is_exact(self):
        a = Point(0, 0)
        b = Point(2, 3)
        self.assertEqual(orient(a, b, Point(Fraction(2, 3), 1)), 0)
        # One part in 10^12 off the line: still exactly "not on the line".
        off = Point(Fraction(2, 3), Fraction(1) + Fraction(1, 10 ** 12))
        self.assertNotEqual(orient(a, b, off), 0)

    def test_segment_intersection_touching(self):
        p1, p2 = Point(0, 0), Point(4, 0)
        p3, p4 = Point(4, 0), Point(4, 4)
        self.assertTrue(segments_intersect(p1, p2, p3, p4))
        self.assertFalse(segments_intersect(p1, p2, Point(0, 1), Point(4, 1)))


class TestMoveVertex(unittest.TestCase):
    def test_move_shared_vertex_updates_all_faces(self):
        m = two_triangle_mesh()
        patch = m.move_vertex("v3", 3, 5)
        self.assertEqual(m.vertices["v3"], Point(3, 5))
        # Both faces reference the same vertex object; both see the move.
        self.assertEqual(m.face_points("f1")[2], Point(3, 5))
        self.assertEqual(m.face_points("f2")[2], Point(3, 5))
        self.assertEqual(patch["op"], "move_vertex")
        self.assertEqual(patch["old"], (Fraction(2), Fraction(3)))
        self.assertEqual(patch["new"], (Fraction(3), Fraction(5)))

    def test_move_preserves_orientation(self):
        m = two_triangle_mesh()
        before = m.orientation("f1")
        m.move_vertex("v3", 3, 5)
        self.assertEqual(m.orientation("f1"), before)

    def test_move_causing_self_intersection_rejected(self):
        m = Mesh()
        for vid, xy in {"a": (0, 0), "b": (4, 0), "c": (4, 4), "d": (0, 4)}.items():
            m.add_vertex(vid, *xy)
        m.add_face("sq", ["a", "b", "c", "d"])
        before = m.snapshot()
        # Moving "a" to (2, 5) makes edge a-b cross edge c-d at (8/3, 4).
        with self.assertRaises(ValidationError):
            m.move_vertex("a", 2, 5)
        self.assertEqual(m, before)

    def test_move_creating_zero_length_edge_rejected(self):
        m = two_triangle_mesh()
        before = m.snapshot()
        with self.assertRaises(ValidationError):
            m.move_vertex("v1", 4, 0)  # would coincide with v2
        self.assertEqual(m, before)

    def test_move_unknown_vertex_rejected(self):
        m = two_triangle_mesh()
        with self.assertRaises(ValidationError):
            m.move_vertex("nope", 0, 0)


class TestSplitEdge(unittest.TestCase):
    def test_split_edge_inserts_vertex(self):
        m = two_triangle_mesh()
        patch = m.split_edge("f1", 0, "v5", 2, 0)  # edge v1->v2
        self.assertEqual(m.faces["f1"], ["v1", "v5", "v2", "v3"])
        self.assertEqual(m.vertices["v5"], Point(2, 0))
        self.assertEqual(patch["new_vid"], "v5")
        self.assertEqual(m.orientation("f1"), 1)  # orientation preserved

    def test_split_edge_rational_point(self):
        m = Mesh()
        m.add_vertex("a", 0, 0)
        m.add_vertex("b", 2, 3)
        m.add_vertex("c", 4, 0)
        m.add_face("t", ["a", "b", "c"])
        m.split_edge("t", 0, "m", Fraction(2, 3), 1)  # exactly on a->b
        self.assertEqual(m.vertices["m"], Point(Fraction(2, 3), 1))

    def test_split_point_off_edge_rejected_exactly(self):
        m = two_triangle_mesh()
        before = m.snapshot()
        # (2, 1/10^12) is microscopically above edge v1->v2 (y=0): rejected,
        # because topology is decided exactly, not with an epsilon.
        with self.assertRaises(ValidationError):
            m.split_edge("f1", 0, "v5", 2, Fraction(1, 10 ** 12))
        self.assertEqual(m, before)

    def test_split_at_endpoint_rejected(self):
        m = two_triangle_mesh()
        with self.assertRaises(ValidationError):
            m.split_edge("f1", 0, "v5", 0, 0)

    def test_split_duplicate_vertex_id_rejected(self):
        m = two_triangle_mesh()
        with self.assertRaises(ValidationError):
            m.split_edge("f1", 0, "v2", 2, 0)


class TestMergeEdges(unittest.TestCase):
    def make_quad(self):
        m = Mesh()
        for vid, xy in {"a": (0, 0), "b": (4, 0), "c": (4, 4), "d": (0, 4)}.items():
            m.add_vertex(vid, *xy)
        m.add_face("q", ["a", "b", "c", "d"])
        return m

    def test_merge_edges_removes_vertex_from_ring(self):
        m = self.make_quad()
        patch = m.merge_edges("q", 1)  # remove b
        self.assertEqual(m.faces["q"], ["a", "c", "d"])
        # Vertex stays in the store (may be shared with other faces).
        self.assertEqual(m.vertices["b"], Point(4, 0))
        self.assertEqual(patch["removed_vid"], "b")
        self.assertEqual(m.orientation("q"), 1)

    def test_merge_keeps_shared_vertex_for_other_faces(self):
        m = Mesh()
        for vid, xy in {"a": (0, 0), "b": (4, 0), "c": (4, 4),
                        "d": (0, 4), "e": (8, 0), "f": (8, 4)}.items():
            m.add_vertex(vid, *xy)
        m.add_face("left", ["a", "b", "c", "d"])
        m.add_face("right", ["b", "e", "f", "c"])
        m.merge_edges("left", 1)  # remove b from left only
        self.assertEqual(m.faces["left"], ["a", "c", "d"])
        self.assertEqual(m.faces["right"], ["b", "e", "f", "c"])
        self.assertIn("b", m.vertices)

    def test_merge_causing_self_intersection_rejected(self):
        m = Mesh()
        # Arrow-shaped simple pentagon; removing "b" makes edge a-c cross d-e.
        for vid, xy in {"a": (0, 0), "b": (4, 0), "c": (4, 3),
                        "d": (2, 1), "e": (0, 3)}.items():
            m.add_vertex(vid, *xy)
        m.add_face("arrow", ["a", "b", "c", "d", "e"])
        before = m.snapshot()
        with self.assertRaises(ValidationError):
            m.merge_edges("arrow", 1)
        self.assertEqual(m, before)

    def test_merge_triangle_rejected(self):
        m = two_triangle_mesh()
        with self.assertRaises(ValidationError):
            m.merge_edges("f1", 0)


class TestTransaction(unittest.TestCase):
    def test_commit_returns_forward_patches(self):
        m = two_triangle_mesh()
        tx = Transaction(m)
        tx.move_vertex("v3", 3, 5).split_edge("f1", 0, "v5", 2, 0)
        patches = tx.commit()
        self.assertEqual([p["op"] for p in patches],
                         ["move_vertex", "split_edge"])
        self.assertEqual(m.vertices["v3"], Point(3, 5))
        self.assertEqual(m.faces["f1"], ["v1", "v5", "v2", "v3"])

    def test_failed_transaction_rolls_back_everything(self):
        m = two_triangle_mesh()
        before = m.snapshot()
        tx = Transaction(m)
        tx.move_vertex("v3", 3, 5)          # would succeed
        tx.split_edge("f1", 0, "v5", 2, 0)  # would succeed
        tx.merge_edges("f2", 0)             # fails: f2 is a triangle
        with self.assertRaises(ValidationError):
            tx.commit()
        self.assertEqual(m, before)  # full rollback, IDs and coords intact

    def test_forward_then_inverse_patches_restore_state(self):
        m = two_triangle_mesh()
        before = m.snapshot()
        tx = Transaction(m)
        tx.move_vertex("v3", Fraction(7, 2), 5)
        tx.split_edge("f1", 0, "v5", Fraction(3, 2), 0)
        # v3 is now at (7/2, 5); midpoint of edge v4(6,0)->v3 is (19/4, 5/2).
        tx.split_edge("f2", 1, "v6", Fraction(19, 4), Fraction(5, 2))
        patches = tx.commit()
        self.assertNotEqual(m, before)
        for patch in reversed(patches):
            m.apply_patch(invert_patch(patch))
        self.assertEqual(m, before)  # IDs and coordinates exactly restored
        self.assertNotIn("v5", m.vertices)
        self.assertNotIn("v6", m.vertices)

    def test_merge_then_inverse_restores_ring_and_id(self):
        m = Mesh()
        for vid, xy in {"a": (0, 0), "b": (4, 0), "c": (4, 4), "d": (0, 4)}.items():
            m.add_vertex(vid, *xy)
        m.add_face("q", ["a", "b", "c", "d"])
        before = m.snapshot()
        patch = m.merge_edges("q", 1)
        m.apply_patch(invert_patch(patch))
        self.assertEqual(m, before)
        self.assertEqual(m.faces["q"], ["a", "b", "c", "d"])


class TestPatchSerialization(unittest.TestCase):
    def test_patch_json_round_trip(self):
        m = two_triangle_mesh()
        patches = Transaction(m).move_vertex(
            "v3", Fraction(7, 2), Fraction(1, 3)).commit()
        blob = json.dumps([encode_patch(p) for p in patches])
        restored = [decode_patch(d) for d in json.loads(blob)]
        self.assertEqual(restored, patches)
        m2 = two_triangle_mesh()
        for p in restored:
            m2.apply_patch(p)
        self.assertEqual(m, m2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
