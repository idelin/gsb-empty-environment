"""Diff/apply tests: change classification, renames, baseline verification."""
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from graphpatch import (
    AddEdge,
    AddVertex,
    ChangeEdgeEndpoints,
    ChangeVertexAttrs,
    Edge,
    Graph,
    Interval,
    PreconditionError,
    RemoveEdge,
    RemoveVertex,
    RenameConflictError,
    RenameVertex,
    Vertex,
    diff,
)
from helpers import mutate, random_graph


def small_graph() -> Graph:
    g = Graph()
    g.add_vertex(Vertex("alice", {"role": "admin"}, Interval(0.0, 10.0)))
    g.add_vertex(Vertex("bob", {"role": "user"}))
    g.add_vertex(Vertex("carol"))
    g.add_vertex(Vertex("dave"))  # isolated
    g.add_edge(Edge("e1", "alice", "bob", {"w": 1}, Interval(1.0, 5.0)))
    g.add_edge(Edge("e2", "alice", "bob", {"w": 2}))  # parallel edge
    g.add_edge(Edge("e3", "bob", "carol"))
    return g


class TestDiffClassification(unittest.TestCase):
    def test_rename_does_not_report_edge_delete_add(self):
        """Renaming A -> X with unchanged edges must not produce
        remove+add edge operations, nor endpoint changes."""
        g1 = small_graph()
        g2 = g1.copy()
        v = g2.vertices.pop("alice")
        g2.vertices["alex"] = Vertex("alex", dict(v.attrs), v.interval)
        for eid, e in list(g2.edges.items()):
            src = "alex" if e.src == "alice" else e.src
            dst = "alex" if e.dst == "alice" else e.dst
            g2.edges[eid] = Edge(eid, src, dst, dict(e.attrs), e.interval)

        p = diff(g1, g2, renames={"alice": "alex"})
        kinds = [type(op) for op in p.ops]
        self.assertEqual(kinds, [RenameVertex])
        self.assertEqual(p.ops[0].old, "alice")
        self.assertEqual(p.ops[0].new, "alex")
        # no edge churn at all
        for op in p.ops:
            self.assertNotIsInstance(op, (RemoveEdge, AddEdge, ChangeEdgeEndpoints))
        self.assertEqual(p.apply(g1), g2)

    def test_add_remove_attr_interval_classification(self):
        g1 = small_graph()
        g2 = g1.copy()
        # attr change on bob
        g2.vertices["bob"] = Vertex("bob", {"role": "moderator", "x": 1})
        # interval change on carol
        g2.vertices["carol"] = Vertex("carol", {}, Interval(0.0, 9.0))
        # remove dave (isolated vertex removal must be tracked)
        g2.remove_vertex("dave")
        # remove edge e2 (multiplicity 2 -> 1), add edge e4
        g2.remove_edge("e2")
        g2.add_edge(Edge("e4", "carol", "alice"))
        # add vertex eve
        g2.add_vertex(Vertex("eve", {"role": "guest"}))

        p = diff(g1, g2)
        by_type = {}
        for op in p.ops:
            by_type.setdefault(type(op).__name__, []).append(op)
        self.assertEqual([o.vid for o in by_type["AddVertex"]], ["eve"])
        self.assertEqual([o.vid for o in by_type["RemoveVertex"]], ["dave"])
        self.assertEqual([o.eid for o in by_type["AddEdge"]], ["e4"])
        self.assertEqual([o.eid for o in by_type["RemoveEdge"]], ["e2"])
        self.assertEqual([o.vid for o in by_type["ChangeVertexAttrs"]], ["bob"])
        self.assertEqual(len(by_type["ChangeVertexInterval"]), 1)
        self.assertEqual(p.apply(g1), g2)

    def test_rename_map_conflicts_detected(self):
        g1 = small_graph()
        g2 = g1.copy()
        g2.vertices["alex"] = g2.vertices.pop("alice")
        # source missing
        with self.assertRaises(RenameConflictError) as ctx:
            diff(g1, g2, renames={"ghost": "alex"})
        self.assertEqual(ctx.exception.conflicts[0]["kind"], "source_missing")
        # target missing in target graph
        with self.assertRaises(RenameConflictError) as ctx:
            diff(g1, g2, renames={"alice": "nobody"})
        self.assertTrue(
            any(c["kind"] == "target_missing" for c in ctx.exception.conflicts)
        )
        # duplicate targets
        with self.assertRaises(RenameConflictError) as ctx:
            diff(g1, g2, renames={"alice": "alex", "bob": "alex"})
        self.assertTrue(
            any(c["kind"] == "duplicate_target" for c in ctx.exception.conflicts)
        )
        # collision with an unrenamed survivor
        with self.assertRaises(RenameConflictError) as ctx:
            diff(g1, g2, renames={"alice": "bob"})
        self.assertTrue(
            any(c["kind"] == "target_collision" for c in ctx.exception.conflicts)
        )
        # trivial rename
        with self.assertRaises(RenameConflictError) as ctx:
            diff(g1, g2, renames={"alice": "alice"})
        self.assertTrue(
            any(c["kind"] == "trivial_rename" for c in ctx.exception.conflicts)
        )

    def test_pure_renames_never_churn_edges_random(self):
        """Across random graphs with self-loops and parallel edges, a pure
        rename produces only RenameVertex ops and identity-equality holds."""
        rng = random.Random(101)
        for trial in range(40):
            g1 = random_graph(rng, rng.randint(1, 8), rng.randint(0, 12))
            g2 = g1.copy()
            candidates = sorted(g2.vertices)
            rng.shuffle(candidates)
            renames = {}
            used = set(candidates)
            for vid in candidates[: rng.randint(1, min(3, len(candidates)))]:
                new = f"{vid}_ren{trial}"
                if new not in used:
                    renames[vid] = new
                    used.add(new)
            for old, new in renames.items():
                v = g2.vertices.pop(old)
                g2.vertices[new] = Vertex(new, dict(v.attrs), v.interval)
                for eid, e in list(g2.edges.items()):
                    src2 = new if e.src == old else e.src
                    dst2 = new if e.dst == old else e.dst
                    g2.edges[eid] = Edge(eid, src2, dst2, dict(e.attrs), e.interval)
            p = diff(g1, g2, renames)
            self.assertTrue(
                all(isinstance(op, RenameVertex) for op in p.ops),
                f"trial {trial}: unexpected ops {[type(o).__name__ for o in p.ops]}",
            )
            self.assertEqual(p.apply(g1), g2)
            self.assertEqual(p.inverse().apply(g2), g1)


