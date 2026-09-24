import random
import unittest

from walkgraph import KthStatus, VersionedGraph


# ---------------------------------------------------------------------------
# Brute-force reference implementations (small graphs only)
# ---------------------------------------------------------------------------

def brute_count(adj, s, t, lo, hi):
    count = 0

    def rec(v, d):
        nonlocal count
        if lo <= d < hi and v == t:
            count += 1
        if d + 1 < hi:
            for _, w in adj.get(v, ()):
                rec(w, d + 1)

    rec(s, 0)
    return count


def brute_walks(adj, s, t, length):
    """All s->t walks of exactly `length`, as sorted signature tuples."""
    out = []
    if length == 0:
        return [(s,)] if s == t else []

    def rec(v, d, sig):
        if d == length:
            if v == t:
                out.append(tuple(sig))
            return
        for eid, w in adj.get(v, ()):
            sig.extend((eid, w))
            rec(w, d + 1, sig)
            del sig[-2:]

    rec(s, 0, [s])
    return sorted(out)


def signature_of(result):
    sig = []
    for i, v in enumerate(result.vertices):
        sig.append(v)
        if i < len(result.edges):
            sig.append(result.edges[i])
    return tuple(sig)


def make_random_graph(rng, n_vertices=5, n_edges=8):
    g = VersionedGraph()
    verts = [f"v{i}" for i in range(n_vertices)]
    with g.batch():
        for v in verts:
            g.add_vertex(v)
        for i in range(n_edges):
            g.add_edge(f"e{i}", rng.choice(verts), rng.choice(verts))
    return g, verts


class CountingTest(unittest.TestCase):
    def test_interval_left_closed_right_open(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "A")
        g.add_edge("e2", "A", "T")
        view = g.at()
        self.assertEqual(view.count_walks("S", "T", 0, 2).count, 0)
        self.assertEqual(view.count_walks("S", "T", 2, 3).count, 1)
        self.assertEqual(view.count_walks("S", "T", 0, 3).count, 1)
        self.assertEqual(view.count_walks("S", "T", 3, 10).count, 0)
        self.assertEqual(view.count_walks("S", "T", 2, 2).count, 0)
        self.assertEqual(view.count_walks("S", "T", 0, 0).count, 0)

    def test_length_zero_only_when_s_equals_t(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "T")
        view = g.at()
        self.assertEqual(view.count_walks("S", "S", 0, 1).count, 1)
        self.assertEqual(view.count_walks("S", "T", 0, 1).count, 0)
        self.assertEqual(view.count_walks("T", "T", 0, 1).count, 1)
        res = view.kth_walk("S", "S", 0, 0)
        self.assertEqual(res.status, KthStatus.OK)
        self.assertEqual(res.vertices, ("S",))
        self.assertEqual(res.edges, ())
        self.assertEqual(view.kth_walk("S", "S", 0, 1).status,
                         KthStatus.K_OUT_OF_RANGE)
        self.assertEqual(view.kth_walk("S", "T", 0, 0).status,
                         KthStatus.NO_PATH)

    def test_counts_match_bruteforce_on_random_graphs(self):
        rng = random.Random(20260924)
        for _ in range(30):
            g, verts = make_random_graph(rng)
            view = g.at()
            adj = view.adjacency()
            for _ in range(6):
                s, t = rng.choice(verts), rng.choice(verts)
                lo = rng.randrange(0, 4)
                hi = lo + rng.randrange(0, 4)
                expected = brute_count(adj, s, t, lo, hi)
                res = view.count_walks(s, t, lo, hi)
                self.assertFalse(res.infinite)
                self.assertEqual(res.count, expected, (s, t, lo, hi))

    def test_modulus_matches_exact_count(self):
        rng = random.Random(7)
        for _ in range(15):
            g, verts = make_random_graph(rng, n_vertices=4, n_edges=7)
            view = g.at()
            s, t = rng.choice(verts), rng.choice(verts)
            lo, hi = 0, 5
            exact = view.count_walks(s, t, lo, hi).count
            for mod in (1, 2, 7, 10**9 + 7):
                self.assertEqual(view.count_walks(s, t, lo, hi, mod=mod).count,
                                 exact % mod)

    def test_big_integer_counts(self):
        # 40 parallel edges S->T: exactly 40 walks of length 1, and
        # a chain of 30 doubled edges gives 2**30 walks of length 30.
        g = VersionedGraph()
        with g.batch():
            for i in range(40):
                g.add_edge(f"p{i}", "S", "T")
            g.add_edge("c0", "A", "B0")
            for i in range(30):
                g.add_edge(f"d{i}a", f"B{i}", f"B{i + 1}")
                g.add_edge(f"d{i}b", f"B{i}", f"B{i + 1}")
        view = g.at()
        self.assertEqual(view.count_walks("S", "T", 0, 10).count, 40)
        self.assertEqual(view.count_walks("B0", "B30", 30, 31).count, 2 ** 30)
        self.assertEqual(
            view.count_walks("B0", "B30", 30, 31, mod=1000).count,
            (2 ** 30) % 1000)

    def test_reachable_slice_attached(self):
        g = VersionedGraph()
        with g.batch():
            g.add_edge("e1", "S", "A")
            g.add_edge("e2", "A", "T")
            g.add_edge("e3", "S", "X")   # X cannot reach T
            g.add_vertex("ISO")
        view = g.at()
        res = view.count_walks("S", "T", 0, 5)
        self.assertEqual(res.reachable_slice, frozenset({"S", "A", "T"}))

    def test_unknown_vertex_raises(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "T")
        view = g.at()
        with self.assertRaises(KeyError):
            view.count_walks("S", "nope", 0, 1)
        with self.assertRaises(KeyError):
            view.kth_walk("nope", "T", 1, 0)


