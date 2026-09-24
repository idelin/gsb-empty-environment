"""Three-way replay: relocation of non-conflicting changes, structured
conflicts for rename-vs-delete and same-name-different-identity."""
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from graphpatch import (
    Edge,
    Graph,
    Interval,
    Vertex,
    diff,
    replay,
)
from graphpatch import rebase as rb
from helpers import mutate, random_graph


def base_graph() -> Graph:
    g = Graph()
    g.add_vertex(Vertex("alice", {"role": "admin", "level": 3}, Interval(0.0, 10.0)))
    g.add_vertex(Vertex("bob", {"role": "user"}))
    g.add_vertex(Vertex("carol"))
    g.add_edge(Edge("e1", "alice", "bob", {"w": 1}))
    g.add_edge(Edge("e2", "bob", "carol"))
    return g


class TestRelocation(unittest.TestCase):
    def test_non_conflicting_baseline_changes_relocate(self):
        base = base_graph()
        # patch: rename alice->alex, change bob.role, add edge e3
        target = base.copy()
        v = target.vertices.pop("alice")
        target.vertices["alex"] = Vertex("alex", dict(v.attrs), v.interval)
        for eid, e in list(target.edges.items()):
            src = "alex" if e.src == "alice" else e.src
            dst = "alex" if e.dst == "alice" else e.dst
            target.edges[eid] = Edge(eid, src, dst, dict(e.attrs), e.interval)
        target.vertices["bob"] = Vertex("bob", {"role": "moderator"})
        target.add_edge(Edge("e3", "alex", "carol"))
        p = diff(base, target, {"alice": "alex"})

        # alternate baseline: independent changes
        alt = base.copy()
        alt.vertices["carol"] = Vertex("carol", {"mood": "happy"})  # new attr
        alt.vertices["bob"] = Vertex("bob", {"role": "user", "dept": "eng"})
        alt.add_vertex(Vertex("frank"))  # extra isolated vertex

        result = replay(p, alt)
        self.assertTrue(result.ok, result.conflicts)
        relocated = result.patch.apply(alt)
        # patch effects landed
        self.assertIn("alex", relocated.vertices)
        self.assertNotIn("alice", relocated.vertices)
        self.assertEqual(relocated.vertices["bob"].attrs["role"], "moderator")
        self.assertIn("e3", relocated.edges)
        # baseline's own changes survived
        self.assertEqual(relocated.vertices["carol"].attrs["mood"], "happy")
        self.assertEqual(relocated.vertices["bob"].attrs["dept"], "eng")
        self.assertIn("frank", relocated.vertices)

    def test_attr_change_relocates_per_key(self):
        base = base_graph()
        target = base.copy()
        target.vertices["alice"] = Vertex(
            "alice", {"role": "root", "level": 3}, Interval(0.0, 10.0)
        )
        p = diff(base, target)
        alt = base.copy()
        # baseline touched a *different* key of the same vertex
        alt.vertices["alice"] = Vertex(
            "alice", {"role": "admin", "level": 5, "nick": "al"},
            Interval(0.0, 10.0),
        )
        result = replay(p, alt)
        self.assertTrue(result.ok, result.conflicts)
        relocated = result.patch.apply(alt)
        self.assertEqual(
            relocated.vertices["alice"].attrs,
            {"role": "root", "level": 5, "nick": "al"},
        )
        # provenance: the relocated op still records the original base value
        op = result.patch.ops[0]
        self.assertEqual(op.delta.before("role"), "admin")
        self.assertEqual(op.delta.after("role"), "root")


