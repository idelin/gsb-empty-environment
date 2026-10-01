"""Reference verification: the ranked enumeration must agree with an
independent brute-force enumeration of the full permutation group.

The reference iterates raw itertools.permutations over observation
indices (N! ordered arrangements, first n1 = group 1), which makes no
use of the library's ranking logic at all.
"""
import itertools
import math
import unittest

from permtest.exact import exact_test, is_extreme
from permtest.spaces import IndependentTwoSampleSpace, PairedSwapSpace, StratifiedSpace


def brute_force_independent(values, n1, alternative):
    n = len(values)
    total = math.fsum(values)
    n2 = n - n1
    observed = math.fsum(values[:n1]) / n1 - math.fsum(values[n1:]) / n2
    tol = 1e-9 * max(1.0, abs(observed))
    n_extreme = 0
    n_total = 0
    for perm in itertools.permutations(range(n)):
        s1 = math.fsum(values[i] for i in perm[:n1])
        s2 = total - s1
        stat = s1 / n1 - s2 / n2
        n_total += 1
        if is_extreme(stat, observed, alternative, tol):
            n_extreme += 1
    # Each label assignment appears n1! * n2! times among the N! ordered
    # permutations; the proportion is unaffected.
    return n_extreme / n_total


def brute_force_paired(differences, alternative):
    n = len(differences)
    observed = math.fsum(differences) / n
    tol = 1e-9 * max(1.0, abs(observed))
    n_extreme = 0
    n_total = 0
    for signs in itertools.product((1, -1), repeat=n):
        stat = math.fsum(s * d for s, d in zip(signs, differences)) / n
        n_total += 1
        if is_extreme(stat, observed, alternative, tol):
            n_extreme += 1
    return n_extreme / n_total


def brute_force_stratified(strata, alternative):
    spaces = [(list(values), n1) for values, n1 in strata]
    totals = [math.fsum(v) for v, _ in spaces]
    big_n1 = sum(n1 for _, n1 in spaces)
    big_n2 = sum(len(v) - n1 for v, n1 in spaces)

    def stat_for(choices):
        s1 = s2 = 0.0
        for (values, _), total, idx in zip(spaces, totals, choices):
            g1 = math.fsum(values[i] for i in idx)
            s1 += g1
            s2 += total - g1
        return s1 / big_n1 - s2 / big_n2

    observed = stat_for([tuple(range(n1)) for _, n1 in spaces])
    tol = 1e-9 * max(1.0, abs(observed))
    per_stratum = [
        list(itertools.combinations(range(len(values)), n1))
        for values, n1 in spaces
    ]
    n_extreme = 0
    n_total = 0
    for choices in itertools.product(*per_stratum):
        n_total += 1
        if is_extreme(stat_for(choices), observed, alternative, tol):
            n_extreme += 1
    return n_extreme / n_total


class TestIndependentReference(unittest.TestCase):
    CASES = [
        ([3, 4, 1, 2], 2),
        ([5, 1, 1, 2, 3], 2),
        ([2, 2, 2, 5, 1], 3),
        ([7, 3, 1, 4, 2, 6], 4),
    ]

    def test_matches_full_permutation_reference(self):
        for values, n1 in self.CASES:
            for alternative in ("two-sided", "greater", "less"):
                with self.subTest(values=values, n1=n1, alternative=alternative):
                    space = IndependentTwoSampleSpace(values, n1)
                    result = exact_test(space, alternative=alternative)
                    expected = brute_force_independent(values, n1, alternative)
                    self.assertAlmostEqual(result.p_value, expected, places=12)
                    self.assertEqual(result.n_total, math.comb(len(values), n1))


class TestPairedReference(unittest.TestCase):
    def test_matches_sign_flip_reference(self):
        for differences in ([1, 2, 4], [3, -1, 2, 5], [1, 1, 1, 2]):
            for alternative in ("two-sided", "greater", "less"):
                with self.subTest(differences=differences, alternative=alternative):
                    result = exact_test(PairedSwapSpace(differences), alternative=alternative)
                    expected = brute_force_paired(differences, alternative)
                    self.assertAlmostEqual(result.p_value, expected, places=12)
                    self.assertEqual(result.n_total, 2 ** len(differences))


class TestStratifiedReference(unittest.TestCase):
    def test_matches_product_reference(self):
        strata = [([3, 1, 2], 1), ([5, 4, 1, 2], 2)]
        for alternative in ("two-sided", "greater", "less"):
            with self.subTest(alternative=alternative):
                space = StratifiedSpace(strata)
                result = exact_test(space, alternative=alternative)
                expected = brute_force_stratified(strata, alternative)
                self.assertAlmostEqual(result.p_value, expected, places=12)
                self.assertEqual(result.n_total, math.comb(3, 1) * math.comb(4, 2))


if __name__ == "__main__":
    unittest.main()