class KthWalkTest(unittest.TestCase):
    def test_kth_reverse_rank_matches_bruteforce(self):
        rng = random.Random(99)
        for _ in range(20):
            g, verts = make_random_graph(rng, n_vertices=4, n_edges=6)
            view = g.at()
            adj = view.adjacency()
            s, t = rng.choice(verts), rng.choice(verts)
            for length in range(0, 5):
                walks = brute_walks(adj, s, t, length)
                for k, sig in enumerate(walks):
                    res = view.kth_walk(s, t, length, k)
                    self.assertEqual(res.status, KthStatus.OK, (s, t, length, k))
                    self.assertEqual(signature_of(res), sig)
                    self.assertEqual(res.total, len(walks))
                res = view.kth_walk(s, t, length, len(walks))
                expected = (KthStatus.NO_PATH if not walks
                            else KthStatus.K_OUT_OF_RANGE)
                self.assertEqual(res.status, expected)

    def test_multi_edges_are_distinct_walks(self):
        g = VersionedGraph()
        g.add_edge("e1", "A", "B")
        g.add_edge("e2", "A", "B")
        view = g.at()
        self.assertEqual(view.count_walks("A", "B", 1, 2).count, 2)
        r0 = view.kth_walk("A", "B", 1, 0)
        r1 = view.kth_walk("A", "B", 1, 1)
        self.assertEqual((r0.edges, r0.vertices), (("e1",), ("A", "B")))
        self.assertEqual((r1.edges, r1.vertices), (("e2",), ("A", "B")))
        self.assertEqual(view.kth_walk("A", "B", 1, 2).status,
                         KthStatus.K_OUT_OF_RANGE)

    def test_budget_exceeded_is_not_no_path(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "A")
        g.add_edge("e2", "A", "T")
        g.add_edge("e3", "S", "X")
        view = g.at()
        res = view.kth_walk("S", "T", 100, 0, max_length=8)
        self.assertEqual(res.status, KthStatus.BUDGET_EXCEEDED)
        self.assertIsNone(res.vertices)
        # A genuine absence of path must still report NO_PATH.
        self.assertEqual(view.kth_walk("X", "T", 3, 0).status,
                         KthStatus.NO_PATH)
        # And a feasible query within budget still works.
        self.assertEqual(view.kth_walk("S", "T", 2, 0, max_length=8).status,
                         KthStatus.OK)

    def test_selection_never_uses_modded_counts(self):
        # 3 walks of length 2 S->T; count mod 2 == 1 must not confuse kth.
        g = VersionedGraph()
        with g.batch():
            g.add_edge("e1", "S", "A")
            g.add_edge("e2", "A", "T")
            g.add_edge("e3", "S", "B")
            g.add_edge("e4", "B", "T")
            g.add_edge("e5", "S", "C")
            g.add_edge("e6", "C", "T")
        view = g.at()
        self.assertEqual(view.count_walks("S", "T", 0, 3, mod=2).count, 1)
        for k in range(3):
            res = view.kth_walk("S", "T", 2, k)
            self.assertEqual(res.status, KthStatus.OK)
            self.assertEqual(res.total, 3)
        self.assertEqual(view.kth_walk("S", "T", 2, 0).edges, ("e1", "e2"))
        self.assertEqual(view.kth_walk("S", "T", 2, 1).edges, ("e3", "e4"))
        self.assertEqual(view.kth_walk("S", "T", 2, 2).edges, ("e5", "e6"))


