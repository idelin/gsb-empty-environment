import random
import unittest

from graphmatch import (AttrPred, DedupStrategy, Edge, IncrementalMatcher,
                        PatternEdge, PatternGraph, PatternVertex, TargetGraph,
                        Vertex, VersionMismatchError, brute_force_match_ids)


def typed_graph():
    g = TargetGraph()
    g.add_vertex(Vertex("a1", "person", {"age": 30}))
    g.add_vertex(Vertex("a2", "person", {"age": 40}))
    g.add_vertex(Vertex("a3", "person", {"age": 15}))
    g.add_vertex(Vertex("c1", "company"))
    return g


class BasicMatchingTest(unittest.TestCase):
    def test_type_and_attr_predicate_with_injectivity(self):
        pat = PatternGraph(
            [PatternVertex("p", "person", [AttrPred("age", "ge", 18)]),
             PatternVertex("q", "person", [AttrPred("age", "ge", 18)])])
        m = IncrementalMatcher(typed_graph(), pat)
        # a3 is a minor; p and q may not share a target vertex.
        self.assertEqual(m.results(), ["p=a1;q=a2", "p=a2;q=a1"])

    def test_no_injective_mapping_possible(self):
        g = TargetGraph()
        g.add_vertex(Vertex("only", "person"))
        pat = PatternGraph([PatternVertex("p", "person"),
                            PatternVertex("q", "person")])
        self.assertEqual(IncrementalMatcher(g, pat).results(), [])

    def test_isolated_vertices_kept_and_matched(self):
        g = TargetGraph()
        g.add_vertex(Vertex("lonely", "person"))
        g.add_vertex(Vertex("other", "robot"))
        pat = PatternGraph([PatternVertex("p", "person")])
        self.assertEqual(IncrementalMatcher(g, pat).results(), ["p=lonely"])

    def test_multi_edges_do_not_duplicate_mappings(self):
        g = TargetGraph()
        g.add_vertex(Vertex("u"))
        g.add_vertex(Vertex("v"))
        g.add_edge(Edge("e1", "u", "v", "x"))
        g.add_edge(Edge("e2", "u", "v", "x"))  # parallel edge
        g.add_edge(Edge("e3", "u", "v", "y"))
        pat = PatternGraph([PatternVertex("p"), PatternVertex("q")],
                           [PatternEdge("pe", "p", "q", "x")])
        self.assertEqual(IncrementalMatcher(g, pat).results(), ["p=u;q=v"])

    def test_time_interval_left_closed_right_open(self):
        g = TargetGraph()
        g.add_vertex(Vertex("u"))
        g.add_vertex(Vertex("v"))
        g.add_edge(Edge("e", "u", "v", "x", valid_from=2, valid_to=5))
        pat = PatternGraph([PatternVertex("p"), PatternVertex("q")],
                           [PatternEdge("pe", "p", "q", "x")])
        for t, expected in [(1, 0), (2, 1), (4, 1), (5, 0)]:
            m = IncrementalMatcher(g, pat, time=t)
            self.assertEqual(len(m.results()), expected, f"t={t}")
            self.assertEqual(len(brute_force_match_ids(g, pat, time=t)),
                             expected, f"brute force t={t}")

    def test_stable_order_and_ids(self):
        m1 = IncrementalMatcher(typed_graph(),
                                PatternGraph([PatternVertex("p", "person")]))
        m2 = IncrementalMatcher(typed_graph(),
                                PatternGraph([PatternVertex("p", "person")]))
        self.assertEqual(m1.results(), m2.results())
        self.assertEqual(m1.results(), sorted(m1.results()))


class NegativeEdgeTest(unittest.TestCase):
    def build(self):
        g = TargetGraph()
        for v in ("A", "B", "C"):
            g.add_vertex(Vertex(v, "n"))
        g.add_edge(Edge("e1", "A", "B", "x"))
        pat = PatternGraph(
            [PatternVertex("a", "n"), PatternVertex("b", "n"),
             PatternVertex("c", "n")],
            [PatternEdge("pos", "a", "b", "x"),
             PatternEdge("neg", "a", "c", "y", exists=False)])
        return g, pat

    def test_three_point_negative_constraint(self):
        g, pat = self.build()
        m = IncrementalMatcher(g, pat)
        self.assertEqual(m.results(), ["a=A;b=B;c=C"])
        # Adding the forbidden edge revokes the match.
        delta = m.apply_update(added_edges=[Edge("e2", "A", "C", "y")])
        self.assertEqual(delta.added, ())
        self.assertEqual(delta.removed, ("a=A;b=B;c=C",))
        self.assertEqual(m.results(), [])
        # Removing it again restores the match.
        delta = m.apply_update(removed_edge_ids=["e2"])
        self.assertEqual(delta.added, ("a=A;b=B;c=C",))
        self.assertEqual(delta.removed, ())
        self.assertEqual(m.results(), ["a=A;b=B;c=C"])

    def test_unrelated_negative_edge_does_not_revoke(self):
        g, pat = self.build()
        m = IncrementalMatcher(g, pat)
        delta = m.apply_update(added_edges=[Edge("e2", "B", "C", "y")])
        self.assertEqual(delta.removed, ())
        self.assertEqual(m.results(), ["a=A;b=B;c=C"])


