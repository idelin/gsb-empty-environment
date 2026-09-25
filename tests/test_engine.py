import random
import unittest

from pattern_matcher import (DedupStrategy, Edge, IncrementalMatchEngine,
                             Metrics, PatternEdge, PatternGraph,
                             PatternVertex, TargetGraph, Vertex,
                             enumerate_matches)


def friendship_pattern(negative=False):
    vertices = [PatternVertex("a", "person"), PatternVertex("b", "person")]
    edges = [PatternEdge("knows", "a", "b", "knows")]
    if negative:
        edges.append(PatternEdge("not_blocked", "b", "a", "blocked",
                                 negative=True))
    return PatternGraph(vertices, edges)


def people_graph():
    g = TargetGraph()
    g.add_vertices([Vertex("ann", "person"), Vertex("bob", "person"),
                    Vertex("cid", "person")])
    return g


class TestIncrementalBasics(unittest.TestCase):
    def test_initial_load_and_add_edge_diff(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows")])
        eng = IncrementalMatchEngine(g, friendship_pattern())
        self.assertEqual(len(eng.matches), 1)
        diff = eng.apply_updates(add_edges=[Edge("e2", "bob", "cid", "knows")])
        self.assertEqual(len(diff.added), 1)
        self.assertEqual(diff.added[0].vertex_mapping, {"a": "bob", "b": "cid"})
        self.assertEqual(diff.removed, ())
        self.assertEqual(len(eng.matches), 2)
        self.assertEqual(diff.version, eng.version)

    def test_remove_edge_revokes(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows")])
        eng = IncrementalMatchEngine(g, friendship_pattern())
        (m0,) = eng.matches
        diff = eng.apply_updates(remove_edge_ids=["e1"])
        self.assertEqual([m.map_id for m in diff.removed], [m0.map_id])
        self.assertEqual(diff.added, ())
        self.assertEqual(eng.matches, ())

    def test_added_edge_can_revoke_via_negative_constraint(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows")])
        eng = IncrementalMatchEngine(g, friendship_pattern(negative=True))
        self.assertEqual(len(eng.matches), 1)
        # Adding the forbidden "blocked" edge ann<-bob revokes the match.
        diff = eng.apply_updates(add_edges=[Edge("e2", "bob", "ann", "blocked")])
        self.assertEqual(len(diff.removed), 1)
        self.assertEqual(diff.added, ())
        self.assertEqual(eng.matches, ())

    def test_removed_edge_can_create_via_negative_constraint(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows"),
                     Edge("e2", "bob", "ann", "blocked")])
        eng = IncrementalMatchEngine(g, friendship_pattern(negative=True))
        self.assertEqual(eng.matches, ())  # blocked while e2 exists
        diff = eng.apply_updates(remove_edge_ids=["e2"])
        self.assertEqual(len(diff.added), 1)
        self.assertEqual(diff.added[0].vertex_mapping,
                         {"a": "ann", "b": "bob"})
        self.assertEqual(diff.removed, ())

    def test_batch_add_and_remove(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows")])
        eng = IncrementalMatchEngine(g, friendship_pattern())
        diff = eng.apply_updates(
            add_edges=[Edge("e2", "cid", "ann", "knows"),
                       Edge("e3", "bob", "cid", "knows")],
            remove_edge_ids=["e1"])
        self.assertEqual(len(diff.removed), 1)
        self.assertEqual(len(diff.added), 2)
        got = sorted(m.map_id for m in eng.matches)
        want = sorted(m.map_id for m in
                      enumerate_matches(g, friendship_pattern()))
        self.assertEqual(got, want)

    def test_mapping_ids_stable_across_updates(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows")])
        eng = IncrementalMatchEngine(g, friendship_pattern())
        (before,) = eng.matches
        # Unrelated edge elsewhere: the existing match keeps its id.
        eng.apply_updates(add_edges=[Edge("e2", "cid", "cid", "knows")])
        ids = {m.map_id: m for m in eng.matches}
        self.assertIn(before.map_id, ids)
        self.assertEqual(ids[before.map_id].mapping, before.mapping)

    def test_parallel_edge_update_keeps_single_vertex_mapping(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows")])
        eng = IncrementalMatchEngine(g, friendship_pattern())
        # Adding a parallel edge must not duplicate the vertex mapping.
        diff = eng.apply_updates(add_edges=[Edge("e2", "ann", "bob", "knows")])
        self.assertEqual(diff.added, ())
        self.assertEqual(len(eng.matches), 1)
        # Removing one of the two parallel edges keeps the mapping alive.
        diff = eng.apply_updates(remove_edge_ids=["e1"])
        self.assertEqual(diff.removed, ())
        self.assertEqual(len(eng.matches), 1)
        diff = eng.apply_updates(remove_edge_ids=["e2"])
        self.assertEqual(len(diff.removed), 1)
        self.assertEqual(eng.matches, ())

    def test_embedding_strategy_diff(self):
        g = people_graph()
        g.add_edges([Edge("e1", "ann", "bob", "knows")])
        eng = IncrementalMatchEngine(g, friendship_pattern(),
                                     dedup=DedupStrategy.EDGE_EMBEDDING)
        diff = eng.apply_updates(add_edges=[Edge("e2", "ann", "bob", "knows")])
        self.assertEqual(len(diff.added), 1)  # new embedding via e2
        self.assertEqual(len(eng.matches), 2)
        diff = eng.apply_updates(remove_edge_ids=["e1"])
        self.assertEqual(len(diff.removed), 1)
        self.assertEqual(len(eng.matches), 1)


class TestExhaustiveRandomComparison(unittest.TestCase):
    """Random graphs/patterns/updates: incremental engine must always agree
    with a from-scratch exhaustive enumeration, while touching fewer
    candidates than a full rescan on localized updates."""

    def random_pattern(self, rng):
        nv = rng.randint(1, 3)
        vids = [f"p{i}" for i in range(nv)]
        vertices = []
        for v in vids:
            preds = {}
            if rng.random() < 0.3:
                preds["k"] = ("ge", rng.randint(0, 1))
            vertices.append(PatternVertex(v, rng.choice([None, "a", "b"]),
                                          preds))
        edges = []
        for i in range(rng.randint(0, 3)):
            edges.append(PatternEdge(
                f"pe{i}", rng.choice(vids), rng.choice(vids),
                type=rng.choice([None, "x", "y"]),
                negative=rng.random() < 0.35))
        return PatternGraph(vertices, edges)

    def random_edge(self, rng, vids, eid):
        return Edge(eid, rng.choice(vids), rng.choice(vids),
                    type=rng.choice(["x", "y"]),
                    props={"w": rng.randint(0, 2)})

    def run_sequence(self, seed, dedup):
        rng = random.Random(seed)
        vids = [f"v{i}" for i in range(5)]
        g = TargetGraph()
        g.add_vertices([Vertex(v, rng.choice(["a", "b"]),
                               {"k": rng.randint(0, 2)}) for v in vids])
        counter = [0]

        def new_id():
            counter[0] += 1
            return f"e{counter[0]}"

        g.add_edges([self.random_edge(rng, vids, new_id())
                     for _ in range(rng.randint(0, 5))])
        pattern = self.random_pattern(rng)
        eng = IncrementalMatchEngine(g, pattern, dedup=dedup)

        def reference():
            m = Metrics()
            out = enumerate_matches(g, pattern, dedup=dedup, metrics=m)
            return out, m

        ref, _ = reference()
        self.assertEqual([m.map_id for m in eng.matches],
                         [m.map_id for m in ref])
        prev_ids = {m.map_id for m in ref}

        strict_savings = 0
        for _step in range(40):
            adds = [self.random_edge(rng, vids, new_id())
                    for _ in range(rng.randint(0, 2))]
            removes = rng.sample(list(g.edges),
                                 k=min(len(g.edges), rng.randint(0, 2)))
            diff = eng.apply_updates(add_edges=adds, remove_edge_ids=removes)
            ref, ref_metrics = reference()
            got = [m.map_id for m in eng.matches]
            want = [m.map_id for m in ref]
            self.assertEqual(got, want,
                             f"seed={seed} dedup={dedup} step={_step}")
            # Diff consistency: applying the diff to the previous state
            # yields exactly the new state.
            self.assertEqual(
                set(want),
                (prev_ids - {m.map_id for m in diff.removed})
                | {m.map_id for m in diff.added})
            prev_ids = set(want)
            # The incremental pass must never do more candidate work than a
            # full rescan, and localized updates should do strictly less.
            inc = diff.metrics.candidates_touched
            full = ref_metrics.candidates_touched
            self.assertLessEqual(inc, full)
            if inc < full:
                strict_savings += 1
            # Revalidation is limited to matches touching changed vertices.
            self.assertLessEqual(diff.metrics.matches_revalidated,
                                 len(eng.matches) + len(diff.removed))
        self.assertGreater(strict_savings, 0,
                           f"no localized-update savings for seed={seed}")

    def test_vertex_mapping_strategy(self):
        for seed in range(6):
            with self.subTest(seed=seed):
                self.run_sequence(1000 + seed, DedupStrategy.VERTEX_MAPPING)

    def test_edge_embedding_strategy(self):
        for seed in range(6):
            with self.subTest(seed=seed):
                self.run_sequence(2000 + seed, DedupStrategy.EDGE_EMBEDDING)


if __name__ == "__main__":
    unittest.main()
