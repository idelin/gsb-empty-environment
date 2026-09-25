import random
import unittest
from fractions import Fraction

from booldag import CycleError, Graph, IntervalSet, ivl, oracle, point


class TestIntervalSemantics(unittest.TestCase):
    def test_touching_intervals_merge_into_connected_component(self):
        a = IntervalSet([ivl(0, 1)])                 # [0,1]
        b = IntervalSet([ivl(1, 2, False, True)])    # (1,2]
        u = a.union(b)
        self.assertTrue(u.is_connected())
        self.assertEqual(u, IntervalSet([ivl(0, 2)]))
        self.assertTrue(a.intersect(b).is_empty())   # [0,1] ∩ (1,2] = ∅

    def test_open_touching_intervals_do_not_merge(self):
        a = IntervalSet([ivl(0, 1, True, False)])    # [0,1)
        b = IntervalSet([ivl(1, 2, False, True)])    # (1,2]
        u = a.union(b)
        self.assertFalse(u.is_connected())
        self.assertEqual(len(u.components()), 2)
        self.assertFalse(u.contains(1))

    def test_difference_endpoint_closedness(self):
        whole = IntervalSet([ivl(0, 2)])
        self.assertEqual(
            whole.subtract(IntervalSet([ivl(1, 2, False, True)])),
            IntervalSet([ivl(0, 1)]),
        )  # [0,2] \ (1,2] = [0,1]
        left = IntervalSet([ivl(0, 1)])
        right = IntervalSet([ivl(1, 2)])
        self.assertEqual(
            left.subtract(right), IntervalSet([ivl(0, 1, True, False)])
        )  # [0,1] \ [1,2] = [0,1)
        self.assertFalse(left.subtract(right).contains(1))

    def test_isolated_point_vs_empty(self):
        pt = IntervalSet([ivl(0, 1)]).intersect(IntervalSet([ivl(1, 2)]))
        self.assertEqual(pt, IntervalSet([point(1)]))
        self.assertEqual(pt.isolated_points(), (Fraction(1),))
        self.assertTrue(pt.contains(1))
        self.assertFalse(pt.is_empty())
        empty = IntervalSet([ivl(0, 1, False, False)]).intersect(
            IntervalSet([point(1)])
        )
        self.assertTrue(empty.is_empty())
        self.assertNotEqual(pt, empty)

    def test_degenerate_open_interval_rejected(self):
        with self.assertRaises(ValueError):
            ivl(1, 1, False, True)
        with self.assertRaises(ValueError):
            ivl(2, 1)

    def test_unbounded_and_complement(self):
        s = IntervalSet([ivl(None, 0, True, False)])  # (-inf, 0)
        c = s.complement()                              # [0, +inf)
        self.assertTrue(c.contains(0))
        self.assertFalse(c.contains(Fraction(-1, 2)))
        self.assertEqual(IntervalSet().complement(), IntervalSet([ivl(None, None)]))
        self.assertTrue(IntervalSet([ivl(None, None)]).complement().is_empty())

    def test_rational_coordinates_exact(self):
        s = IntervalSet([ivl(Fraction(1, 3), Fraction(2, 3))])
        self.assertTrue(s.contains(Fraction(1, 2)))
        self.assertFalse(s.contains(Fraction(1, 3) - Fraction(1, 10**18)))
        with self.assertRaises(TypeError):
            s.contains(0.5)  # floats forbidden: no epsilon guessing


