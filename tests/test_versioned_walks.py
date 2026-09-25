import random
import unittest

from versioned_walks import (
    BudgetExhausted,
    NoSuchWalk,
    TrajectoryTooLong,
    VersionedGraph,
)
from versioned_walks.analysis import outgoing_map


def brute_walks(snapshot, source, target, lo, hi):
    """Enumerate every source->target walk with length in [lo, hi)."""
    out = outgoing_map(snapshot)
    found = []

    def dfs(u, path):
        if u == target and lo <= len(path) < hi:
            found.append(tuple(path))
        if len(path) + 1 < hi:
            for e in out.get(u, ()):
                dfs(e.dst, path + [e.eid])

    if source in snapshot.vertices:
        dfs(source, [])
    return found


def make_loop_graph():
    """S -> A, A -> A (self-loop), A -> T."""
    g = VersionedGraph()
    g.add_edge("e_sa", "S", "A")
    g.add_edge("e_loop", "A", "A")
    g.add_edge("e_at", "A", "T")
    return g


class TestLengthZero(unittest.TestCase):
    def test_length_zero_only_when_source_equals_target(self):
        g = VersionedGraph()
        g.add_vertex("S")
        g.add_vertex("T")
        self.assertEqual(g.count("S", "S", 0, 1).count, 1)
        self.assertEqual(g.count("S", "T", 0, 1).count, 0)
        self.assertEqual(g.count("S", "S", 1, 2).count, 0)
        self.assertEqual(g.count("S", "S", 0, 0).count, 0)  # empty interval

    def test_length_zero_walk_is_first_in_lex_order(self):
        g = make_loop_graph()
        g.add_edge("e_ts", "T", "S")  # allow S == T style reuse via new query
        w = g.kth("A", "A", 0, 3, 0)
        self.assertEqual(w.edges, ())
        self.assertEqual(w.vertices, ("A",))


class TestIntervals(unittest.TestCase):
    def test_left_closed_right_open(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "A")
        g.add_edge("e2", "A", "T")
        self.assertEqual(g.count("S", "T", 2, 3).count, 1)   # length 2 only
        self.assertEqual(g.count("S", "T", 1, 2).count, 0)
        self.assertEqual(g.count("S", "T", 2, 4).count, 1)
        self.assertEqual(g.count("S", "T", 3, 4).count, 0)
        self.assertEqual(g.count("S", "T", 0, 2).count, 0)


class TestInfiniteMode(unittest.TestCase):
    def test_self_loop_gives_infinite_with_evidence(self):
        g = make_loop_graph()
        r = g.count("S", "T", 0, None)
        self.assertTrue(r.infinite)
        self.assertIsNone(r.count)
        self.assertEqual(r.evidence.cycle, ("e_loop",))
        self.assertEqual(r.evidence.cycle_vertex, "A")
        self.assertEqual(r.evidence.reach_path, ("e_sa",))
        self.assertEqual(r.evidence.exit_path, ("e_at",))

    def test_irrelevant_cycle_does_not_count(self):
        # Cycle unreachable from S.
        g = VersionedGraph()
        g.add_edge("e_st", "S", "T")
        g.add_edge("e_xy", "X", "Y")
        g.add_edge("e_yx", "Y", "X")
        r = g.count("S", "T", 0, None)
        self.assertFalse(r.infinite)
        self.assertEqual(r.count, 1)

    def test_cycle_that_cannot_reach_target_does_not_count(self):
        g = VersionedGraph()
        g.add_edge("e_st", "S", "T")
        g.add_edge("e_sc", "S", "C")
        g.add_edge("e_cc", "C", "C")  # reachable from S, dead-end for T
        r = g.count("S", "T", 0, None)
        self.assertFalse(r.infinite)
        self.assertEqual(r.count, 1)

    def test_cycle_not_reachable_from_source_does_not_count(self):
        g = VersionedGraph()
        g.add_edge("e_st", "S", "T")
        g.add_edge("e_cc", "C", "C")
        g.add_edge("e_ct", "C", "T")  # can reach T, but not reachable from S
        r = g.count("S", "T", 0, None)
        self.assertFalse(r.infinite)
        self.assertEqual(r.count, 1)

    def test_finite_unbounded_count_is_exact(self):
        g = make_loop_graph()
        g.remove_edge("e_loop")
        r = g.count("S", "T", 0, None)
        self.assertFalse(r.infinite)
        self.assertEqual(r.count, 1)  # only S -> A -> T