class InfiniteTest(unittest.TestCase):
    def build(self):
        g = VersionedGraph()
        with g.batch():
            g.add_edge("e_sa", "S", "A")
            g.add_edge("e_aa", "A", "A")   # self-loop on the S->T route
            g.add_edge("e_at", "A", "T")
            g.add_edge("e_sx", "S", "X")
            g.add_edge("e_xy", "X", "Y")
            g.add_edge("e_yx", "Y", "X")   # unrelated cycle, cannot reach T
        return g

    def test_cycle_evidence(self):
        view = self.build().at()
        ev = view.infinite_walks("S", "T")
        self.assertIsNotNone(ev)
        self.assertEqual(ev.cycle, ("A",))
        self.assertEqual(ev.cycle_edges, ("e_aa",))
        self.assertEqual(ev.prefix[0], "S")
        self.assertEqual(ev.prefix[-1], "A")
        self.assertEqual(ev.suffix[0], "A")
        self.assertEqual(ev.suffix[-1], "T")
        res = view.count_walks("S", "T", 0, None)
        self.assertTrue(res.infinite)
        self.assertIsNone(res.count)
        self.assertEqual(res.evidence.cycle, ev.cycle)

    def test_unrelated_cycle_does_not_trigger(self):
        g = self.build()
        g.remove_edge("e_aa")
        view = g.at()
        # X<->Y cycle is reachable from S but cannot reach T: no infinity.
        self.assertIsNone(view.infinite_walks("S", "T"))
        res = view.count_walks("S", "T", 0, None)
        self.assertFalse(res.infinite)
        self.assertEqual(res.count, 1)  # only S->A->T
        # The unrelated cycle DOES make S->Y infinite.
        self.assertIsNotNone(view.infinite_walks("S", "Y"))

    def test_batch_delete_revokes_infinite_old_version_kept(self):
        g = self.build()
        g.add_edge("e_aa2", "A", "A")  # second self-loop (multi-edge)
        v_inf = g.version
        self.assertTrue(g.at(v_inf).count_walks("S", "T", 0, None).infinite)
        with g.batch():
            g.remove_edge("e_aa")
            g.remove_edge("e_aa2")
        v_fin = g.version
        self.assertEqual(v_fin, v_inf + 1)  # one commit for the batch
        self.assertFalse(g.at(v_fin).count_walks("S", "T", 0, None).infinite)
        self.assertEqual(g.at(v_fin).count_walks("S", "T", 0, None).count, 1)
        # Old version is retained and still reports infinity.
        old = g.at(v_inf).count_walks("S", "T", 0, None)
        self.assertTrue(old.infinite)
        self.assertEqual(old.evidence.cycle, ("A",))

    def test_finite_total_when_acyclic_slice(self):
        g = VersionedGraph()
        with g.batch():
            g.add_edge("e1", "S", "A")
            g.add_edge("e2", "A", "T")
            g.add_edge("e3", "S", "T")
        view = g.at()
        res = view.count_walks("S", "T", 0, None)
        self.assertFalse(res.infinite)
        self.assertEqual(res.count, 2)
        self.assertEqual(view.count_walks("S", "T", 2, None).count, 1)


class VersioningTest(unittest.TestCase):
    def test_old_versions_stay_queryable(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "T")
        v1 = g.version
        g.add_edge("e2", "S", "T")
        v2 = g.version
        g.remove_edge("e1")
        v3 = g.version
        self.assertEqual(g.at(v1).count_walks("S", "T", 0, 2).count, 1)
        self.assertEqual(g.at(v2).count_walks("S", "T", 0, 2).count, 2)
        self.assertEqual(g.at(v3).count_walks("S", "T", 0, 2).count, 1)
        self.assertEqual(g.at().version, v3)
        with self.assertRaises(KeyError):
            g.at(v3 + 1)

    def test_isolated_vertices_preserved(self):
        g = VersionedGraph()
        g.add_vertex("ISO")
        g.add_edge("e1", "S", "T")
        view = g.at()
        self.assertIn("ISO", view.vertices)
        self.assertEqual(view.count_walks("ISO", "ISO", 0, 1).count, 1)
        self.assertEqual(view.kth_walk("ISO", "ISO", 0, 0).status,
                         KthStatus.OK)
        self.assertEqual(view.count_walks("ISO", "T", 0, 5).count, 0)

    def test_string_ids_and_duplicate_edge_rejected(self):
        g = VersionedGraph()
        g.add_edge("edge-1", "vertex-s", "vertex-t")
        with self.assertRaises(ValueError):
            g.add_edge("edge-1", "a", "b")
        with self.assertRaises(KeyError):
            g.remove_edge("missing")


if __name__ == "__main__":
    unittest.main()