class TestStructuredConflicts(unittest.TestCase):
    def test_rename_vs_delete(self):
        base = base_graph()
        target = base.copy()
        v = target.vertices.pop("alice")
        target.vertices["alex"] = Vertex("alex", dict(v.attrs), v.interval)
        for eid, e in list(target.edges.items()):
            src = "alex" if e.src == "alice" else e.src
            dst = "alex" if e.dst == "alice" else e.dst
            target.edges[eid] = Edge(eid, src, dst, dict(e.attrs), e.interval)
        p = diff(base, target, {"alice": "alex"})

        alt = base.copy()
        alt.remove_edge("e1")
        alt.remove_vertex("alice")  # baseline deleted the renamed vertex
        result = replay(p, alt)
        self.assertFalse(result.ok)
        kinds = [c.kind for c in result.conflicts]
        self.assertIn(rb.RENAME_VS_DELETE, kinds)
        conflict = next(c for c in result.conflicts if c.kind == rb.RENAME_VS_DELETE)
        self.assertEqual(conflict.entity, "vertex:alice")
        self.assertEqual(conflict.detail, {"old": "alice", "new": "alex"})

    def test_same_name_different_identity(self):
        base = base_graph()
        target = base.copy()
        v = target.vertices.pop("alice")
        target.vertices["alex"] = Vertex("alex", dict(v.attrs), v.interval)
        for eid, e in list(target.edges.items()):
            src = "alex" if e.src == "alice" else e.src
            dst = "alex" if e.dst == "alice" else e.dst
            target.edges[eid] = Edge(eid, src, dst, dict(e.attrs), e.interval)
        p = diff(base, target, {"alice": "alex"})

        alt = base.copy()
        alt.add_vertex(Vertex("alex", {"role": "stranger"}))  # name taken
        result = replay(p, alt)
        self.assertFalse(result.ok)
        conflict = result.conflicts[0]
        self.assertEqual(conflict.kind, rb.SAME_NAME_DIFFERENT_IDENTITY)
        self.assertEqual(conflict.entity, "vertex:alex")

    def test_attr_value_conflict(self):
        base = base_graph()
        target = base.copy()
        target.vertices["bob"] = Vertex("bob", {"role": "moderator"})
        p = diff(base, target)
        alt = base.copy()
        alt.vertices["bob"] = Vertex("bob", {"role": "owner"})  # conflicting
        result = replay(p, alt)
        self.assertFalse(result.ok)
        conflict = result.conflicts[0]
        self.assertEqual(conflict.kind, rb.ATTR_VALUE)
        self.assertEqual(conflict.detail["key"], "role")
        self.assertEqual(conflict.detail["expected_before"], "user")
        self.assertEqual(conflict.detail["baseline_value"], "owner")

    def test_removed_vs_modified(self):
        base = base_graph()
        target = base.copy()
        target.remove_edge("e2")
        target.remove_vertex("carol")
        p = diff(base, target)
        alt = base.copy()
        alt.vertices["carol"] = Vertex("carol", {"edited": True})
        result = replay(p, alt)
        self.assertFalse(result.ok)
        self.assertEqual(result.conflicts[0].kind, rb.REMOVED_VS_MODIFIED)
        self.assertEqual(result.conflicts[0].entity, "vertex:carol")

    def test_modified_vs_removed(self):
        base = base_graph()
        target = base.copy()
        target.vertices["carol"] = Vertex("carol", {"x": 1})
        p = diff(base, target)
        alt = base.copy()
        alt.remove_edge("e2")
        alt.remove_vertex("carol")
        result = replay(p, alt)
        self.assertFalse(result.ok)
        self.assertEqual(result.conflicts[0].kind, rb.MODIFIED_VS_REMOVED)

    def test_random_replay_equivalence_when_clean(self):
        """When the alternate baseline only adds non-conflicting changes,
        replay must succeed and the relocated result must contain both
        sets of changes."""
        rng = random.Random(555)
        for trial in range(40):
            g1 = random_graph(rng, rng.randint(2, 8), rng.randint(0, 12))
            g2, renames = mutate(rng, g1, tag=f"r{trial}")
            p = diff(g1, g2, renames)
            # alternate baseline: add fresh isolated vertices only (cannot
            # conflict with anything)
            alt = g1.copy()
            extra = f"extra{trial}"
            alt.add_vertex(Vertex(extra, {"tag": trial}))
            result = replay(p, alt)
            self.assertTrue(result.ok, f"trial {trial}: {result.conflicts}")
            relocated = result.patch.apply(alt)
            expected = p.apply(g1).copy()
            expected.add_vertex(Vertex(extra, {"tag": trial}))
            self.assertEqual(relocated, expected, f"trial {trial}")


if __name__ == "__main__":
    unittest.main()
