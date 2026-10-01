"""Unit tests for factor_graph (Python 3.11, standard library only)."""

import random
import unittest

from factor_graph import (
    ContradictoryEvidenceError,
    DomainMismatchError,
    EmpiricalMarginal,
    ExactMarginal,
    Factor,
    FactorGraph,
    UnknownVariableError,
    elimination_width,
    greedy_order,
    multiply,
)


def sprinkler_graph():
    """Classic Cloudy/Sprinkler/Rain/WetGrass network as a factor graph."""
    g = FactorGraph()
    binary = (0, 1)
    g.add(("C",), {"C": binary}, [0.5, 0.5])
    g.add(("S", "C"), {"S": binary, "C": binary}, [0.5, 0.9, 0.5, 0.1])
    g.add(("R", "C"), {"R": binary, "C": binary}, [0.8, 0.2, 0.2, 0.8])
    g.add(("S", "R", "W"), {"S": binary, "R": binary, "W": binary},
          [1.0, 0.0, 0.1, 0.9, 0.1, 0.9, 0.01, 0.99])
    return g


class TestExactInference(unittest.TestCase):
    def test_marginal_matches_known_value(self):
        g = sprinkler_graph()
        m = g.eliminate(["R"])
        self.assertEqual(m.kind, "exact")
        # P(R=1) = 0.5*0.2 + 0.5*0.8 = 0.5
        self.assertAlmostEqual(m.probability(1), 0.5)
        self.assertAlmostEqual(m.normalization, 1.0)

    def test_posterior_with_evidence(self):
        g = sprinkler_graph()
        m = g.eliminate(["R"], evidence={"W": 1})
        # P(R=1 | W=1) ~ 0.7079 (well-known value for this network)
        self.assertAlmostEqual(m.probability(1), 0.7079, places=4)
        # Z equals P(W=1) computed by enumeration
        z_enum = g.enumerate_marginal([], evidence={"W": 1}).normalization
        self.assertAlmostEqual(m.normalization, z_enum)
        self.assertAlmostEqual(m.normalization, 0.6471, places=4)

    def test_joint_marginal(self):
        g = sprinkler_graph()
        m = g.eliminate(["S", "R"], evidence={"W": 1})
        total = sum(m.probabilities)
        self.assertAlmostEqual(total, 1.0)
        # marginalizing the joint recovers the single-variable marginal
        p_r1 = m.probability({"S": 0, "R": 1}) + m.probability({"S": 1, "R": 1})
        self.assertAlmostEqual(p_r1, g.eliminate(["R"], {"W": 1}).probability(1))

    def test_ve_matches_enumeration_on_random_graphs(self):
        rng = random.Random(7)
        for trial in range(30):
            g = FactorGraph()
            n_vars = rng.randint(2, 5)
            names = [f"V{i}" for i in range(n_vars)]
            domains = {v: tuple(range(rng.randint(2, 3))) for v in names}
            for _ in range(rng.randint(n_vars, n_vars + 3)):
                scope = tuple(sorted(rng.sample(names, rng.randint(1, min(3, n_vars)))))
                size = 1
                for v in scope:
                    size *= len(domains[v])
                table = [rng.random() * 2 for _ in range(size)]
                if rng.random() < 0.3:  # sprinkle in exact zeros
                    table[rng.randrange(size)] = 0.0
                g.add(scope, domains, table)
            names = sorted(g.domains)  # only vars that appear in a factor
            if len(names) < 2:
                continue
            query = tuple(sorted(rng.sample(names, rng.randint(1, 2))))
            rest = [v for v in names if v not in query]
            evidence = {}
            if rest and rng.random() < 0.7:
                ev = rng.choice(rest)
                evidence[ev] = rng.choice(g.domains[ev])
            hidden = [v for v in names
                      if v not in query and v not in evidence]
            order = rng.sample(hidden, k=len(hidden))
            try:
                expected = g.enumerate_marginal(query, evidence)
            except ContradictoryEvidenceError:
                with self.assertRaises(ContradictoryEvidenceError):
                    g.eliminate(query, evidence, order=order)
                continue
            got = g.eliminate(query, evidence, order=order)
            self.assertAlmostEqual(got.normalization, expected.normalization)
            for a, b in zip(got.probabilities, expected.probabilities):
                self.assertAlmostEqual(a, b, msg=f"trial {trial}")

    def test_zero_probability_deterministic_factor(self):
        g = FactorGraph()
        g.add(("A",), {"A": (0, 1)}, [0.5, 0.5])
        # B is a deterministic copy of A (zeros encode the constraint)
        g.add(("B", "A"), {"B": (0, 1), "A": (0, 1)},
              [1.0, 0.0, 0.0, 1.0])
        m = g.eliminate(["B"], evidence={"A": 1})
        self.assertEqual(m.probabilities, (0.0, 1.0))
        self.assertAlmostEqual(m.normalization, 0.5)