class TestBatchDeleteAndVersions(unittest.TestCase):
    def test_batch_delete_revokes_infinite_old_version_kept(self):
        g = VersionedGraph()
        g.add_edge("e_sa", "S", "A")
        g.add_edge("e_loop1", "A", "A")
        g.add_edge("e_loop2", "A", "A")  # multi-edge self-loops
        g.add_edge("e_at", "A", "T")
        v1 = g.version

        r1 = g.count("S", "T", 0, None)
        self.assertTrue(r1.infinite)
        # two loop edges: walks of length L >= 2 number 2^(L-2)
        self.assertEqual(g.count("S", "T", 0, 6).count, 1 + 2 + 4 + 8)

        v2 = g.remove_edges(["e_loop1", "e_loop2"])
        self.assertEqual(v2, v1 + 1)  # single commit for the batch

        r2 = g.count("S", "T", 0, None)
        self.assertFalse(r2.infinite)
        self.assertEqual(r2.count, 1)

        # Old version is preserved byte-for-byte.
        r1_again = g.count("S", "T", 0, None, version=v1)
        self.assertTrue(r1_again.infinite)
        self.assertEqual(r1_again.evidence.cycle, r1.evidence.cycle)
        self.assertEqual(g.count("S", "T", 0, 6, version=v1).count, 15)
        self.assertEqual(g.count("S", "T", 0, 6).count, 1)

    def test_edge_add_invalidates_previous_blocks(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "T")
        v1 = g.version
        self.assertEqual(g.count("S", "T", 0, 5).count, 1)
        g.add_edge("e2", "S", "T")
        self.assertEqual(g.count("S", "T", 0, 5).count, 2)
        self.assertEqual(g.count("S", "T", 0, 5, version=v1).count, 1)


class TestStructure(unittest.TestCase):
    def test_isolated_vertices_preserved(self):
        g = make_loop_graph()
        g.add_vertex("ISO")
        self.assertIn("ISO", g.snapshot().vertices)
        r = g.count("S", "T", 0, 5)
        self.assertNotIn("ISO", r.reachable_slice)

    def test_reachable_slice(self):
        g = VersionedGraph()
        g.add_edge("e_sa", "S", "A")
        g.add_edge("e_at", "A", "T")
        g.add_edge("e_ab", "A", "B")   # dead end, cannot reach T
        g.add_edge("e_ca", "C", "A")   # unreachable from S
        g.add_vertex("ISO")
        r = g.count("S", "T", 0, 5)
        self.assertEqual(r.reachable_slice, frozenset({"S", "A", "T"}))

    def test_multi_edges_counted_and_ordered_separately(self):
        g = VersionedGraph()
        g.add_edge("e1", "S", "T")
        g.add_edge("e2", "S", "T")
        self.assertEqual(g.count("S", "T", 1, 2).count, 2)
        self.assertEqual(g.kth("S", "T", 0, 2, 0).edges, ("e1",))
        self.assertEqual(g.kth("S", "T", 0, 2, 1).edges, ("e2",))

    def test_string_ids(self):
        g = VersionedGraph()
        g.add_edge("edge-alpha", "start node", "end node")
        self.assertEqual(g.count("start node", "end node", 0, 2).count, 1)


class TestModulusAndBigInts(unittest.TestCase):
    def test_big_integer_count(self):
        g = VersionedGraph()
        g.add_edge("e_sa", "S", "A")
        g.add_edge("e_loop1", "A", "A")
        g.add_edge("e_loop2", "A", "A")
        g.add_edge("e_at", "A", "T")
        # walks of length L >= 2: 2^(L-2); sum over [0, 102) = 2^100 - 1
        exact = g.count("S", "T", 0, 102).count
        self.assertEqual(exact, 2**100 - 1)

    def test_modulus_matches_exact(self):
        g = make_loop_graph()
        exact = g.count("S", "T", 0, 10**6).count
        for m in (2, 7, 1_000_000_007):
            self.assertEqual(g.count("S", "T", 0, 10**6, modulus=m).count,
                             exact % m)

    def test_modulus_not_mixed_with_selection(self):
        g = make_loop_graph()
        with self.assertRaises(ValueError):
            g.kth("S", "T", 0, 10, 0, modulus=5)


