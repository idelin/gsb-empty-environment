import unittest
from fractions import Fraction

from distances import (
    FlowEdge,
    general_transport,
    ordered_transport,
    total_variation,
    tv_contributions,
    verify_transport_flow,
)
from histogram import Histogram


def hist(pairs):
    h = Histogram()
    for v, n in pairs:
        h.add(v, n)
    return h


class TotalVariationTest(unittest.TestCase):
    def test_exact_rational(self):
        p = hist([("a", 1), ("b", 3)])
        q = hist([("a", 2), ("b", 2)])
        self.assertEqual(total_variation(p, q), Fraction(1, 4))

    def test_identical_is_zero(self):
        p = hist([("a", 1), ("b", 3)])
        q = hist([("a", 2), ("b", 6)])
        self.assertEqual(total_variation(p, q), Fraction(0))

    def test_disjoint_is_one(self):
        p = hist([("a", 2)])
        q = hist([("b", 5)])
        self.assertEqual(total_variation(p, q), Fraction(1))

    def test_contributions_sum_and_counts(self):
        p = hist([("a", 1), ("b", 3), ("c", 2)])
        q = hist([("a", 2), ("b", 1), ("d", 3)])
        contribs = tv_contributions(p, q)
        self.assertEqual(sum((c.contribution for c in contribs), Fraction(0)),
                         total_variation(p, q))
        by_value = {c.value: c for c in contribs}
        self.assertEqual(by_value["a"].class_count, 1)
        self.assertEqual(by_value["a"].population_count, 2)
        self.assertEqual(by_value["d"].class_count, 0)
        self.assertEqual(by_value["d"].population_prob, Fraction(1, 2))

    def test_empty_raises_not_divides(self):
        with self.assertRaises(ValueError):
            total_variation(Histogram(), hist([("a", 1)]))
        with self.assertRaises(ValueError):
            total_variation(hist([("a", 1)]), Histogram())


class OrderedTransportTest(unittest.TestCase):
    def test_point_mass_shift(self):
        res = ordered_transport(hist([(0, 1)]), hist([(3, 1)]))
        self.assertEqual(res.distance, 3)

    def test_known_value(self):
        res = ordered_transport(hist([(0, 1), (2, 1)]), hist([(1, 2)]))
        self.assertEqual(res.distance, Fraction(1))

    def test_identical_is_zero(self):
        res = ordered_transport(hist([(1, 2), (5, 1)]), hist([(1, 4), (5, 2)]))
        self.assertEqual(res.distance, 0)

    def test_interval_contributions_sum(self):
        res = ordered_transport(hist([(0, 2), (2, 1), (7, 3)]), hist([(1, 4), (9, 2)]))
        self.assertEqual(sum((c.contribution for c in res.intervals), Fraction(0)),
                         res.distance)

    def test_flow_optimal_and_feasible(self):
        p, q = hist([(0, 1), (2, 1)]), hist([(1, 1), (3, 1)])
        res = ordered_transport(p, q)
        check = verify_transport_flow(p, q, res.flow, lambda a, b: abs(a - b),
                                      expected_cost=res.distance)
        self.assertTrue(check.feasible, check.problems)
        self.assertTrue(check.optimal)
        self.assertEqual(res.distance, Fraction(1))

    def test_verifier_rejects_infeasible(self):
        p, q = hist([(0, 1), (2, 1)]), hist([(1, 1), (3, 1)])
        bad = [FlowEdge(0, 1, Fraction(3, 4)), FlowEdge(2, 3, Fraction(1, 4))]
        check = verify_transport_flow(p, q, bad, lambda a, b: abs(a - b))
        self.assertFalse(check.feasible)
        self.assertTrue(check.problems)

    def test_verifier_detects_suboptimal(self):
        # x=0 的可行流成本 2，而最优（x=1/2）成本 1。
        p, q = hist([(0, 1), (2, 1)]), hist([(1, 1), (3, 1)])
        sub = [FlowEdge(0, 3, Fraction(1, 2)), FlowEdge(2, 1, Fraction(1, 2))]
        check = verify_transport_flow(p, q, sub, lambda a, b: abs(a - b),
                                      expected_cost=Fraction(1))
        self.assertTrue(check.feasible, check.problems)
        self.assertEqual(check.cost, 2)
        self.assertFalse(check.optimal)

    def test_empty_raises(self):
        with self.assertRaises(ValueError):
            ordered_transport(Histogram(), hist([(1, 1)]))


class GeneralTransportTest(unittest.TestCase):
    def test_differs_from_1d_formula(self):
        # 成本矩阵破坏一维结构：远距离反而便宜。
        p = hist([(0, 1), (2, 1)])
        q = hist([(1, 1), (3, 1)])
        weird = {(0, 1): 9, (0, 3): 0, (2, 1): 0, (2, 3): 9}
        res = general_transport(p, q, cost_matrix=weird)
        self.assertEqual(res.distance, Fraction(0))
        self.assertNotEqual(res.distance,
                            ordered_transport(p, q).distance)

    def test_normalized_flow_verifies(self):
        p = hist([("a", 2), ("b", 1)])
        q = hist([("x", 2), ("y", 4)])
        costs = {("a", "x"): 5, ("a", "y"): 1, ("b", "x"): 1, ("b", "y"): 2}
        res = general_transport(p, q, cost_matrix=costs)
        check = verify_transport_flow(p, q, res.flow, lambda a, b: costs[(a, b)],
                                      expected_cost=res.distance)
        self.assertTrue(check.feasible, check.problems)
        self.assertTrue(check.optimal)


if __name__ == "__main__":
    unittest.main()
