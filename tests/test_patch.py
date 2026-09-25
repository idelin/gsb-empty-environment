import unittest

import graphdiff.hashing as hashing
from graphdiff import (
    Graph, Interval, diff, compose, Patch, BaseMismatchError,
    ContentMismatchError, ComposeError, RenameNode, SetAttr, AddNode,
    DelNode, AddEdge, DelEdge, ReconnectEdge,
)


def make_graphs():
    a = Graph()
    a.add_vertex("X", {"color": "red"})
    a.add_vertex("Y")
    a.add_vertex("iso")
    a.add_edge("e1", "X", "Y", {"w": 1})
    a.add_edge("e2", "X", "Y")

    b = a.copy()
    RenameNode("X", "A").apply(b)
    b.vertices["Y"].attrs["size"] = 3
    b.add_vertex("N")
    b.add_edge("e3", "A", "N")

    c = b.copy()
    c.vertices["Y"].attrs["size"] = 4
    del c.edges["e2"]
    c.add_edge("e4", "N", "N", interval=Interval(1, 5))
    return a, b, c


class TestApplyAndInverse(unittest.TestCase):
    def test_apply_equals_target(self):
        a, b, _ = make_graphs()
        p = diff(a, b, {"X": "A"})
        self.assertEqual(p.apply(a), b)

    def test_inverse_recovers_base(self):
        a, b, _ = make_graphs()
        p = diff(a, b, {"X": "A"})
        self.assertEqual(p.inverse().apply(b), a)

    def test_double_inverse_is_identity(self):
        a, b, _ = make_graphs()
        p = diff(a, b, {"X": "A"})
        self.assertEqual(p.inverse().inverse().apply(a), b)

    def test_apply_does_not_mutate_input(self):
        a, b, _ = make_graphs()
        snapshot = a.canonical()
        diff(a, b, {"X": "A"}).apply(a)
        self.assertEqual(a.canonical(), snapshot)

    def test_wrong_baseline_rejected_and_unchanged(self):
        a, b, _ = make_graphs()
        p = diff(a, b, {"X": "A"})
        wrong = a.copy()
        wrong.vertices["X"].attrs["color"] = "green"
        before = wrong.canonical()
        with self.assertRaises(BaseMismatchError):
            p.apply(wrong)
        self.assertEqual(wrong.canonical(), before)  # original untouched

    def test_digest_collision_rechecked_by_content(self):
        """A forged digest collision must not let a wrong baseline pass."""
        a, b, _ = make_graphs()
        wrong = a.copy()
        wrong.vertices["X"].attrs["color"] = "green"
        self.assertNotEqual(a.canonical(), wrong.canonical())

        real_digest = hashing.digest
        # inject a weak digest under which a and wrong collide
        hashing.digest = lambda content: "0" * 64
        try:
            p = diff(a, b, {"X": "A"})
            self.assertEqual(p.base_digest, "0" * 64)
            # digest of `wrong` also "matches" -> content re-check must reject
            before = wrong.canonical()
            with self.assertRaises(ContentMismatchError):
                p.apply(wrong)
            self.assertEqual(wrong.canonical(), before)
            # the true baseline still passes (content verified, not digest)
            self.assertEqual(p.apply(a), b)
        finally:
            hashing.digest = real_digest

    def test_json_roundtrip(self):
        a, b, _ = make_graphs()
        p = diff(a, b, {"X": "A"})
        p2 = Patch.from_json(p.to_json())
        self.assertEqual(p2.base_digest, p.base_digest)
        self.assertEqual(p2.apply(a), b)
        self.assertEqual(p2.inverse().apply(b), a)


class TestCompose(unittest.TestCase):
    def test_compose_equivalence(self):
        a, b, c = make_graphs()
        p1 = diff(a, b, {"X": "A"})
        p2 = diff(b, c)
        self.assertEqual(compose(p1, p2).apply(a), c)

    def test_compose_requires_matching_versions(self):
        a, b, c = make_graphs()
        p1 = diff(a, b, {"X": "A"})
        p2 = diff(b, c)
        p3 = diff(a, c, {"X": "A"})
        with self.assertRaises(ComposeError):
            compose(p1, p3)

    def test_compose_then_inverse_recovers(self):
        a, b, c = make_graphs()
        p = compose(diff(a, b, {"X": "A"}), diff(b, c))
        self.assertEqual(p.inverse().apply(c), a)

    def test_simplification_cancels_noop_chains(self):
        a, b, c = make_graphs()
        # b -> c only changes Y.size 3 -> 4; compose with a c->b patch
        p_fwd = diff(b, c)
        p_bwd = diff(c, b)
        p = compose(p_fwd, p_bwd)
        self.assertEqual(p.ops, [])  # net effect is empty
        self.assertEqual(p.apply(b), b)

    def test_compose_preserves_provenance(self):
        """Attribute provenance: composed SetAttr.old is the value in A."""
        g0 = Graph(); g0.add_vertex("n", {"k": 1})
        g1 = g0.copy(); g1.vertices["n"].attrs["k"] = 2
        g2 = g1.copy(); g2.vertices["n"].attrs["k"] = 3
        p = compose(diff(g0, g1), diff(g1, g2))
        self.assertEqual(len(p.ops), 1)
        op = p.ops[0]
        self.assertIsInstance(op, SetAttr)
        self.assertEqual((op.old, op.new), (1, 3))

    def test_compose_rename_chain(self):
        g0 = Graph(); g0.add_vertex("a")
        g1 = g0.copy(); RenameNode("a", "b").apply(g1)
        g2 = g1.copy(); RenameNode("b", "c").apply(g2)
        p = compose(diff(g0, g1, {"a": "b"}), diff(g1, g2, {"b": "c"}))
        self.assertEqual(len(p.ops), 1)
        self.assertIsInstance(p.ops[0], RenameNode)
        self.assertEqual((p.ops[0].old, p.ops[0].new), ("a", "c"))
        self.assertEqual(p.apply(g0), g2)

    def test_compose_add_then_delete_cancels(self):
        g0 = Graph(); g0.add_vertex("a")
        g1 = g0.copy(); g1.add_vertex("n", {"x": 1})
        p = compose(diff(g0, g1), diff(g1, g0))
        self.assertEqual(p.ops, [])

    def test_compose_reconnect_then_delete_keeps_base_endpoints(self):
        g0 = Graph()
        g0.add_vertex("a"); g0.add_vertex("b"); g0.add_vertex("c")
        g0.add_edge("e", "a", "b", {"w": 1})
        g1 = g0.copy(); g1.edges["e"].dst = "c"
        g2 = g1.copy(); del g2.edges["e"]
        p = compose(diff(g0, g1), diff(g1, g2))
        self.assertEqual(len(p.ops), 1)
        op = p.ops[0]
        self.assertIsInstance(op, DelEdge)
        self.assertEqual((op.src, op.dst), ("a", "b"))  # base-side endpoints
        self.assertEqual(p.inverse().apply(g2), g0)     # restores a->b edge

    def test_compose_preserves_multiplicity(self):
        g0 = Graph()
        g0.add_vertex("a"); g0.add_vertex("b")
        g0.add_edge("e1", "a", "b"); g0.add_edge("e2", "a", "b")
        g1 = g0.copy(); g1.add_edge("e3", "a", "b")
        g2 = g1.copy(); g2.edges["e1"].attrs["w"] = 9
        p = compose(diff(g0, g1), diff(g1, g2))
        out = p.apply(g0)
        self.assertEqual(out.multiplicity("a", "b"), 3)
        self.assertEqual(out, g2)


if __name__ == "__main__":
    unittest.main()
