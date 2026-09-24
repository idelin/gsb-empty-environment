import random
import unittest

from hyperfold import (
    NEG_INF,
    POS_INF,
    AddEdge,
    AddGroup,
    AddVertex,
    Graph,
    HierarchyCycleError,
    IdConflictError,
    Move,
    View,
)


def build_basic():
    """A,B in G; C in H; D and isolated Z free; multiedge e1/e5."""
    g = Graph()
    g.apply([
        AddVertex("A"), AddVertex("B"), AddVertex("C"),
        AddVertex("D"), AddVertex("Z"),
        AddGroup("G"), AddGroup("H"),
        Move("A", "G"), Move("B", "G"), Move("C", "H"),
        AddEdge("ab", "A", "B"),
        AddEdge("e1", "A", "C"),
        AddEdge("e2", "B", "C"),
        AddEdge("e3", "C", "D"),
        AddEdge("e4", "D", "A"),
        AddEdge("e5", "A", "C"),
    ])
    return g


def sorted_hedges(view):
    return {k: sorted(v) for k, v in view.hedges.items()}


def oracle_hedges(g, collapsed, t):
    """Independent reference implementation used by the diff tests."""
    def rep(v):
        result = v
        anc = g.parent[v]
        while anc is not None:
            if anc in collapsed:
                result = anc
            anc = g.parent[anc]
        return result

    out = {}
    for eid, e in g.edges.items():
        if not (e.start <= t < e.end):
            continue
        a, b = rep(e.src), rep(e.dst)
        if a != b:
            out.setdefault((a, b), []).append(eid)
    return {k: sorted(v) for k, v in out.items()}


def oracle_member_counts(g):
    counts = {gid: 0 for gid in g.groups}
    for v in g.vertices:
        anc = g.parent[v]
        while anc is not None:
            counts[anc] += 1
            anc = g.parent[anc]
    return counts


