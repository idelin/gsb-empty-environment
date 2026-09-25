import unittest

from graphdiff import Graph, Interval, diff, replay, RenameNode


def base_graph():
    g = Graph()
    g.add_vertex("X", {"color": "red"})
    g.add_vertex("Y")
    g.add_vertex("Z")
    g.add_edge("e1", "X", "Y", {"w": 1})
    return g


class TestRelocation(unittest.TestCase):
    def test_add_edge_relocated_through_other_rename(self):
        """Other baseline renamed X->R; the patch's new edge to X must be
        relocated to R."""
        base = base_graph()
        target = base.copy()
        target.add_edge("e2", "X", "Z")          # patch adds edge X->Z
        p = diff(base, target)

        other = base.copy()
        RenameNode("X", "R").apply(other)        # other branch renamed X

        res = replay(p, other, {"X": "R"})
        self.assertTrue(res.ok, res.conflicts)
        self.assertIn("R", res.graph.vertices)
        self.assertEqual(res.graph.edges["e2"].src, "R")  # relocated
        self.assertEqual(res.graph.edges["e1"].src, "R")  # other's change kept
        self.assertTrue(res.relocations)

    def test_attr_write_relocated_through_rename(self):
        base = base_graph()
        target = base.copy()
        target.vertices["X"].attrs["size"] = 5
        p = diff(base, target)

        other = base.copy()
        RenameNode("X", "R").apply(other)
        res = replay(p, other, {"X": "R"})
        self.assertTrue(res.ok, res.conflicts)
        self.assertEqual(res.graph.vertices["R"].attrs["size"], 5)

    def test_del_edge_relocated_through_rename(self):
        """DelEdge stores endpoints for inversion; they must follow the
        other branch's renames too."""
        base = base_graph()
        target = base.copy()
        del target.edges["e1"]
        p = diff(base, target)

        other = base.copy()
        RenameNode("Y", "R").apply(other)
        res = replay(p, other, {"Y": "R"})
        self.assertTrue(res.ok, res.conflicts)
        self.assertNotIn("e1", res.graph.edges)
        self.assertTrue(any(r.op == "DelEdge" for r in res.relocations))

    def test_non_conflicting_independent_changes_merge(self):
        base = base_graph()
        target = base.copy()
        target.vertices["X"].attrs["color"] = "blue"
        target.add_vertex("W")
        p = diff(base, target)

        other = base.copy()
        other.vertices["Y"].attrs["tag"] = "t"
        other.add_edge("e9", "Y", "Z")
        res = replay(p, other)
        self.assertTrue(res.ok, res.conflicts)
        self.assertEqual(res.graph.vertices["X"].attrs["color"], "blue")
        self.assertEqual(res.graph.vertices["Y"].attrs["tag"], "t")
        self.assertIn("e9", res.graph.edges)
        self.assertIn("W", res.graph.vertices)


class TestStructuredConflicts(unittest.TestCase):
    def test_rename_vs_delete(self):
        base = base_graph()
        target = base.copy()
        RenameNode("X", "A").apply(target)
        p = diff(base, target, {"X": "A"})

        other = base.copy()
        del other.edges["e1"]
        del other.vertices["X"]                     # other deleted X
        res = replay(p, other)
        self.assertFalse(res.ok)
        self.assertIsNone(res.graph)
        kinds = [c.kind for c in res.conflicts]
        self.assertIn("rename_vs_delete", kinds)
        c = res.conflicts[0]
        self.assertEqual((c.entity, c.entity_id), ("node", "X"))
        self.assertEqual(c.ours, ("rename", "X", "A"))

    def test_same_name_different_identity_node(self):
        base = base_graph()
        target = base.copy()
        target.add_vertex("N", {"who": "patch"})
        p = diff(base, target)

        other = base.copy()
        other.add_vertex("N", {"who": "other"})     # same id, different content
        res = replay(p, other)
        self.assertFalse(res.ok)
        self.assertEqual(res.conflicts[0].kind, "same_name_different_identity")
        self.assertEqual(res.conflicts[0].entity, "node")

    def test_same_name_different_identity_edge(self):
        base = base_graph()
        target = base.copy()
        target.add_edge("e2", "X", "Z", {"w": 1})
        p = diff(base, target)

        other = base.copy()
        other.add_edge("e2", "Y", "Z", {"w": 2})
        res = replay(p, other)
        self.assertFalse(res.ok)
        self.assertEqual(res.conflicts[0].kind, "same_name_different_identity")
        self.assertEqual(res.conflicts[0].entity, "edge")

    def test_identical_additions_are_not_conflicts(self):
        base = base_graph()
        target = base.copy()
        target.add_vertex("N", {"who": "both"})
        p = diff(base, target)

        other = base.copy()
        other.add_vertex("N", {"who": "both"})
        res = replay(p, other)
        self.assertTrue(res.ok, res.conflicts)
        self.assertEqual(res.graph.vertices["N"].attrs, {"who": "both"})

    def test_rename_vs_rename(self):
        base = base_graph()
        target = base.copy()
        RenameNode("X", "A").apply(target)
        p = diff(base, target, {"X": "A"})

        other = base.copy()
        RenameNode("X", "B").apply(other)
        res = replay(p, other, {"X": "B"})
        self.assertFalse(res.ok)
        self.assertEqual(res.conflicts[0].kind, "rename_vs_rename")

    def test_same_rename_on_both_sides_is_idempotent(self):
        base = base_graph()
        target = base.copy()
        RenameNode("X", "A").apply(target)
        target.vertices["A"].attrs["n"] = 1
        p = diff(base, target, {"X": "A"})

        other = base.copy()
        RenameNode("X", "A").apply(other)
        res = replay(p, other, {"X": "A"})
        self.assertTrue(res.ok, res.conflicts)
        self.assertEqual(res.graph.vertices["A"].attrs["n"], 1)

    def test_delete_vs_modify(self):
        base = base_graph()
        target = base.copy()
        del target.edges["e1"]
        del target.vertices["X"]
        p = diff(base, target)

        other = base.copy()
        other.vertices["X"].attrs["color"] = "green"
        res = replay(p, other)
        self.assertFalse(res.ok)
        self.assertEqual(res.conflicts[0].kind, "delete_vs_modify")

    def test_attr_vs_attr(self):
        base = base_graph()
        target = base.copy()
        target.vertices["X"].attrs["color"] = "blue"
        p = diff(base, target)

        other = base.copy()
        other.vertices["X"].attrs["color"] = "green"
        res = replay(p, other)
        self.assertFalse(res.ok)
        c = res.conflicts[0]
        self.assertEqual(c.kind, "attr_vs_attr")
        self.assertEqual((c.ours, c.theirs), ("blue", "green"))

    def test_conflict_leaves_other_unchanged(self):
        base = base_graph()
        target = base.copy()
        target.add_vertex("N", {"who": "patch"})
        target.vertices["X"].attrs["color"] = "blue"
        p = diff(base, target)

        other = base.copy()
        other.add_vertex("N", {"who": "other"})
        before = other.canonical()
        res = replay(p, other)
        self.assertFalse(res.ok)
        self.assertEqual(other.canonical(), before)


if __name__ == "__main__":
    unittest.main()
