"""Randomized property tests: composition equivalence and inverse recovery
on random graphs, plus randomized replay and collision rejection."""
import random
import unittest

import graphdiff.hashing as hashing
from graphdiff import (
    Graph, Interval, diff, compose, replay, BaseMismatchError, RenameNode,
)

NAMES = [f"n{i}" for i in range(10)]
KEYS = ["a", "b", "c"]
VALS = [0, 1, 2, "x", "y", None, True]


def random_attrs(rng):
    return {k: rng.choice(VALS) for k in rng.sample(KEYS, rng.randint(0, 2))}


def random_interval(rng):
    if rng.random() < 0.7:
        return Interval()
    start = rng.randint(0, 5)
    return Interval(start, start + rng.randint(1, 5))


def random_graph(rng, n_vertices=6, n_edges=8):
    g = Graph()
    for vid in rng.sample(NAMES, n_vertices):
        g.add_vertex(vid, random_attrs(rng), random_interval(rng))
    for i in range(n_edges):
        src, dst = rng.choice(list(g.vertices)), rng.choice(list(g.vertices))
        g.add_edge(f"e{i}", src, dst, random_attrs(rng), random_interval(rng))
    return g


def random_mutation(rng, g, rename_map, reserved=()):
    """Apply one random mutation to g (in place); records renames."""
    free = [n for n in NAMES if n not in g.vertices]
    edge_free = [f"e{i}" for i in range(100) if f"e{i}" not in g.edges]
    choices = ["add_node", "add_edge"]
    if g.vertices:
        choices += ["del_node", "rename", "set_node_attr", "set_interval"]
    if g.edges:
        choices += ["del_edge", "set_edge_attr", "reconnect"]
    kind = rng.choice(choices)

    if kind == "add_node" and free:
        g.add_vertex(rng.choice(free), random_attrs(rng), random_interval(rng))
    elif kind == "del_node":
        vid = rng.choice(list(g.vertices))
        for eid in [i for i, e in g.edges.items()
                    if e.src == vid or e.dst == vid]:
            del g.edges[eid]
        del g.vertices[vid]
    elif kind == "rename":
        old = rng.choice(list(g.vertices))
        # targets must be free and must not be names occupied in the base
        # version (the diff's rename-map validation rejects those)
        pool = [n for n in free
                if n not in rename_map.values() and n not in reserved]
        if pool:
            new = rng.choice(pool)
            RenameNode(old, new).apply(g)
            followed = False
            for k, v in list(rename_map.items()):
                if v == old:
                    rename_map[k] = new
                    followed = True
            if not followed:
                rename_map[old] = new
    elif kind == "add_edge" and edge_free and g.vertices:
        g.add_edge(rng.choice(edge_free), rng.choice(list(g.vertices)),
                   rng.choice(list(g.vertices)), random_attrs(rng),
                   random_interval(rng))
    elif kind == "del_edge":
        del g.edges[rng.choice(list(g.edges))]
    elif kind == "set_node_attr" and g.vertices:
        v = g.vertices[rng.choice(list(g.vertices))]
        v.attrs[rng.choice(KEYS)] = rng.choice(VALS)
    elif kind == "set_edge_attr":
        e = g.edges[rng.choice(list(g.edges))]
        e.attrs[rng.choice(KEYS)] = rng.choice(VALS)
    elif kind == "reconnect":
        e = g.edges[rng.choice(list(g.edges))]
        e.src, e.dst = rng.choice(list(g.vertices)), rng.choice(list(g.vertices))
    elif kind == "set_interval":
        g.vertices[rng.choice(list(g.vertices))].interval = random_interval(rng)


def random_version(rng, base, steps):
    g = base.copy()
    rename_map = {}
    reserved = set(base.vertices)
    for _ in range(steps):
        random_mutation(rng, g, rename_map, reserved)
    # normalize: drop no-op renames and renames of nodes not in the base
    return g, {k: v for k, v in rename_map.items()
               if k != v and k in base.vertices}


