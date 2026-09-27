import os
import random
import sys
import unittest
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from distances import (
    emd_1d,
    emd_general,
    emd_general_exhaustive,
    min_cost_flow,
    total_variation,
    verify_emd_1d,
    verify_transport_plan,
)


class TotalVariationTests(unittest.TestCase):
    def test_known_value_exact_rational(self):
        # P = (1/2, 1/2), Q = (1/4, 3/4) -> TV = 1/4 exactly.
        tv, contribs = total_variation([1, 1], [1, 3])
        self.assertEqual(tv, Fraction(1, 4))
        self.assertTrue(all(isinstance(c, Fraction) for c in contribs))
        self.assertEqual(sum(contribs, Fraction(0)), tv)

    def test_identical_distributions_are_zero(self):
        tv, contribs = total_variation([2, 3, 5], [4, 6, 10])
        self.assertEqual(tv, 0)
        self.assertTrue(all(c == 0 for c in contribs))

    def test_disjoint_supports_are_one(self):
        tv, _ = total_variation([3, 0], [0, 4])
        self.assertEqual(tv, 1)

    def test_empty_distribution_does_not_divide_by_zero(self):
        with self.assertRaises(ValueError):
            total_variation([0, 0], [1, 1])
        with self.assertRaises(ValueError):
            total_variation([1, 1], [0, 0])


class Emd1dTests(unittest.TestCase):
    POSITIONS = [0, 1, 2]

    def test_mass_moved_two_units(self):
        result = emd_1d([1, 0, 0], [0, 0, 1], self.POSITIONS)
        self.assertEqual(result["distance"], 2)
        self.assertEqual(result["flow"][0][2], 1)
        verify_emd_1d(
            [1, 0, 0], [0, 0, 1], self.POSITIONS,
            result["flow"], result["distance"], result["dual"],
        )

    def test_known_center_case(self):
        # P = (1/2, 0, 1/2), Q = (0, 1, 0): both halves move one unit -> 1.
        result = emd_1d([1, 0, 1], [0, 2, 0], self.POSITIONS)
        self.assertEqual(result["distance"], 1)
        self.assertEqual(result["gap_contribs"], [Fraction(1, 2), Fraction(1, 2)])
        verify_emd_1d(
            [1, 0, 1], [0, 2, 0], self.POSITIONS,
            result["flow"], result["distance"], result["dual"],
        )

    def test_identical_distributions_are_zero(self):
        result = emd_1d([2, 3, 5], [4, 6, 10], self.POSITIONS)
        self.assertEqual(result["distance"], 0)
        self.assertTrue(all(c == 0 for c in result["gap_contribs"]))
        verify_emd_1d(
            [2, 3, 5], [4, 6, 10], self.POSITIONS,
            result["flow"], result["distance"], result["dual"],
        )

    def test_empty_distribution_does_not_divide_by_zero(self):
        with self.assertRaises(ValueError):
            emd_1d([0, 0, 0], [1, 1, 1], self.POSITIONS)

    def test_irregular_positions(self):
        # Positions 0, 3, 10: P at 0, Q at 3 -> distance 3.
        result = emd_1d([1, 0, 0], [0, 1, 0], [0, 3, 10])
        self.assertEqual(result["distance"], 3)
        verify_emd_1d(
            [1, 0, 0], [0, 1, 0], [0, 3, 10],
            result["flow"], result["distance"], result["dual"],
        )

    def test_verifier_rejects_tampered_cost(self):
        result = emd_1d([1, 0, 0], [0, 0, 1], self.POSITIONS)
        with self.assertRaises(ValueError):
            verify_emd_1d(
                [1, 0, 0], [0, 0, 1], self.POSITIONS,
                result["flow"], result["distance"] + 1, result["dual"],
            )

    def test_verifier_rejects_bad_dual(self):
        result = emd_1d([1, 0, 0], [0, 0, 1], self.POSITIONS)
        bad_dual = [0, 10, 0]
        with self.assertRaises(ValueError):
            verify_emd_1d(
                [1, 0, 0], [0, 0, 1], self.POSITIONS,
                result["flow"], result["distance"], bad_dual,
            )


class MinCostFlowTests(unittest.TestCase):
    def test_simple_assignment(self):
        flow, cost = min_cost_flow([1, 1], [1, 1], [[0, 5], [5, 0]])
        self.assertEqual(cost, 0)
        self.assertEqual(flow, [[1, 0], [0, 1]])

    def test_matches_exhaustive_on_random_small_instances(self):
        rng = random.Random(1234)
        # Exhaustive enumeration is exponential; keep instances tiny.
        for dims in ((2, 2), (3, 3)):
            m, n = dims
            for _ in range(20):
                p_counts = [rng.randrange(0, 4) for _ in range(m)]
                q_counts = [rng.randrange(0, 4) for _ in range(n)]
                if sum(p_counts) == 0 or sum(q_counts) == 0:
                    continue
                cost_matrix = [
                    [rng.randrange(0, 6) for _ in range(n)] for _ in range(m)
                ]
                distance, flow, _ = emd_general(p_counts, q_counts, cost_matrix)
                # 1) exhaustive optimum agrees
                self.assertEqual(
                    distance,
                    emd_general_exhaustive(p_counts, q_counts, cost_matrix),
                )
                # 2) the returned plan is feasible and costs exactly that
                verify_transport_plan(
                    p_counts, q_counts, cost_matrix, flow, distance
                )

    def test_general_matrix_differs_from_1d_cumulative_formula(self):
        # Squared-distance ground cost on 3 ordered buckets.  The cumulative
        # formula assumes the line metric |x_i - x_j| and gives a different
        # (wrong) value, proving the general mode needs a real min-cost flow.
        positions = [0, 1, 2]
        squared = [[0, 1, 4], [1, 0, 1], [4, 1, 0]]
        p_counts, q_counts = [1, 0, 1], [0, 2, 0]
        general_distance, flow, _ = emd_general(p_counts, q_counts, squared)
        line_result = emd_1d(p_counts, q_counts, positions)
        self.assertEqual(general_distance, 1)  # two half units, squared cost 1
        self.assertEqual(line_result["distance"], 1)
        # A case where the two formulations genuinely diverge:
        p_counts, q_counts = [2, 0, 0], [0, 0, 2]
        general_distance, flow, _ = emd_general(p_counts, q_counts, squared)
        line_result = emd_1d(p_counts, q_counts, positions)
        self.assertEqual(general_distance, 4)
        self.assertEqual(line_result["distance"], 2)
        self.assertNotEqual(general_distance, line_result["distance"])
        verify_transport_plan(p_counts, q_counts, squared, flow, general_distance)
        self.assertEqual(
            general_distance,
            emd_general_exhaustive(p_counts, q_counts, squared),
        )

    def test_identical_distributions_are_zero(self):
        distance, flow, contribs = emd_general(
            [2, 3], [6, 9], [[0, 1], [1, 0]]
        )
        self.assertEqual(distance, 0)
        self.assertTrue(all(c == 0 for c in contribs))
        verify_transport_plan([2, 3], [6, 9], [[0, 1], [1, 0]], flow, distance)

    def test_empty_distribution_does_not_divide_by_zero(self):
        with self.assertRaises(ValueError):
            emd_general([0, 0], [1, 1], [[0, 1], [1, 0]])


if __name__ == "__main__":
    unittest.main(verbosity=2)
