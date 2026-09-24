"""Composition and inversion: identity, multiplicity, provenance."""
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from graphpatch import (
    MISSING,
    AddVertex,
    AttrDelta,
    ChangeVertexAttrs,
    Edge,
    Graph,
    Interval,
    Patch,
    RemoveVertex,
    RenameVertex,
    Vertex,
    compose,
    diff,
)
from helpers import mutate, random_graph


class TestAttrDeltaAlgebra(unittest.TestCase):
    def test_compose_keeps_earliest_before_and_latest_after(self):
        d1 = AttrDelta.between({"a": 1, "b": 2}, {"a": 10, "b": 2})
        d2 = AttrDelta.between({"a": 10, "b": 2}, {"a": 99, "b": 7})
        merged = d1.then(d2)
        self.assertEqual(merged.before("a"), 1)   # provenance: original value
        self.assertEqual(merged.after("a"), 99)
        self.assertEqual(merged.before("b"), 2)
        self.assertEqual(merged.after("b"), 7)

    def test_roundtrip_change_drops_key(self):
        d1 = AttrDelta.between({"a": 1}, {"a": 2})
        d2 = AttrDelta.between({"a": 2}, {"a": 1})
        self.assertTrue(d1.then(d2).is_empty())

    def test_inverse_restores_provenance(self):
        before = {"a": 1, "gone": "x"}
        after = {"a": 2, "new": [1, 2]}
        delta = AttrDelta.between(before, after)
        self.assertEqual(delta.inverse().apply_to(after), before)
        self.assertIs(delta.before("new"), MISSING)
        self.assertIs(delta.after("gone"), MISSING)

    def test_bool_int_zero_are_distinct_values(self):
        # 0 == False in Python, but they are different attribute values
        delta = AttrDelta.between({"k": 0}, {"k": False})
        self.assertEqual(len(delta), 1)
        self.assertEqual(delta.apply_to({"k": 0}), {"k": False})


class TestCompose(unittest.TestCase):
    def test_random_compose_equivalence(self):
        """compose(p12, p23) applied to g1 must equal applying both in turn."""
        rng = random.Random(2024)
        for trial in range(60):
            g1 = random_graph(rng, rng.randint(1, 9), rng.randint(0, 14))
            g2, r12 = mutate(rng, g1, tag=f"a{trial}")
            g3, r23 = mutate(rng, g2, tag=f"b{trial}")
            p12 = diff(g1, g2, r12)
            p23 = diff(g2, g3, r23)
            composed = compose(p12, p23, g1)
            sequential = p23.apply(p12.apply(g1))
            direct = composed.apply(g1)
            self.assertEqual(direct, g3, f"trial {trial}")
            self.assertEqual(direct, sequential, f"trial {trial}")
            self.assertEqual(direct.content_hash(), composed.target_hash)

    def test_compose_preserves_provenance(self):
        g1 = Graph()
        g1.add_vertex(Vertex("n", {"a": 1}))
        g2 = Graph()
        g2.add_vertex(Vertex("n", {"a": 2}))
        g3 = Graph()
        g3.add_vertex(Vertex("n", {"a": 3}))
        p12 = diff(g1, g2)
        p23 = diff(g2, g3)
        composed = compose(p12, p23, g1)
        (op,) = [o for o in composed.ops if isinstance(o, ChangeVertexAttrs)]
        self.assertEqual(op.delta.before("a"), 1)  # original base value kept
        self.assertEqual(op.delta.after("a"), 3)

    def test_compose_add_then_remove_cancels(self):
        g1 = Graph()
        g1.add_vertex(Vertex("stay"))
        g2 = g1.copy()
        g2.add_vertex(Vertex("temp", {"x": 1}))
        g3 = g1.copy()
        p12 = diff(g1, g2)
        p23 = diff(g2, g3)
        composed = compose(p12, p23, g1)
        self.assertEqual(composed.ops, [])
        self.assertEqual(composed.apply(g1), g1)

    def test_compose_remove_then_readd_becomes_modification(self):
        g1 = Graph()
        g1.add_vertex(Vertex("n", {"a": 1}))
        g2 = Graph()  # n removed
        g3 = Graph()
        g3.add_vertex(Vertex("n", {"a": 2, "b": 3}))
        composed = compose(diff(g1, g2), diff(g2, g3), g1)
        kinds = {type(o).__name__ for o in composed.ops}
        self.assertEqual(kinds, {"ChangeVertexAttrs"})
        self.assertEqual(composed.apply(g1), g3)

    def test_compose_chained_renames_collapse(self):
        g1 = Graph()
        g1.add_vertex(Vertex("a"))
        g1.add_vertex(Vertex("b"))
        g1.add_edge(Edge("e1", "a", "b"))
        g2 = g1.copy()
        v = g2.vertices.pop("a")
        g2.vertices["x"] = Vertex("x", dict(v.attrs), v.interval)
        g2.edges["e1"] = Edge("e1", "x", "b")
        g3 = g2.copy()
        v = g3.vertices.pop("x")
        g3.vertices["y"] = Vertex("y", dict(v.attrs), v.interval)
        g3.edges["e1"] = Edge("e1", "y", "b")
        composed = compose(
            diff(g1, g2, {"a": "x"}), diff(g2, g3, {"x": "y"}), g1
        )
        renames = [o for o in composed.ops if isinstance(o, RenameVertex)]
        self.assertEqual(len(renames), 1)
        self.assertEqual((renames[0].old, renames[0].new), ("a", "y"))
        # the internal rename must not leak out as an endpoint change
        self.assertFalse(
            any(type(o).__name__ == "ChangeEdgeEndpoints" for o in composed.ops)
        )
        self.assertEqual(composed.apply(g1), g3)

    def test_compose_preserves_edge_multiplicity(self):
        rng = random.Random(99)
        for trial in range(40):
            g1 = random_graph(rng, 4, 10)
            # force parallel edges
            vids = sorted(g1.vertices)
            g1.add_edge(Edge(f"par{trial}a", vids[0], vids[1]))
            g1.add_edge(Edge(f"par{trial}b", vids[0], vids[1]))
            g2, r12 = mutate(rng, g1, tag=f"m{trial}")
            g3, r23 = mutate(rng, g2, tag=f"n{trial}")
            composed = compose(diff(g1, g2, r12), diff(g2, g3, r23), g1)
            result = composed.apply(g1)
            self.assertEqual(result, g3)
            # multiplicity per endpoint pair matches the target exactly
            pairs = {(e.src, e.dst) for e in g3.edges.values()}
            for src, dst in pairs:
                self.assertEqual(
                    result.edge_multiplicity(src, dst),
                    g3.edge_multiplicity(src, dst),
                )