class DedupStrategyTest(unittest.TestCase):
    def test_automorphism_dedup_on_symmetric_pattern(self):
        g = TargetGraph()
        g.add_vertex(Vertex("a1", "A"))
        g.add_vertex(Vertex("a2", "A"))
        g.add_edge(Edge("e1", "a1", "a2", "x"))
        g.add_edge(Edge("e2", "a2", "a1", "x"))
        pat = PatternGraph(
            [PatternVertex("p1", "A"), PatternVertex("p2", "A")],
            [PatternEdge("f", "p1", "p2", "x"),
             PatternEdge("b", "p2", "p1", "x")])
        m = IncrementalMatcher(g, pat)
        self.assertEqual(m.results(DedupStrategy.NONE),
                         ["p1=a1;p2=a2", "p1=a2;p2=a1"])
        self.assertEqual(m.results(DedupStrategy.AUTOMORPHISM),
                         ["p1=a1;p2=a2"])

    def test_asymmetric_pattern_unaffected(self):
        g = TargetGraph()
        g.add_vertex(Vertex("a1", "A"))
        g.add_vertex(Vertex("a2", "B"))
        pat = PatternGraph([PatternVertex("p1", "A"), PatternVertex("p2", "B")])
        m = IncrementalMatcher(g, pat)
        self.assertEqual(m.results(DedupStrategy.NONE),
                         m.results(DedupStrategy.AUTOMORPHISM))


class EnumerationTest(unittest.TestCase):
    def build_matcher(self):
        g = TargetGraph()
        for i in range(4):
            g.add_vertex(Vertex(f"p{i}", "person"))
        pat = PatternGraph([PatternVertex("x", "person"),
                            PatternVertex("y", "person")])
        return IncrementalMatcher(g, pat)  # 4*3 = 12 mappings

    def test_paged_enumeration_and_resume(self):
        m = self.build_matcher()
        seen, cursor = [], None
        while True:
            page, cursor = m.enumerate(limit=5, cursor=cursor)
            seen.extend(page)
            if cursor is None:
                break
        self.assertEqual(seen, m.results())
        self.assertEqual(len(seen), 12)

    def test_cursor_version_mismatch_raises(self):
        m = self.build_matcher()
        page, cursor = m.enumerate(limit=3)
        self.assertIsNotNone(cursor)
        m.apply_update(added_edges=[Edge("e", "p0", "p1", "x")])
        with self.assertRaises(VersionMismatchError):
            m.enumerate(limit=3, cursor=cursor)
        # Fresh enumeration on the new version works.
        page, _ = m.enumerate(limit=3)
        self.assertEqual(len(page), 3)


class CandidateLimitTest(unittest.TestCase):
    def test_truncation_flag(self):
        g = TargetGraph()
        for i in range(6):
            g.add_vertex(Vertex(f"v{i}"))
        pat = PatternGraph([PatternVertex("p"), PatternVertex("q"),
                            PatternVertex("r")])
        m = IncrementalMatcher(g, pat, max_candidates=2)
        self.assertTrue(m.last_op.truncated)
        m2 = IncrementalMatcher(g, pat)
        self.assertFalse(m2.last_op.truncated)
        self.assertEqual(len(m2.results()), 6 * 5 * 4)