class TestBaselineVerification(unittest.TestCase):
    def test_wrong_baseline_rejected_and_graph_untouched(self):
        g1 = small_graph()
        g2, renames = mutate(random.Random(3), g1)
        p = diff(g1, g2, renames)
        wrong = g1.copy()
        wrong.vertices["bob"] = Vertex("bob", {"role": "intruder"})
        before = wrong.canonical()
        with self.assertRaises(PreconditionError):
            p.apply(wrong)
        self.assertEqual(wrong.canonical(), before)  # unchanged

    def test_digest_collision_still_rejected(self):
        """A weak digest that collides must never be enough to accept a
        wrong baseline: content is re-verified."""
        g1 = small_graph()
        g2, renames = mutate(random.Random(4), g1)
        # deliberately weak digest: everything collides
        weak = lambda text: "COLLIDE"
        p = diff(g1, g2, renames, digest_fn=weak)
        wrong = g1.copy()
        wrong.vertices["bob"] = Vertex("bob", {"role": "intruder"})
        self.assertEqual(weak(wrong.canonical()), p.base_digest)  # collision!
        before = wrong.canonical()
        with self.assertRaises(PreconditionError) as ctx:
            p.apply(wrong, digest_fn=weak)
        self.assertIn("collision", str(ctx.exception))
        self.assertEqual(wrong.canonical(), before)
        # ...while the genuine baseline is accepted
        self.assertEqual(p.apply(g1, digest_fn=weak), g2)

    def test_apply_equals_target_on_random_graphs(self):
        rng = random.Random(42)
        for trial in range(50):
            g1 = random_graph(rng, rng.randint(1, 10), rng.randint(0, 15))
            g2, renames = mutate(rng, g1, tag=f"t{trial}")
            p = diff(g1, g2, renames)
            self.assertEqual(p.apply(g1), g2, f"trial {trial}")
            # target hash verification is active by default
            self.assertEqual(p.apply(g1).content_hash(), p.target_hash)


if __name__ == "__main__":
    unittest.main()