class TestInverse(unittest.TestCase):
    def test_random_inverse_recovery(self):
        """apply(inverse(p), apply(p, g)) == g, including multiedges and
        isolated vertices."""
        rng = random.Random(7)
        for trial in range(60):
            g1 = random_graph(rng, rng.randint(1, 9), rng.randint(0, 14))
            g2, renames = mutate(rng, g1, tag=f"i{trial}")
            p = diff(g1, g2, renames)
            forward = p.apply(g1)
            self.assertEqual(forward, g2, f"trial {trial}")
            back = p.inverse().apply(forward)
            self.assertEqual(back, g1, f"trial {trial}")
            # double inverse is the identity patch
            self.assertEqual(p.inverse().inverse().apply(g1), g2)

    def test_inverse_of_composition_recovers_base(self):
        rng = random.Random(11)
        for trial in range(40):
            g1 = random_graph(rng, rng.randint(1, 8), rng.randint(0, 12))
            g2, r12 = mutate(rng, g1, tag=f"x{trial}")
            g3, r23 = mutate(rng, g2, tag=f"y{trial}")
            composed = compose(diff(g1, g2, r12), diff(g2, g3, r23), g1)
            self.assertEqual(composed.inverse().apply(composed.apply(g1)), g1)

    def test_inverse_preserves_multiplicity_and_isolation(self):
        g1 = Graph()
        g1.add_vertex(Vertex("a"))
        g1.add_vertex(Vertex("b"))
        g1.add_vertex(Vertex("iso"))
        g1.add_edge(Edge("p1", "a", "b", {"k": 1}))
        g1.add_edge(Edge("p2", "a", "b", {"k": 2}))
        g2 = g1.copy()
        g2.remove_edge("p1")
        g2.remove_vertex("iso")
        p = diff(g1, g2)
        restored = p.inverse().apply(p.apply(g1))
        self.assertEqual(restored, g1)
        self.assertEqual(restored.edge_multiplicity("a", "b"), 2)
        self.assertIn("iso", restored.vertices)


if __name__ == "__main__":
    unittest.main()
