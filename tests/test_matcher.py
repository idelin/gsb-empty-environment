import unittest

from pattern_matcher import (DedupStrategy, Edge, MatchEnumerator,
                             Metrics, PatternEdge, PatternGraph,
                             PatternVertex, StaleVersionError, TargetGraph,
                             Vertex, enumerate_matches)


def make_target(vertices, edges):
    g = TargetGraph()
    g.add_vertices(vertices)
    g.add_edges(edges)
    return g


class TestPredicatesAndTypes(unittest.TestCase):
    def test_type_and_property_predicates(self):
        g = make_target(
            [Vertex("u1", "user", {"age": 30}),
             Vertex("u2", "user", {"age": 15}),
             Vertex("b1", "bot", {"age": 99})],
            [])
        pat = PatternGraph(
            [PatternVertex("p", "user", {"age": ("ge", 18)})], [])
        got = [m.vertex_mapping for m in enumerate_matches(g, pat)]
        self.assertEqual(got, [{"p": "u1"}])

    def test_predicate_ops(self):
        g = make_target([Vertex(f"v{i}", "t", {"k": i}) for i in range(5)], [])
        cases = [
            (("eq", 2), ["v2"]),
            (("ne", 2), ["v0", "v1", "v3", "v4"]),
            (("lt", 2), ["v0", "v1"]),
            (("le", 2), ["v0", "v1", "v2"]),
            (("gt", 2), ["v3", "v4"]),
            (("in", [1, 3]), ["v1", "v3"]),
            (("exists",), ["v0", "v1", "v2", "v3", "v4"]),
        ]
        for spec, want in cases:
            pat = PatternGraph([PatternVertex("p", "t", {"k": spec})], [])
            got = [m.vertex_mapping["p"] for m in enumerate_matches(g, pat)]
            self.assertEqual(got, want, spec)
        pat = PatternGraph([PatternVertex("p", "t", {"nope": ("missing",)})], [])
        self.assertEqual(len(enumerate_matches(g, pat)), 5)
        pat = PatternGraph([PatternVertex("p", "t", {"nope": ("exists",)})], [])
        self.assertEqual(len(enumerate_matches(g, pat)), 0)


class TestInjectivityAndStructure(unittest.TestCase):
    def test_injective_mapping(self):
        # Two pattern vertices may not map to the same target vertex.
        g = make_target([Vertex("a", "t"), Vertex("b", "t")], [])
        pat = PatternGraph([PatternVertex("p", "t"), PatternVertex("q", "t")], [])
        got = sorted(tuple(sorted(m.mapping)) for m in enumerate_matches(g, pat))
        self.assertEqual(got, [(("p", "a"), ("q", "b")),
                               (("p", "b"), ("q", "a"))])
        g1 = make_target([Vertex("a", "t")], [])
        self.assertEqual(enumerate_matches(g1, pat), [])

    def test_isolated_vertices_kept_and_matched(self):
        g = make_target([Vertex("lonely", "t"), Vertex("x", "t")],
                        [Edge("e1", "x", "x")])
        self.assertIn("lonely", g.vertices)  # isolated vertex preserved
        pat = PatternGraph([PatternVertex("p", "t")], [])
        got = sorted(m.vertex_mapping["p"] for m in enumerate_matches(g, pat))
        self.assertEqual(got, ["lonely", "x"])

    def test_negative_edge_three_point_pattern(self):
        # A->B, A->C required; B->C forbidden.
        pat = PatternGraph(
            [PatternVertex("A", "t"), PatternVertex("B", "t"),
             PatternVertex("C", "t")],
            [PatternEdge("e1", "A", "B"),
             PatternEdge("e2", "A", "C"),
             PatternEdge("e3", "B", "C", negative=True)])
        base = [Vertex("a", "t"), Vertex("b", "t"), Vertex("c", "t")]
        ok = make_target(base, [Edge("f1", "a", "b"), Edge("f2", "a", "c")])
        got = sorted(tuple(sorted(m.mapping)) for m in enumerate_matches(ok, pat))
        self.assertEqual(got, [(("A", "a"), ("B", "b"), ("C", "c")),
                               (("A", "a"), ("B", "c"), ("C", "b"))])
        # Target has the extra forbidden edge b->c: match must be excluded.
        bad = make_target(base, [Edge("f1", "a", "b"), Edge("f2", "a", "c"),
                                 Edge("f3", "b", "c")])
        got = sorted(tuple(sorted(m.mapping))
                     for m in enumerate_matches(bad, pat))
        self.assertEqual(got, [(("A", "a"), ("B", "c"), ("C", "b"))])

    def test_parallel_target_edges_dedup_strategies(self):
        g = make_target([Vertex("x", "t"), Vertex("y", "t")],
                        [Edge("e1", "x", "y", "k"), Edge("e2", "x", "y", "k")])
        self.assertEqual(len(g.edges_between("x", "y")), 2)  # multi-edge kept
        pat = PatternGraph([PatternVertex("p", "t"), PatternVertex("q", "t")],
                           [PatternEdge("pe", "p", "q", "k")])
        vm = enumerate_matches(g, pat, dedup=DedupStrategy.VERTEX_MAPPING)
        self.assertEqual(len(vm), 1)  # parallel edges do not duplicate mapping
        ee = enumerate_matches(g, pat, dedup=DedupStrategy.EDGE_EMBEDDING)
        self.assertEqual(len(ee), 2)  # embeddings distinguish witness edges
        self.assertEqual({m.edge_assignment for m in ee},
                         {(("pe", "e1"),), (("pe", "e2"),)})

    def test_symmetric_pattern(self):
        pat = PatternGraph([PatternVertex("p", "t"), PatternVertex("q", "t")],
                           [PatternEdge("e1", "p", "q"),
                            PatternEdge("e2", "q", "p")])
        g = make_target([Vertex("a", "t"), Vertex("b", "t")],
                        [Edge("f1", "a", "b"), Edge("f2", "b", "a")])
        got = sorted(tuple(sorted(m.mapping)) for m in enumerate_matches(g, pat))
        self.assertEqual(got, [(("p", "a"), ("q", "b")),
                               (("p", "b"), ("q", "a"))])

    def test_stable_result_order(self):
        g = make_target([Vertex(c, "t") for c in "cab"], [])
        pat = PatternGraph([PatternVertex("p", "t")], [])
        got = [m.vertex_mapping["p"] for m in enumerate_matches(g, pat)]
        self.assertEqual(got, ["a", "b", "c"])  # sorted by target id


