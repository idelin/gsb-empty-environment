import math
import unittest

from amplification import dag, intervals, stable
from amplification.assumptions import AssumptionError, SamplingAssumption
from amplification.intervals import Interval


def make_round(q, eps, **kw):
    return dag.EpsPrime(dag.Const(q), dag.Const(eps), SamplingAssumption(**kw))


class TestDagEvaluation(unittest.TestCase):
    def test_eps_prime_node_point_and_interval(self):
        node = dag.EpsPrime(dag.Param("q"), dag.Param("eps"))
        env = {"q": 0.1, "eps": 1.0}
        self.assertAlmostEqual(
            node.eval_point(env), math.log1p(0.1 * math.expm1(1.0)), places=15
        )
        iv = node.eval_interval({"q": Interval(0.09, 0.11), "eps": 1.0})
        self.assertTrue(iv.contains(stable.eps_prime(0.1, 1.0)))

    def test_multi_round_composition_sums(self):
        rounds = [make_round(0.01, 2.0) for _ in range(10)]
        comp = dag.compose_rounds(rounds)
        expected = 10.0 * stable.eps_prime(0.01, 2.0)
        self.assertAlmostEqual(comp.eval_point({}), expected, places=12)
        iv = comp.eval_interval({})
        self.assertTrue(iv.contains(expected))
        self.assertLess(iv.width, 1e-9)

    def test_composition_with_huge_eps_no_overflow(self):
        rounds = [make_round(1e-3, 1000.0) for _ in range(5)]
        comp = dag.compose_rounds(rounds)
        total = comp.eval_point({})
        self.assertTrue(math.isfinite(total))
        self.assertAlmostEqual(total, 5.0 * (1000.0 + math.log(1e-3)), places=4)
        iv = comp.eval_interval({})
        self.assertTrue(iv.contains(total))

    def test_mixed_param_and_const_leaves(self):
        node = dag.Compose((
            dag.EpsPrime(dag.Param("q"), dag.Const(1.0)),
            dag.EpsPrime(dag.Const(0.5), dag.Param("eps")),
        ))
        env = {"q": 0.2, "eps": 3.0}
        expected = stable.eps_prime(0.2, 1.0) + stable.eps_prime(0.5, 3.0)
        self.assertAlmostEqual(node.eval_point(env), expected, places=14)


class TestCompositionRejection(unittest.TestCase):
    def test_rejects_non_poisson_scheme(self):
        with self.assertRaises(AssumptionError):
            make_round(0.1, 1.0, scheme="without_replacement")

    def test_rejects_wrong_adjacency(self):
        with self.assertRaises(AssumptionError):
            make_round(0.1, 1.0, adjacency="replace_one")

    def test_rejects_nonzero_base_delta(self):
        with self.assertRaises(AssumptionError):
            make_round(0.1, 1.0, base_delta=1e-5)

    def test_rejects_inconsistent_rounds(self):
        r1 = make_round(0.1, 1.0, dataset_size=1000)
        r2 = make_round(0.1, 1.0, dataset_size=2000)
        with self.assertRaises(AssumptionError):
            dag.compose_rounds([r1, r2])

    def test_rejects_empty_composition(self):
        with self.assertRaises(AssumptionError):
            dag.compose_rounds([])

    def test_accepts_consistent_rounds(self):
        r1 = make_round(0.1, 1.0, dataset_size=1000)
        r2 = make_round(0.2, 0.5, dataset_size=1000)
        comp = dag.compose_rounds([r1, r2])
        self.assertGreater(comp.eval_point({}), 0.0)


if __name__ == "__main__":
    unittest.main()