class TestRandomizedProperties(unittest.TestCase):
    def test_apply_and_inverse_recovery(self):
        rng = random.Random(20260925)
        for trial in range(150):
            a = random_graph(rng)
            b, rm = random_version(rng, a, rng.randint(1, 8))
            p = diff(a, b, rm)
            with self.subTest(trial=trial):
                self.assertEqual(p.apply(a), b)
                self.assertEqual(p.inverse().apply(b), a)

    def test_compose_equivalence(self):
        rng = random.Random(112233)
        for trial in range(150):
            a = random_graph(rng)
            b, rm1 = random_version(rng, a, rng.randint(1, 6))
            c, rm2 = random_version(rng, b, rng.randint(1, 6))
            p1, p2 = diff(a, b, rm1), diff(b, c, rm2)
            pc = compose(p1, p2)
            with self.subTest(trial=trial):
                self.assertEqual(pc.apply(a), c)          # composition equivalence
                self.assertEqual(pc.inverse().apply(c), a)  # inverse recovery
                # multiplicities preserved
                for x in c.vertices:
                    for y in c.vertices:
                        self.assertEqual(pc.apply(a).multiplicity(x, y),
                                         c.multiplicity(x, y))

    def test_compose_associativity(self):
        rng = random.Random(777)
        for trial in range(60):
            a = random_graph(rng)
            b, rm1 = random_version(rng, a, rng.randint(1, 5))
            c, rm2 = random_version(rng, b, rng.randint(1, 5))
            d, rm3 = random_version(rng, c, rng.randint(1, 5))
            p1, p2, p3 = diff(a, b, rm1), diff(b, c, rm2), diff(c, d, rm3)
            left = compose(compose(p1, p2), p3)
            right = compose(p1, compose(p2, p3))
            with self.subTest(trial=trial):
                self.assertEqual(left.apply(a), d)
                self.assertEqual(right.apply(a), d)
                self.assertEqual(left.apply(a), right.apply(a))

    def test_random_wrong_baselines_rejected(self):
        rng = random.Random(42)
        for trial in range(80):
            a = random_graph(rng)
            b, rm = random_version(rng, a, rng.randint(1, 6))
            p = diff(a, b, rm)
            wrong, _ = random_version(rng, a, rng.randint(1, 4))
            if wrong == a:
                continue
            before = wrong.canonical()
            with self.subTest(trial=trial):
                with self.assertRaises(BaseMismatchError):
                    p.apply(wrong)
                self.assertEqual(wrong.canonical(), before)  # unchanged

    def test_random_collision_baselines_rejected_by_content(self):
        rng = random.Random(9001)
        real_digest = hashing.digest
        hashing.digest = lambda content: "COLLIDE"
        try:
            for trial in range(60):
                a = random_graph(rng)
                b, rm = random_version(rng, a, rng.randint(1, 6))
                p = diff(a, b, rm)
                wrong, _ = random_version(rng, a, rng.randint(1, 4))
                if wrong == a:
                    continue
                before = wrong.canonical()
                with self.subTest(trial=trial):
                    with self.assertRaises(BaseMismatchError):
                        p.apply(wrong)
                    self.assertEqual(wrong.canonical(), before)
                    self.assertEqual(p.apply(a), b)  # true base still works
        finally:
            hashing.digest = real_digest

    def test_random_replay_relocation(self):
        """Other branch renames a random node; patch ops must relocate and
        the merged result must equal the target with the same rename applied."""
        rng = random.Random(31337)
        for trial in range(100):
            a = random_graph(rng)
            b, rm = random_version(rng, a, rng.randint(1, 6))
            p = diff(a, b, rm)
            # pick a node untouched by the patch to rename in other branch
            touched = set()
            for op in p.ops:
                for attr in ("old", "new", "vid", "eid", "src", "dst"):
                    v = getattr(op, attr, None)
                    if isinstance(v, str):
                        touched.add(v)
            candidates = [v for v in a.vertices
                          if v not in touched and v not in rm]
            if not candidates:
                continue
            victim = rng.choice(candidates)
            new_name = f"R{trial}"
            other = a.copy()
            RenameNode(victim, new_name).apply(other)
            expected = b.copy()
            RenameNode(victim, new_name).apply(expected)
            res = replay(p, other, {victim: new_name})
            with self.subTest(trial=trial):
                self.assertTrue(res.ok, res.conflicts)
                self.assertEqual(res.graph, expected)


if __name__ == "__main__":
    unittest.main()