class TestTimeIntervals(unittest.TestCase):
    def test_left_closed_right_open(self):
        g = make_target([Vertex("x"), Vertex("y")],
                        [Edge("e1", "x", "y", valid_from=10, valid_to=20)])
        pat = PatternGraph([PatternVertex("p"), PatternVertex("q")],
                           [PatternEdge("pe", "p", "q")])
        self.assertEqual(len(enumerate_matches(g, pat, at_time=9)), 0)
        self.assertEqual(len(enumerate_matches(g, pat, at_time=10)), 1)  # closed
        self.assertEqual(len(enumerate_matches(g, pat, at_time=19)), 1)
        self.assertEqual(len(enumerate_matches(g, pat, at_time=20)), 0)  # open
        self.assertEqual(len(enumerate_matches(g, pat)), 1)  # no time filter


class TestLimits(unittest.TestCase):
    def test_max_candidates_per_vertex(self):
        g = make_target([Vertex(f"v{i}", "t") for i in range(5)], [])
        pat = PatternGraph([PatternVertex("p", "t")], [])
        got = enumerate_matches(g, pat, max_candidates=2)
        self.assertEqual([m.vertex_mapping["p"] for m in got], ["v0", "v1"])

    def test_max_matches(self):
        g = make_target([Vertex(f"v{i}", "t") for i in range(5)], [])
        pat = PatternGraph([PatternVertex("p", "t")], [])
        got = enumerate_matches(g, pat, max_matches=3)
        self.assertEqual(len(got), 3)


class TestEnumeratorResume(unittest.TestCase):
    def build(self, dedup=DedupStrategy.VERTEX_MAPPING):
        g = make_target([Vertex(c, "t") for c in "abcd"],
                        [Edge("e1", "a", "b"), Edge("e2", "b", "c"),
                         Edge("e3", "c", "d"), Edge("e4", "a", "c")])
        pat = PatternGraph([PatternVertex("p", "t"), PatternVertex("q", "t")],
                           [PatternEdge("pe", "p", "q")])
        return g, pat, dedup

    def test_interrupt_and_resume_same_version(self):
        g, pat, dedup = self.build()
        full = [m.map_id for m in enumerate_matches(g, pat, dedup=dedup)]
        enum = MatchEnumerator(g, pat, dedup=dedup)
        page1 = enum.next_batch(2)
        self.assertEqual(len(page1), 2)
        cursor = enum.cursor
        # "Interrupted" enumeration resumes from the cursor on the same version.
        resumed = MatchEnumerator(g, pat, dedup=dedup, resume_after=cursor)
        rest = list(resumed)
        self.assertTrue(resumed.exhausted)
        self.assertEqual([m.map_id for m in page1 + rest], full)

    def test_resume_edge_embedding_strategy(self):
        g = make_target([Vertex("x", "t"), Vertex("y", "t")],
                        [Edge("e1", "x", "y"), Edge("e2", "x", "y"),
                         Edge("e3", "x", "y")])
        pat = PatternGraph([PatternVertex("p", "t"), PatternVertex("q", "t")],
                           [PatternEdge("pe", "p", "q")])
        dedup = DedupStrategy.EDGE_EMBEDDING
        full = [m.map_id for m in enumerate_matches(g, pat, dedup=dedup)]
        self.assertEqual(len(full), 3)
        enum = MatchEnumerator(g, pat, dedup=dedup)
        page1 = enum.next_batch(1)  # stop mid-mapping (3 embeddings share it)
        resumed = MatchEnumerator(g, pat, dedup=dedup,
                                  resume_after=enum.cursor)
        got = [m.map_id for m in page1 + list(resumed)]
        self.assertEqual(got, full)

    def test_stale_version_rejected(self):
        g, pat, dedup = self.build()
        enum = MatchEnumerator(g, pat, dedup=dedup)
        enum.next_batch(1)
        g.add_edges([Edge("e9", "d", "a")])  # graph version bumps
        with self.assertRaises(StaleVersionError):
            enum.next_batch(1)

    def test_metrics_count_candidates(self):
        g, pat, dedup = self.build()
        metrics = Metrics()
        enumerate_matches(g, pat, metrics=metrics)
        self.assertGreater(metrics.candidates_touched, 0)


if __name__ == "__main__":
    unittest.main()