class TestDagIncremental(unittest.TestCase):
    def build(self):
        g = Graph()
        a = g.leaf("a")
        b = g.leaf("b")
        c = g.leaf("c")
        u = g.node("u", "union", ["a", "b"])
        d = g.node("d", "diff", ["u", "c"])
        iso = g.node("iso", "union", ["c"])  # not downstream of a or b
        return g, a, b, c, u, d, iso

    def test_only_affected_cone_reevaluated(self):
        g, a, b, c, u, d, iso = self.build()
        a.set(ivl(0, 2))
        b.set(ivl(1, 3))
        c.set(ivl(2, 4, False, True))
        self.assertEqual(d.value, IntervalSet([ivl(0, 2)]))
        counts = {n.name: n.eval_count for n in (a, b, c, u, d, iso)}
        a.add(ivl(10, 11))
        # a's cone: a, u, d re-evaluate once; b, c, iso untouched.
        self.assertEqual(a.eval_count, counts["a"] + 1)
        self.assertEqual(u.eval_count, counts["u"] + 1)
        self.assertEqual(d.eval_count, counts["d"] + 1)
        self.assertEqual(b.eval_count, counts["b"])
        self.assertEqual(c.eval_count, counts["c"])
        self.assertEqual(iso.eval_count, counts["iso"])

    def test_two_path_retraction(self):
        g = Graph()
        leaf = g.leaf("src")
        na = g.node("na", "union", ["src"])
        nb = g.node("nb", "union", ["src"])
        top = g.node("top", "union", ["na", "nb"])
        leaf.set(ivl(0, 1))
        self.assertEqual(top.value, IntervalSet([ivl(0, 1)]))
        self.assertEqual(g.prove(top, Fraction(1, 2)), frozenset({"src"}))
        # one retraction, even though src reaches top through two paths
        leaf.clear()
        self.assertEqual(top.value, IntervalSet())
        self.assertEqual(top.explain(), ())
        self.assertIsNone(g.prove(top, Fraction(1, 2)))

    def test_duplicate_sources_do_not_inflate_proof(self):
        g = Graph()
        x = g.leaf("x")
        y = g.leaf("y")
        u = g.node("u", "union", ["x", "y"])
        x.set(ivl(0, 1))
        y.set(ivl(0, 1))  # fully redundant coverage
        proof = g.prove(u, Fraction(1, 2))
        self.assertEqual(len(proof), 1)  # minimal: either leaf alone suffices
        # same leaf twice as parents: dedup, still a single-source proof
        g2 = Graph()
        z = g2.leaf("z")
        uu = g2.node("uu", "union", ["z", "z"])
        z.set(ivl(0, 1))
        self.assertEqual(g2.prove(uu, 0), frozenset({"z"}))

    def test_intersection_proof_needs_both_sides(self):
        g = Graph()
        x = g.leaf("x")
        y = g.leaf("y")
        i = g.node("i", "intersect", ["x", "y"])
        x.set(ivl(0, 2))
        y.set(ivl(1, 3))
        self.assertEqual(i.value, IntervalSet([ivl(1, 2)]))
        self.assertEqual(g.prove(i, Fraction(3, 2)), frozenset({"x", "y"}))

    def test_unknown_empty_point_distinct(self):
        g = Graph()
        u_leaf = g.leaf("unk")          # unknown until set
        e_leaf = g.leaf("emp")
        e_leaf.clear()                  # defined empty
        p_leaf = g.leaf("pt")
        p_leaf.set(point(5))            # isolated point
        self.assertTrue(g.nodes["unk"].is_unknown())
        self.assertTrue(g.nodes["emp"].is_empty())
        self.assertFalse(g.nodes["emp"].is_unknown())
        self.assertEqual(g.nodes["pt"].value.isolated_points(), (Fraction(5),))
        # unknown propagates through ops and recovers
        u = g.node("u", "union", ["unk", "pt"])
        self.assertTrue(u.is_unknown())
        g.nodes["unk"].set(ivl(0, 1))
        self.assertEqual(u.value, IntervalSet([ivl(0, 1), point(5)]))
        g.nodes["unk"].set_unknown()
        self.assertTrue(u.is_unknown())


class TestTransactionsAndSnapshots(unittest.TestCase):
    def test_batch_single_propagation(self):
        g = Graph()
        a, b = g.leaf("a"), g.leaf("b")
        u = g.node("u", "union", ["a", "b"])
        before = u.eval_count
        with g.transaction():
            a.set(ivl(0, 1))
            b.set(ivl(2, 3))
        self.assertEqual(u.eval_count, before + 1)  # one re-evaluation, not two
        self.assertEqual(u.value, IntervalSet([ivl(0, 1), ivl(2, 3)]))

    def test_explicit_rollback(self):
        g = Graph()
        a = g.leaf("a")
        a.set(ivl(0, 1))
        u = g.node("u", "union", ["a"])
        with g.transaction() as tx:
            a.set(ivl(5, 6))
            tx.rollback()
        self.assertEqual(a.state, IntervalSet([ivl(0, 1)]))
        self.assertEqual(u.value, IntervalSet([ivl(0, 1)]))
        self.assertEqual(oracle.check_node(g, u), 5)

    def test_exception_rolls_back(self):
        g = Graph()
        a = g.leaf("a")
        a.set(ivl(0, 1))
        with self.assertRaises(RuntimeError):
            with g.transaction():
                a.set(ivl(9, 9))
                raise RuntimeError("boom")
        self.assertEqual(a.state, IntervalSet([ivl(0, 1)]))

    def test_snapshot_isolated_from_later_edits(self):
        g = Graph()
        a = g.leaf("a")
        u = g.node("u", "union", ["a"])
        a.set(ivl(0, 1))
        snap = g.snapshot()
        with g.transaction():
            a.set(ivl(10, 20))
        self.assertEqual(u.value, IntervalSet([ivl(10, 20)]))
        self.assertEqual(snap.value("u"), IntervalSet([ivl(0, 1)]))
        self.assertEqual(snap.prove("u", Fraction(1, 2)), frozenset({"a"}))
        self.assertIsNone(snap.prove("u", 15))
        # rollback restores, snapshot still frozen
        with g.transaction() as tx:
            a.clear()
            tx.rollback()
        self.assertEqual(snap.value("u"), IntervalSet([ivl(0, 1)]))


