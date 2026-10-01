import random
import unittest

from factorelim import (
    ContradictoryEvidenceError,
    FactorGraph,
    brute_force_query,
    greedy_order,
    query,
)


def make_chain(n=4):
    """Chain X0 - X1 - ... - X(n-1) of binary variables."""
    g = FactorGraph()
    for i in range(n):
        g.add_variable(f"X{i}", (0, 1))
    g.add_factor(("X0",), [0.4, 0.6])
    for i in range(n - 1):
        g.add_factor((f"X{i}", f"X{i+1}"), [0.7, 0.3, 0.2, 0.8])
    return g


def make_star(n_leaves=4):
    """Star: center X connected to binary leaves L0..L(n-1).

    Eliminating the center first builds one big table over all leaves;
    eliminating leaves first keeps every intermediate tiny.
    """
    g = FactorGraph()
    g.add_variable("X", (0, 1))
    for i in range(n_leaves):
        g.add_variable(f"L{i}", (0, 1))
    g.add_factor(("X",), [0.3, 0.7])
    for i in range(n_leaves):
        g.add_factor(("X", f"L{i}"), [0.9, 0.1, 0.25, 0.75])
    return g


class TestExactQuery(unittest.TestCase):
    def test_chain_marginal_matches_enumeration(self):
        g = make_chain(5)
        got = query(g, ("X2",))
        want = brute_force_query(g, ("X2",))
        self.assertAlmostEqual(got.normalizer, want.normalizer)
        for key, p in want.probabilities.items():
            self.assertAlmostEqual(got.probabilities[key], p, places=12)

    def test_joint_query_matches_enumeration(self):
        g = make_chain(4)
        got = query(g, ("X0", "X3"))
        want = brute_force_query(g, ("X0", "X3"))
        self.assertAlmostEqual(got.normalizer, want.normalizer)
        for key, p in want.probabilities.items():
            self.assertAlmostEqual(got.probabilities[key], p, places=12)

    def test_evidence_query_matches_enumeration(self):
        g = make_chain(5)
        ev = {"X0": 1, "X4": 0}
        got = query(g, ("X2",), evidence=ev)
        want = brute_force_query(g, ("X2",), evidence=ev)
        self.assertAlmostEqual(got.normalizer, want.normalizer)
        for key, p in want.probabilities.items():
            self.assertAlmostEqual(got.probabilities[key], p, places=12)
        self.assertAlmostEqual(sum(got.probabilities.values()), 1.0)

    def test_normalizer_is_evidence_probability(self):
        g = make_chain(3)
        # P(X0=1) = 0.6 by construction of the root factor.
        got = query(g, ("X1",), evidence={"X0": 1})
        self.assertAlmostEqual(got.normalizer, 0.6)

    def test_zero_probability_evidence_raises(self):
        g = FactorGraph()
        g.add_variable("A", (0, 1))
        g.add_factor(("A",), [1.0, 0.0])  # A=1 is impossible
        with self.assertRaises(ContradictoryEvidenceError) as ctx:
            query(g, ("A",), evidence={"A": 1})
        self.assertIn("Z = 0", str(ctx.exception))

    def test_zero_probability_evidence_raises_in_enumeration(self):
        g = FactorGraph()
        g.add_variable("A", (0, 1))
        g.add_factor(("A",), [1.0, 0.0])
        with self.assertRaises(ContradictoryEvidenceError):
            brute_force_query(g, ("A",), evidence={"A": 1})

    def test_deterministic_xor_zero_probs(self):
        g = FactorGraph()
        for v in "ABC":
            g.add_variable(v, (0, 1))
        g.add_factor(("A",), [0.5, 0.5])
        g.add_factor(("B",), [0.5, 0.5])
        # C = A xor B, deterministic factor with zeros.
        g.add_factor(("A", "B", "C"), [1, 0, 0, 1, 0, 1, 1, 0])
        got = query(g, ("C",), evidence={"A": 1, "B": 1})
        self.assertAlmostEqual(got.probabilities[(0,)], 1.0)
        self.assertAlmostEqual(got.probabilities[(1,)], 0.0)

    def test_result_labelled_exact(self):
        g = make_chain(3)
        self.assertEqual(query(g, ("X0",)).method, "exact")
        self.assertIn("[EXACT]", str(query(g, ("X0",))))


class TestEliminationOrder(unittest.TestCase):
    def test_order_controls_intermediate_size(self):
        g = make_star(4)
        # Center first: one 2^5 = 32-entry intermediate table.
        bad = query(g, (), order=("X", "L0", "L1", "L2", "L3"))
        # Leaves first (what greedy picks): intermediates stay at 4.
        good = query(g, (), order=greedy_order(
            [f.restrict({}) for f in g.factors], ()
        ))
        self.assertGreater(bad.stats.max_intermediate_size,
                           good.stats.max_intermediate_size)
        self.assertEqual(bad.stats.max_intermediate_size, 32)
        self.assertEqual(good.stats.max_intermediate_size, 4)
        # Both orders give the same normalizer.
        self.assertAlmostEqual(bad.normalizer, good.normalizer)

    def test_stats_record_every_elimination(self):
        g = make_chain(4)
        res = query(g, ("X0",))
        self.assertEqual(len(res.stats.intermediate_sizes), 3)
        self.assertEqual(res.stats.max_intermediate_size, 4)

    def test_partial_order_completed_automatically(self):
        g = make_chain(4)
        res = query(g, ("X0",), order=("X2",))
        self.assertEqual(set(res.stats.order), {"X1", "X2", "X3"})

    def test_random_small_graphs_match_enumeration(self):
        rng = random.Random(2024)
        for trial in range(30):
            n = rng.randint(2, 5)
            g = FactorGraph()
            for i in range(n):
                g.add_variable(f"V{i}", tuple(range(rng.randint(2, 3))))
            for i in range(n):
                table = [rng.random() for _ in g.domains[f"V{i}"]]
                g.add_factor((f"V{i}",), table)
            for _ in range(rng.randint(1, n)):
                i, j = rng.sample(range(n), 2)
                size = len(g.domains[f"V{i}"]) * len(g.domains[f"V{j}"])
                table = [rng.random() for _ in range(size)]
                g.add_factor((f"V{i}", f"V{j}"), table)
            qv = (f"V{rng.randrange(n)}",)
            got = query(g, qv)
            want = brute_force_query(g, qv)
            self.assertAlmostEqual(got.normalizer, want.normalizer, places=9)
            for key, p in want.probabilities.items():
                self.assertAlmostEqual(got.probabilities[key], p, places=9)


if __name__ == "__main__":
    unittest.main()
