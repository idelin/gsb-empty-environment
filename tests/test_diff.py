import unittest

from graphdiff import (
    Graph, Interval, diff, RenameMapError, RenameNode, AddNode, DelNode,
    AddEdge, DelEdge, ReconnectEdge, SetAttr, SetInterval,
)
from graphdiff.ops import MISSING


def sample_base():
    g = Graph()
    g.add_vertex("X", {"color": "red"})
    g.add_vertex("Y")
    g.add_vertex("Z", interval=Interval(0, 10))
    g.add_vertex("iso")  # isolated vertex
    g.add_edge("e1", "X", "Y", {"w": 1})
    g.add_edge("e2", "X", "Y")  # parallel edge (multiplicity 2)
    g.add_edge("e3", "Y", "Z")
    return g


class TestRenameMapValidation(unittest.TestCase):
    def test_missing_source(self):
        with self.assertRaises(RenameMapError) as cm:
            diff(sample_base(), Graph(), {"nope": "N"})
        self.assertEqual(cm.exception.conflicts[0].kind, "missing_source")

    def test_duplicate_target(self):
        with self.assertRaises(RenameMapError) as cm:
            diff(sample_base(), Graph(), {"X": "N", "Y": "N"})
        kinds = {c.kind for c in cm.exception.conflicts}
        self.assertIn("duplicate_target", kinds)

    def test_target_occupied(self):
        with self.assertRaises(RenameMapError) as cm:
            diff(sample_base(), Graph(), {"X": "Y"})
        self.assertEqual(cm.exception.conflicts[0].kind, "target_occupied")

    def test_simultaneous_swap_allowed(self):
        base = sample_base()
        target = base.copy()
        RenameNode("X", "tmp").apply(target)
        RenameNode("Y", "X").apply(target)
        RenameNode("tmp", "Y").apply(target)
        p = diff(base, target, {"X": "Y", "Y": "X"})
        self.assertEqual(p.apply(base), target)


class TestDiffClassification(unittest.TestCase):
    def test_rename_does_not_delete_readd_edges(self):
        """A renames X, edges unchanged: no DelEdge/AddEdge may be reported."""
        base = sample_base()
        target = base.copy()
        RenameNode("X", "A").apply(target)
        p = diff(base, target, {"X": "A"})
        kinds = [type(op).__name__ for op in p.ops]
        self.assertNotIn("DelEdge", kinds)
        self.assertNotIn("AddEdge", kinds)
        self.assertNotIn("DelNode", kinds)
        self.assertNotIn("AddNode", kinds)
        self.assertEqual(kinds, ["RenameNode"])
        self.assertEqual(p.apply(base), target)

    def test_add_delete_attrchange_classified(self):
        base = sample_base()
        target = base.copy()
        del target.edges["e3"]                       # deletion
        target.add_vertex("N", {"k": "v"})           # addition
        target.add_edge("e4", "N", "iso")            # addition
        target.vertices["X"].attrs["color"] = "blue"  # attr change
        p = diff(base, target)
        self.assertTrue(any(isinstance(o, DelEdge) and o.eid == "e3"
                            for o in p.ops))
        self.assertTrue(any(isinstance(o, AddNode) and o.vid == "N"
                            for o in p.ops))
        self.assertTrue(any(isinstance(o, AddEdge) and o.eid == "e4"
                            for o in p.ops))
        self.assertTrue(any(isinstance(o, SetAttr) and o.eid == "X"
                            and o.old == "red" and o.new == "blue"
                            for o in p.ops))
        self.assertEqual(p.apply(base), target)

    def test_reconnect_vs_delete_add(self):
        """Endpoint move on a surviving edge is a reconnect, not del+add."""
        base = sample_base()
        target = base.copy()
        target.edges["e3"].src = "X"
        target.edges["e3"].dst = "X"
        p = diff(base, target)
        self.assertEqual([type(o).__name__ for o in p.ops], ["ReconnectEdge"])
        self.assertEqual(p.apply(base), target)

    def test_attr_delete_key(self):
        base = sample_base()
        target = base.copy()
        del target.vertices["X"].attrs["color"]
        p = diff(base, target)
        op = p.ops[0]
        self.assertIsInstance(op, SetAttr)
        self.assertIs(op.new, MISSING)
        self.assertEqual(p.apply(base), target)

    def test_interval_change(self):
        base = sample_base()
        target = base.copy()
        target.vertices["Z"].interval = Interval(2, 8)
        p = diff(base, target)
        self.assertTrue(any(isinstance(o, SetInterval) for o in p.ops))
        self.assertEqual(p.apply(base), target)

    def test_isolated_vertices_and_multiedges_preserved(self):
        base = sample_base()
        target = base.copy()
        target.vertices["iso"].attrs["lonely"] = True
        p = diff(base, target)
        out = p.apply(base)
        self.assertIn("iso", out.vertices)
        self.assertEqual(out.multiplicity("X", "Y"), 2)
        self.assertEqual(out, target)


class TestHalfOpenIntervals(unittest.TestCase):
    def test_left_closed_right_open(self):
        iv = Interval(2, 5)
        self.assertTrue(iv.contains(2))      # left boundary included
        self.assertTrue(iv.contains(4.999))
        self.assertFalse(iv.contains(5))     # right boundary excluded
        self.assertFalse(iv.contains(1.999))

    def test_unbounded(self):
        self.assertTrue(Interval().contains(-10**9))
        self.assertTrue(Interval(None, 5).contains(-100))
        self.assertFalse(Interval(None, 5).contains(5))
        self.assertTrue(Interval(5, None).contains(5))

    def test_graph_at(self):
        g = Graph()
        g.add_vertex("a", interval=Interval(0, 10))
        g.add_vertex("b", interval=Interval(10, 20))
        g.add_edge("e", "a", "b", interval=Interval(0, 20))
        self.assertEqual(set(g.at(5).vertices), {"a"})
        self.assertEqual(set(g.at(10).vertices), {"b"})  # 10 in [10,20), not [0,10)
        self.assertEqual(set(g.at(15).edges), set())     # endpoint a absent

    def test_adjacent_intervals_do_not_overlap(self):
        self.assertFalse(Interval(0, 10).overlaps(Interval(10, 20)))
        self.assertTrue(Interval(0, 11).overlaps(Interval(10, 20)))


if __name__ == "__main__":
    unittest.main()