class TestStructure(unittest.TestCase):
    def test_cycle_rejected(self):
        g = Graph()
        a = g.leaf("a")
        n1 = g.node("n1", "union", ["a"])
        n2 = g.node("n2", "union", ["n1"])
        with self.assertRaises(CycleError):
            g.add_parent(n1, n2)   # n1 <- n2 closes a loop
        with self.assertRaises(CycleError):
            g.add_parent(n1, n1)   # self loop
        # legal edge still works
        b = g.leaf("b")
        g.add_parent(n2, b)
        b.set(ivl(3, 4))
        a.set(ivl(0, 1))
        self.assertEqual(n2.value, IntervalSet([ivl(0, 1), ivl(3, 4)]))

    def test_remove_leaf_reevaluates_dependents(self):
        g = Graph()
        a, b = g.leaf("a"), g.leaf("b")
        u = g.node("u", "union", ["a", "b"])
        a.set(ivl(0, 1))
        b.set(ivl(2, 3))
        g.remove_leaf("b")
        self.assertEqual(u.value, IntervalSet([ivl(0, 1)]))
        self.assertEqual(g.leaves_of(u), frozenset({"a"}))

    def test_leaf_correction(self):
        g = Graph()
        a = g.leaf("a")
        a.set(ivl(0, 10))
        a.subtract(ivl(2, 3, False, True))  # correct: punch out (2,3]
        self.assertEqual(
            a.state, IntervalSet([ivl(0, 2), ivl(3, 10, False, True)])
        )


class TestOracleCrossCheck(unittest.TestCase):
    def test_required_example(self):
        g = Graph()
        a, b = g.leaf("a"), g.leaf("b")
        u = g.node("u", "union", ["a", "b"])
        i = g.node("i", "intersect", ["a", "b"])
        d = g.node("d", "diff", ["a", "b"])
        a.set(ivl(0, 1))
        b.set(ivl(1, 2, False, True))
        self.assertTrue(u.is_connected())
        self.assertTrue(i.is_empty())
        for n in (u, i, d):
            oracle.check_node(g, n)

    def test_random_dag_against_oracle(self):
        rng = random.Random(20260925)
        for trial in range(30):
            g = Graph()
            leaves = []
            for k in range(4):
                lf = g.leaf(f"L{k}")
                if rng.random() < 0.15:
                    pass  # stay unknown
                else:
                    ivs = []
                    for _ in range(rng.randint(0, 2)):
                        lo = rng.randint(-4, 4)
                        hi = lo + rng.randint(0, 3)
                        ivs.append(
                            ivl(lo, hi, rng.random() < 0.5, rng.random() < 0.5)
                            if hi > lo
                            else point(lo)
                        )
                    lf.set(*ivs)
                leaves.append(lf)
            u1 = g.node("u1", "union", ["L0", "L1"])
            i1 = g.node("i1", "intersect", ["L1", "L2"])
            d1 = g.node("d1", "diff", ["u1", "i1"])
            top = g.node("top", "union", ["d1", "L3"])
            for n in (u1, i1, d1, top):
                oracle.check_node(g, n)
            # mutate inside a transaction and re-verify
            with g.transaction():
                leaves[0].set(ivl(-1, 1, False, True))
                leaves[1].set_unknown()
            for n in (u1, i1, d1, top):
                oracle.check_node(g, n)
            # rollback and re-verify
            with g.transaction() as tx:
                leaves[1].set(ivl(0, 0))
                leaves[2].clear()
                tx.rollback()
            for n in (u1, i1, d1, top):
                oracle.check_node(g, n)
            # proof soundness: replaying the expression with only the proven
            # leaves (others emptied) must still cover the point
            states = {lf.name: lf.state for lf in leaves}
            spec = oracle.spec_of(top)
            for x in oracle.sample_points(states):
                if top.contains(x):
                    proof = g.prove(top, x)
                    self.assertIsNotNone(proof)
                    sub = {
                        n: (st if n in proof else IntervalSet())
                        for n, st in states.items()
                    }
                    self.assertTrue(oracle.eval_spec(spec, sub, x))
                else:
                    self.assertIsNone(g.prove(top, x))


if __name__ == "__main__":
    unittest.main()
