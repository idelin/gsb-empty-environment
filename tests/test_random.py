"""Randomized differential test: incremental projection vs brute-force
re-projection of every super-edge's source-edge multiset."""

import random
import unittest
from collections import defaultdict
from copy import deepcopy

from hyperproj import (
    AddEdge,
    AddGroup,
    AddVertex,
    Collapse,
    Expand,
    MoveGroup,
    MoveVertex,
    Projection,
    ProjectionError,
)
from hyperproj.brute import brute_projection
from hyperproj.model import Edge

TIMES = (None, 0, 4, 7, 13)


class Oracle:
    """Plain-dict mirror of the projection's primitive state."""

    def __init__(self):
        self.vertices = {}
        self.groups = {}
        self.edges = {}
        self.collapsed = set()

    def descendant_groups(self, gid):
        children = defaultdict(set)
        for g, p in self.groups.items():
            children[p].add(g)
        out, stack = set(), [gid]
        while stack:
            for c in children[stack.pop()]:
                if c not in out:
                    out.add(c)
                    stack.append(c)
        return out

    def apply(self, op):
        if isinstance(op, AddVertex):
            self.vertices[op.vid] = op.group
        elif isinstance(op, AddGroup):
            self.groups[op.gid] = op.parent
        elif isinstance(op, AddEdge):
            self.edges[op.eid] = Edge(op.eid, op.src, op.dst, op.start, op.end)
        elif isinstance(op, MoveVertex):
            self.vertices[op.vid] = op.group
        elif isinstance(op, MoveGroup):
            self.groups[op.gid] = op.parent
        elif isinstance(op, Collapse):
            self.collapsed -= self.descendant_groups(op.gid)
            self.collapsed.add(op.gid)
        elif isinstance(op, Expand):
            self.collapsed.discard(op.gid)


def assert_consistent(tc, proj, st):
    for at in TIMES:
        brute = brute_projection(st.vertices, st.groups, st.edges, st.collapsed, at)
        tc.assertEqual(proj.superedges(at), brute["cross"], f"cross edges @t={at}")
        tc.assertEqual(proj.internal_map(at), brute["internal"], f"internal @t={at}")
    brute = brute_projection(st.vertices, st.groups, st.edges, st.collapsed)
    tc.assertEqual(proj.supernodes(), brute["nodes"])
    tc.assertEqual(proj.visible_map(), brute["visible"])
    for g in st.groups:
        tc.assertEqual(proj.member_count(g), brute["counts"][g], f"count({g})")
    for eid in st.edges:
        u, v = proj.edge_location(eid)
        tc.assertEqual(u, brute["visible"][st.edges[eid].src])
        tc.assertEqual(v, brute["visible"][st.edges[eid].dst])


class TestRandomDifferential(unittest.TestCase):
    def run_random(self, seed, steps):
        rng = random.Random(seed)
        proj = Projection()
        st = Oracle()
        history = []          # oracle states, mirrors proj's undo stack
        snaps = {}            # named oracle snapshots
        counter = [0]

        def fresh(prefix):
            counter[0] += 1
            return f"{prefix}{counter[0]}"

        def rand_vertex():
            return rng.choice(list(st.vertices)) if st.vertices else None

        def rand_group():
            return rng.choice(list(st.groups)) if st.groups else None

        def gen_op():
            r = rng.random()
            if r < 0.16:
                g = rand_group() if rng.random() < 0.5 else None
                return AddVertex(fresh("v"), g)
            if r < 0.26:
                parent = rand_group() if rng.random() < 0.4 else None
                return AddGroup(fresh("g"), parent)
            if r < 0.50:
                if len(st.vertices) < 2:
                    return AddVertex(fresh("v"), None)
                src, dst = rand_vertex(), rand_vertex()
                if rng.random() < 0.5:
                    a = rng.randint(0, 10)
                    return AddEdge(fresh("e"), src, dst, a, a + rng.randint(1, 8))
                return AddEdge(fresh("e"), src, dst)
            if r < 0.62:
                g = rand_group()
                return Collapse(g) if g else AddVertex(fresh("v"), None)
            if r < 0.70:
                g = rand_group()
                return Expand(g) if g else AddVertex(fresh("v"), None)
            if r < 0.84:
                v = rand_vertex()
                if v is None:
                    return AddVertex(fresh("v"), None)
                return MoveVertex(v, rand_group() if rng.random() < 0.7 else None)
            if r < 0.92:
                g = rand_group()
                if g is None:
                    return AddGroup(fresh("g"), None)
                return MoveGroup(g, rand_group() if rng.random() < 0.6 else None)
            # deliberately invalid ops: id conflicts / unknown ids / cycles
            bad = rng.random()
            if bad < 0.4 and st.vertices:
                return AddVertex(rng.choice(list(st.vertices)), None)
            if bad < 0.8:
                return Collapse("no-such-group")
            g = rand_group()
            return MoveGroup(g, g) if g else AddEdge("x", "a", "b")

        for _ in range(steps):
            roll = rng.random()
            if roll < 0.10 and history:
                proj.undo()
                st.vertices, st.groups, st.edges, st.collapsed = history.pop()
            elif roll < 0.14:
                tag = fresh("snap")
                proj.save_snapshot(tag)
                snaps[tag] = deepcopy((st.vertices, st.groups, st.edges, st.collapsed))
            elif roll < 0.18 and snaps:
                tag = rng.choice(list(snaps))
                proj.restore(tag)
                history.append(deepcopy((st.vertices, st.groups, st.edges, st.collapsed)))
                st.vertices, st.groups, st.edges, st.collapsed = deepcopy(snaps[tag])
            elif roll < 0.24:
                # batch with a guaranteed-invalid tail: whole batch must be rejected
                batch = [gen_op(), AddVertex("__dup__", None), AddGroup("__dup__")]
                try:
                    proj.apply(batch)
                    # only possible if __dup__ was somehow free twice: never
                    st.apply(batch[0])
                    st.apply(batch[1])
                    st.apply(batch[2])
                except ProjectionError:
                    pass
            else:
                op = gen_op()
                try:
                    proj.apply([op])
                except ProjectionError:
                    pass
                else:
                    history.append(
                        deepcopy((st.vertices, st.groups, st.edges, st.collapsed))
                    )
                    st.apply(op)
            assert_consistent(self, proj, st)

    def test_random_seed_1(self):
        self.run_random(seed=20260924, steps=250)

    def test_random_seed_2(self):
        self.run_random(seed=42, steps=250)

    def test_random_seed_3(self):
        self.run_random(seed=7, steps=250)


if __name__ == "__main__":
    unittest.main()