class IncrementalVsBruteForceTest(unittest.TestCase):
    """Random batch-update sequences checked against exhaustive matching."""

    def random_graph(self, rng, n=8, types=None):
        g = TargetGraph()
        for i in range(n):
            g.add_vertex(Vertex(f"v{i}",
                                types[i] if types else rng.choice(["A", "B"]),
                                {"score": rng.randint(0, 3)}))
        return g

    def random_edge(self, rng, g, eid):
        vids = sorted(g.vertices)
        vf = rng.choice([0, 0, 0, 1, 2])
        vt = rng.choice([None, None, vf + 1, vf + 2, 5])
        return Edge(eid, rng.choice(vids), rng.choice(vids),
                    rng.choice(["x", "y"]), {"w": rng.randint(0, 2)}, vf, vt)

    def run_sequence(self, rng, pattern, n_steps=40, time=1, n=8,
                     max_init_edges=6, types=None, seed=None):
        g = self.random_graph(rng, n=n, types=types)
        if seed is not None:
            seed(g)
        for j in range(rng.randint(0, max_init_edges)):
            e = self.random_edge(rng, g, f"init{j}")
            g.add_edge(e)
        m = IncrementalMatcher(g, pattern, time=time)
        prev = brute_force_match_ids(g, pattern, time=time)
        self.assertEqual(set(m.results()), prev)
        inc_touches, full_touches = 0, 0
        edge_counter = 0
        for step in range(n_steps):
            adds = [self.random_edge(rng, g, f"s{step}a{edge_counter + k}")
                    for k in range(rng.randint(0, 3))]
            edge_counter += len(adds)
            rems = rng.sample(sorted(g.edges),
                              min(len(g.edges), rng.randint(0, 3)))
            delta = m.apply_update(added_edges=adds, removed_edge_ids=rems)
            self.assertEqual(delta.version, m.version)
            now = brute_force_match_ids(g, pattern, time=time)
            self.assertEqual(set(m.results()), now, f"step {step}")
            self.assertEqual(set(delta.added), now - prev, f"step {step} added")
            self.assertEqual(set(delta.removed), prev - now,
                             f"step {step} removed")
            prev = now
            inc_touches += m.last_op.candidates_touched
            full = IncrementalMatcher(g, pattern, time=time)
            full_touches += full.last_op.candidates_touched
        return inc_touches, full_touches

    def test_random_patterns(self):
        rng = random.Random(20260924)
        total_inc = total_full = 0
        for _ in range(6):
            pvs = [PatternVertex(f"p{i}", rng.choice([None, "A", "B"]),
                                 (AttrPred("score", "ge", rng.randint(0, 2)),)
                                 if rng.random() < 0.3 else ())
                   for i in range(rng.randint(2, 4))]
            edges = []
            for j in range(rng.randint(1, 4)):
                s, d = rng.choice(pvs), rng.choice(pvs)
                edges.append(PatternEdge(
                    f"e{j}", s.id, d.id, rng.choice([None, "x", "y"]),
                    exists=rng.random() > 0.35))
            total = self.run_sequence(rng, PatternGraph(pvs, edges),
                                      n_steps=25)
            total_inc += total[0]
            total_full += total[1]
        # Incremental maintenance must not rescan everything per update.
        self.assertLess(total_inc, total_full)

    def test_symmetric_pattern_with_multiedges_and_negative_edges(self):
        rng = random.Random(7)
        pattern = PatternGraph(
            [PatternVertex("p1", "A"), PatternVertex("p2", "A"),
             PatternVertex("p3")],
            [PatternEdge("f", "p1", "p2", "x"),
             PatternEdge("b", "p2", "p1", "x"),
             PatternEdge("neg", "p1", "p3", "y", exists=False)])

        def seed(g):
            # Guaranteed mutual x pairs (symmetric structure) among the
            # six type-A vertices, plus a negative-constraint y edge.
            for k, (u, v) in enumerate([("v0", "v1"), ("v2", "v3"),
                                        ("v4", "v5"), ("v1", "v2")]):
                g.add_edge(Edge(f"mf{k}", u, v, "x"))
                g.add_edge(Edge(f"mb{k}", v, u, "x"))
            g.add_edge(Edge("ny0", "v0", "v7", "y"))

        types = ["A"] * 6 + ["B"] * 4
        inc, full = self.run_sequence(rng, pattern, n_steps=40, n=10,
                                      max_init_edges=10, types=types,
                                      seed=seed)
        self.assertLess(inc, full)

    def test_localized_update_touches_few_candidates(self):
        rng = random.Random(99)
        g = self.random_graph(rng, n=14)
        for j in range(30):
            g.add_edge(self.random_edge(rng, g, f"e{j}"))
        pattern = PatternGraph(
            [PatternVertex("p1"), PatternVertex("p2"), PatternVertex("p3")],
            [PatternEdge("e1", "p1", "p2", "x"),
             PatternEdge("e2", "p2", "p3", "x")])
        m = IncrementalMatcher(g, pattern, time=1)
        full_touches = m.last_op.candidates_touched
        m.apply_update(added_edges=[Edge("new", "v0", "v1", "x")])
        self.assertLess(m.last_op.candidates_touched, full_touches)
        self.assertEqual(set(m.results()),
                         brute_force_match_ids(g, pattern, time=1))


if __name__ == "__main__":
    unittest.main()