class AcceptanceTests(unittest.TestCase):
    def test_internal_edge_not_counted_as_external(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        self.assertNotIn(("G", "G"), v.hedges)
        all_sources = [e for ids in v.hedges.values() for e in ids]
        self.assertNotIn("ab", all_sources)

    def test_merged_hyperedge_keeps_both_sources(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        self.assertEqual(sorted(v.hyperedge_sources("G", "C")), ["e1", "e2", "e5"])
        self.assertEqual(v.locate("e2"), ("G", "C"))

    def test_expand_then_collapse_conserves_statistics(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        before = sorted_hedges(v)
        v.expand("G")
        self.assertIn(("A", "B"), v.hedges)
        v.collapse("G")
        self.assertEqual(sorted_hedges(v), before)

    def test_isolated_vertices_and_multiedges_preserved(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        self.assertIn("Z", v.supernodes())
        self.assertEqual(sorted(v.hyperedge_sources("G", "C")).count("e1"), 1)
        self.assertEqual(sorted(v.hyperedge_sources("G", "C")).count("e5"), 1)

    def test_time_interval_left_closed_right_open(self):
        g = Graph()
        g.apply([
            AddVertex("A"), AddVertex("B"),
            AddEdge("w", "A", "B", start=1.0, end=3.0),
        ])
        v = View(g, time=0.5)
        self.assertEqual(v.hedges, {})
        v.set_time(1.0)
        self.assertEqual(v.hedges, {("A", "B"): ["w"]})
        v.set_time(2.999)
        self.assertEqual(v.hedges, {("A", "B"): ["w"]})
        v.set_time(3.0)
        self.assertEqual(v.hedges, {})

    def test_batch_rejected_on_id_conflict(self):
        g = build_basic()
        with self.assertRaises(IdConflictError):
            g.apply([AddVertex("W"), AddGroup("W")])
        self.assertNotIn("W", g.vertices)
        self.assertNotIn("W", g.groups)
        with self.assertRaises(IdConflictError):
            g.apply([AddVertex("G")])
        with self.assertRaises(IdConflictError):
            g.apply([AddEdge("e1", "A", "B")])

    def test_batch_rejected_on_hierarchy_cycle(self):
        g = build_basic()
        g.apply([AddGroup("G2", parent="G")])
        parent_before = dict(g.parent)
        with self.assertRaises(HierarchyCycleError):
            g.apply([AddVertex("NEW"), Move("G", "G2")])
        self.assertNotIn("NEW", g.vertices)
        self.assertEqual(g.parent, parent_before)
        with self.assertRaises(HierarchyCycleError):
            g.apply([Move("G", "G")])

    def test_move_atomically_revises_counts_edges_and_reverse_index(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        g.move("C", "G")
        self.assertEqual(g.member_count["G"], 3)
        self.assertEqual(g.member_count["H"], 0)
        self.assertNotIn(("G", "H"), v.hedges)
        self.assertEqual(v.locate("e1"), None)
        self.assertEqual(v.locate("e3"), ("G", "D"))
        self.assertEqual(v.locate("e4"), ("D", "G"))
        for key, ids in v.hedges.items():
            for eid in ids:
                self.assertEqual(v.edge_loc[eid], key)
        g.undo()
        self.assertEqual(g.member_count["G"], 2)
        self.assertEqual(g.member_count["H"], 1)
        self.assertEqual(sorted(v.hyperedge_sources("G", "C")), ["e1", "e2", "e5"])

    def test_move_touches_only_incident_edges(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        v.reindexed = 0
        g.move("D", "H")
        self.assertEqual(v.reindexed, len(g.incident["D"]))
        v.reindexed = 0
        g.move("Z", "H")
        self.assertEqual(v.reindexed, 0)

    def test_snapshots_and_restore(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        snap = v.snapshot()
        v.collapse("H")
        v.expand("G")
        self.assertNotEqual(sorted_hedges(v), {k: sorted(x) for k, x in snap.hedges.items()})
        v.restore(snap)
        self.assertEqual(v.collapsed, {"G"})
        self.assertEqual(sorted_hedges(v), {k: sorted(x) for k, x in snap.hedges.items()})
        self.assertIs(v.history[-1], snap)

    def test_restore_after_graph_mutation_reprojects(self):
        g = build_basic()
        v = View(g)
        v.collapse("G")
        snap = v.snapshot()
        g.move("C", "G")
        v.restore(snap)
        self.assertEqual(v.collapsed, {"G"})
        self.assertLess(snap.graph_version, g._version)
        self.assertEqual(sorted_hedges(v), oracle_hedges(g, {"G"}, 0.0))
        self.assertEqual(v.locate("e1"), None)

    def test_multiple_views_are_independent(self):
        g = build_basic()
        v1 = View(g)
        v2 = View(g)
        v1.collapse("G")
        v2.collapse("H")
        g.move("D", "G")
        self.assertEqual(v1.collapsed, {"G"})
        self.assertEqual(v2.collapsed, {"H"})
        self.assertEqual(v1.locate("e3"), ("C", "G"))
        self.assertEqual(v2.locate("e3"), ("H", "D"))
        self.assertEqual(sorted_hedges(v1), oracle_hedges(g, {"G"}, 0.0))
        self.assertEqual(sorted_hedges(v2), oracle_hedges(g, {"H"}, 0.0))

    def test_nested_groups_outermost_collapsed_wins(self):
        g = Graph()
        g.apply([
            AddVertex("P"), AddVertex("Q"),
            AddGroup("I", parent=None), AddGroup("O", parent=None),
            Move("P", "I"), Move("I", "O"), Move("Q", "O"),
            AddEdge("pq", "P", "Q"),
        ])
        v = View(g)
        v.collapse("O")
        v.collapse("I")
        self.assertEqual(v.rep("P"), "O")
        self.assertEqual(v.supernodes(), {"O"})
        self.assertEqual(v.hedges, {})
        v.expand("O")
        self.assertEqual(v.rep("P"), "I")
        self.assertEqual(v.rep("Q"), "Q")
        self.assertEqual(v.hedges, {("I", "Q"): ["pq"]})
        self.assertEqual(g.member_count["O"], 2)
        self.assertEqual(g.member_count["I"], 1)


class RandomizedDiffTest(unittest.TestCase):
    def test_random_ops_match_reprojection(self):
        rng = random.Random(20260924)
        g = Graph()
        verts = [f"v{i}" for i in range(12)]
        groups = [f"g{i}" for i in range(5)]
        ops = [AddVertex(v) for v in verts]
        for i, gid in enumerate(groups):
            ops.append(AddGroup(gid, rng.choice([None] + groups[:i])))
        ops += [Move(v, rng.choice([None] + groups)) for v in verts]
        intervals = [(NEG_INF, POS_INF), (0.0, 5.0), (2.0, 8.0), (5.0, 10.0)]
        for i in range(30):
            s, e = intervals[rng.randrange(len(intervals))]
            ops.append(AddEdge(f"e{i}", rng.choice(verts), rng.choice(verts), s, e))
        g.apply(ops)

        views = [View(g, time=3.0), View(g, time=7.0)]
        snaps = {id(v): [] for v in views}

        def valid_parents(node):
            out = [None]
            for gid in groups:
                if gid == node:
                    continue
                anc, ok = g.parent[gid], True
                while anc is not None:
                    if anc == node:
                        ok = False
                        break
                    anc = g.parent[anc]
                if ok:
                    out.append(gid)
            return out

        def check(view):
            self.assertEqual(sorted_hedges(view), oracle_hedges(g, view.collapsed, view.time))
            self.assertEqual(g.member_count, oracle_member_counts(g))
            for key, ids in view.hedges.items():
                for eid in ids:
                    self.assertEqual(view.edge_loc[eid], key)
            self.assertEqual(set(view.edge_loc), {e for ids in view.hedges.values() for e in ids})

        for step in range(1500):
            r = rng.random()
            view = rng.choice(views)
            if r < 0.20:
                view.collapse(rng.choice(groups))
            elif r < 0.35:
                if view.collapsed:
                    view.expand(rng.choice(sorted(view.collapsed)))
            elif r < 0.60:
                node = rng.choice(verts + groups)
                g.move(node, rng.choice(valid_parents(node)))
            elif r < 0.70:
                g.undo()
            elif r < 0.80:
                view.undo()
            elif r < 0.86:
                snaps[id(view)].append(view.snapshot())
            elif r < 0.92:
                pool = snaps[id(view)]
                if pool:
                    view.restore(rng.choice(pool))
            elif r < 0.96:
                gid = rng.choice(groups)
                anc = g.parent[gid]
                if anc is not None:
                    before = dict(g.parent)
                    with self.assertRaises(HierarchyCycleError):
                        g.apply([Move(anc, gid)])
                    self.assertEqual(g.parent, before)
            else:
                with self.assertRaises(IdConflictError):
                    g.apply([AddVertex(rng.choice(verts + groups))])
            if step % 20 == 0:
                check(views[0])
                check(views[1])
        check(views[0])
        check(views[1])


if __name__ == "__main__":
    unittest.main(verbosity=2)