class TestEvidenceErrors(unittest.TestCase):
    def test_contradictory_evidence_raises(self):
        g = FactorGraph()
        g.add(("A",), {"A": (0, 1)}, [1.0, 0.0])  # A=1 is impossible
        g.add(("B", "A"), {"B": (0, 1), "A": (0, 1)}, [0.3, 0.7, 0.4, 0.6])
        with self.assertRaises(ContradictoryEvidenceError):
            g.eliminate(["B"], evidence={"A": 1})
        with self.assertRaises(ContradictoryEvidenceError):
            g.enumerate_marginal(["B"], evidence={"A": 1})
        with self.assertRaises(ContradictoryEvidenceError):
            g.sample_gibbs(["B"], evidence={"A": 1},
                           rng=random.Random(0), samples=10)

    def test_evidence_value_outside_domain_raises(self):
        g = sprinkler_graph()
        with self.assertRaises(ContradictoryEvidenceError):
            g.eliminate(["R"], evidence={"W": 7})

    def test_unknown_variable_raises(self):
        g = sprinkler_graph()
        with self.assertRaises(UnknownVariableError):
            g.eliminate(["Nope"])
        with self.assertRaises(UnknownVariableError):
            g.eliminate(["R"], evidence={"Nope": 1})

    def test_query_and_evidence_overlap_rejected(self):
        g = sprinkler_graph()
        with self.assertRaises(ValueError):
            g.eliminate(["R"], evidence={"R": 1})


class TestDomainConsistency(unittest.TestCase):
    def test_same_variable_different_domain_rejected(self):
        g = FactorGraph()
        g.add(("A",), {"A": (0, 1)}, [0.5, 0.5])
        with self.assertRaises(DomainMismatchError):
            g.add(("A", "B"), {"A": ("x", "y"), "B": (0, 1)},
                  [0.1, 0.2, 0.3, 0.4])

    def test_multiply_mismatched_domains_rejected(self):
        f1 = Factor(("A",), {"A": (0, 1)}, [1.0, 2.0])
        f2 = Factor(("A",), {"A": (0, 1, 2)}, [1.0, 1.0, 1.0])
        with self.assertRaises(DomainMismatchError):
            multiply(f1, f2)

    def test_same_variable_same_domain_accepted(self):
        g = FactorGraph()
        g.add(("A",), {"A": (0, 1)}, [0.5, 0.5])
        g.add(("A", "B"), {"A": (0, 1), "B": (0, 1)}, [1, 2, 3, 4])
        self.assertEqual(g.domains["A"], (0, 1))


class TestEliminationOrder(unittest.TestCase):
    def star_graph(self, leaves=4):
        g = FactorGraph()
        binary = (0, 1)
        g.add(("C",), {"C": binary}, [0.5, 0.5])
        for i in range(leaves):
            g.add((f"L{i}", "C"), {f"L{i}": binary, "C": binary},
                  [0.3 + 0.1 * i, 0.7 - 0.1 * i, 0.6, 0.4])
        return g

    def test_order_controls_intermediate_table_size(self):
        g = self.star_graph()
        hidden = ["C"] + [f"L{i}" for i in range(4)]
        scopes = [set(f.variables) for f in g.factors]
        greedy = greedy_order(scopes, hidden, g.domains)
        center_first = ["C", "L0", "L1", "L2", "L3"]
        w_greedy = elimination_width(scopes, greedy, g.domains)
        w_bad = elimination_width(scopes, center_first, g.domains)
        self.assertLess(w_greedy, w_bad)  # 4 vs 32
        self.assertEqual(w_greedy, 4)
        self.assertEqual(w_bad, 32)

    def test_custom_order_is_respected_and_validated(self):
        g = sprinkler_graph()
        m = g.eliminate(["R"], order=["W", "S", "C"])
        self.assertAlmostEqual(m.probability(1), 0.5)
        with self.assertRaises(ValueError):
            g.eliminate(["R"], order=["C"])  # not a full permutation
        with self.assertRaises(ValueError):
            g.eliminate(["R"], order=["W", "S", "C", "R"])  # includes query

    def test_all_orders_agree_with_enumeration(self):
        g = sprinkler_graph()
        expected = g.enumerate_marginal(["S"], {"W": 1})
        for order in (["C", "R"], ["R", "C"]):
            got = g.eliminate(["S"], {"W": 1}, order=order)
            for a, b in zip(got.probabilities, expected.probabilities):
                self.assertAlmostEqual(a, b)


class TestEmpiricalInference(unittest.TestCase):
    def test_gibbs_estimate_close_to_exact(self):
        g = sprinkler_graph()
        exact = g.eliminate(["R"], evidence={"W": 1})
        est = g.sample_gibbs(["R"], evidence={"W": 1},
                             rng=random.Random(42), samples=20000,
                             burn_in=500, seed=42)
        self.assertEqual(est.kind, "empirical")
        self.assertEqual(est.samples, 20000)
        self.assertEqual(est.seed, 42)
        self.assertAlmostEqual(sum(est.probabilities), 1.0)
        self.assertAlmostEqual(est.probability(1), exact.probability(1),
                               delta=0.02)

    def test_reproducible_with_same_seed(self):
        g = sprinkler_graph()
        a = g.sample_gibbs(["R"], {"W": 1}, rng=random.Random(123),
                           samples=500, seed=123)
        b = g.sample_gibbs(["R"], {"W": 1}, rng=random.Random(123),
                           samples=500, seed=123)
        self.assertEqual(a.probabilities, b.probabilities)

    def test_random_source_must_be_injected(self):
        g = sprinkler_graph()
        with self.assertRaises(TypeError):
            g.sample_gibbs(["R"], rng=random, samples=10)

    def test_result_labels(self):
        g = sprinkler_graph()
        exact = g.eliminate(["R"])
        empirical = g.sample_gibbs(["R"], rng=random.Random(1), samples=10,
                                   seed=1)
        self.assertIsInstance(exact, ExactMarginal)
        self.assertIsInstance(empirical, EmpiricalMarginal)
        self.assertEqual(exact.kind, "exact")
        self.assertEqual(empirical.kind, "empirical")


if __name__ == "__main__":
    unittest.main()