class TestKthAndRank(unittest.TestCase):
    def test_known_order(self):
        g = make_loop_graph()
        # e_loop < e_at, so more loops sort earlier; k-th walk has k loops.
        for k in range(5):
            w = g.kth("S", "T", 0, 10, k)
            self.assertEqual(w.edges, ("e_sa",) + ("e_loop",) * k + ("e_at",))
            self.assertEqual(w.length, k + 2)

    def test_rank_inverts_kth(self):
        g = make_loop_graph()
        for k in range(8):
            w = g.kth("S", "T", 0, 10, k)
            self.assertEqual(g.rank("S", "T", 0, 10, w.edges), k)

    def test_no_such_walk(self):
        g = make_loop_graph()
        total = g.count("S", "T", 0, 5).count
        with self.assertRaises(NoSuchWalk):
            g.kth("S", "T", 0, 5, total)

    def test_kth_rejects_infinite_unbounded_interval(self):
        g = make_loop_graph()
        with self.assertRaises(ValueError):
            g.kth("S", "T", 0, None, 0)

    def test_kth_unbounded_finite_interval_resolves(self):
        g = make_loop_graph()
        g.remove_edge("e_loop")
        w = g.kth("S", "T", 0, None, 0)
        self.assertEqual(w.edges, ("e_sa", "e_at"))


class TestBudgetAndLimits(unittest.TestCase):
    def test_budget_exhaustion_is_not_no_path(self):
        g = make_loop_graph()
        with self.assertRaises(BudgetExhausted):
            g.kth("S", "T", 0, 10, 3, budget=0)
        with self.assertRaises(BudgetExhausted):
            g.kth("S", "T", 0, 10, 3, budget=1)
        # The same query with enough budget succeeds: paths do exist.
        self.assertEqual(g.kth("S", "T", 0, 10, 3, budget=100).length, 5)

    def test_trajectory_length_limit(self):
        g = make_loop_graph()
        # k=50 needs a walk of length 52, beyond max_length=10.
        with self.assertRaises(TrajectoryTooLong):
            g.kth("S", "T", 0, 100, 50, max_length=10)
        # Within the limit it materializes fine.
        w = g.kth("S", "T", 0, 100, 8, max_length=10)
        self.assertEqual(w.length, 10)


class TestAgainstBruteForce(unittest.TestCase):
    def test_random_small_graphs(self):
        rng = random.Random(20260925)
        for trial in range(40):
            g = VersionedGraph()
            verts = [f"v{i}" for i in range(rng.randint(2, 5))]
            for v in verts:
                g.add_vertex(v)
            for i in range(rng.randint(0, 8)):
                g.add_edge(f"e{i}", rng.choice(verts), rng.choice(verts))
            s, t = rng.choice(verts), rng.choice(verts)
            hi = rng.randint(1, 7)
            snap = g.snapshot()
            walks = brute_walks(snap, s, t, 0, hi)

            # Counts for every sub-interval match enumeration.
            for lo in range(hi):
                for h in range(lo + 1, hi + 1):
                    expected = sum(1 for w in walks if lo <= len(w) < h)
                    got = g.count(s, t, lo, h).count
                    self.assertEqual(got, expected,
                                     f"trial {trial} [{lo}, {h})")

            # k-th matches sorted enumeration; rank inverts it.
            ordered = sorted(walks)
            for k, w in enumerate(ordered):
                self.assertEqual(g.kth(s, t, 0, hi, k).edges, w,
                                 f"trial {trial} k={k}")
                self.assertEqual(g.rank(s, t, 0, hi, w), k)
            with self.assertRaises(NoSuchWalk):
                g.kth(s, t, 0, hi, len(ordered))

            # Unbounded mode agrees with acyclicity of the relevant slice.
            r = g.count(s, t, 0, None)
            if r.infinite:
                self.assertIsNotNone(r.evidence.cycle)
            else:
                all_walks = brute_walks(snap, s, t, 0, len(verts) + 1)
                self.assertEqual(r.count, len(all_walks))


if __name__ == "__main__":
    unittest.main()
