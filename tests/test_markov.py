"""unittest suite for the markov package (standard library only)."""

import math
import os
import sys
import unittest
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from markov import (MarkovChain, SparseTransitionMatrix, TransientSolver,
                    ValidationError, assess_convergence, exact_solve,
                    gauss_seidel)

HALF = Fraction(1, 2)


def gamblers_ruin_rows(n=4):
    """States 0..n; 0 and n absorbing; fair coin otherwise (exact halves)."""
    rows = {0: {0: 1}, n: {n: 1}}
    for i in range(1, n):
        rows[i] = {i - 1: HALF, i + 1: HALF}
    return rows


class ValidationTests(unittest.TestCase):
    def test_row_sum_mismatch_raises_and_is_not_normalized(self):
        m = SparseTransitionMatrix(2)
        m.set(0, 0, 0.3)
        m.set(0, 1, 0.6)  # sums to 0.9
        m.set(1, 1, 1.0)
        with self.assertRaises(ValidationError) as ctx:
            m.validate()
        self.assertIn("refusing to normalize", str(ctx.exception))

    def test_missing_probability_row_raises(self):
        m = SparseTransitionMatrix(3)
        m.set(0, 0, 1.0)
        m.set(1, 0, 1.0)
        with self.assertRaises(ValidationError) as ctx:
            m.validate()
        self.assertIn("missing probability line 2", str(ctx.exception))

    def test_negative_and_above_one_entries_raise(self):
        m = SparseTransitionMatrix(2)
        with self.assertRaises(ValidationError):
            m.set(0, 0, -0.1)
        with self.assertRaises(ValidationError):
            m.set(0, 0, 1.5)

    def test_index_out_of_range_raises(self):
        m = SparseTransitionMatrix(2)
        with self.assertRaises(ValidationError):
            m.set(0, 2, 1.0)
        with self.assertRaises(ValidationError):
            m.set(-1, 0, 1.0)

    def test_valid_row_stochastic_passes(self):
        m = SparseTransitionMatrix.from_rows(5, gamblers_ruin_rows())
        self.assertTrue(m.validate())

    def test_column_convention(self):
        rows = gamblers_ruin_rows()
        col_entries = {}
        for i, row in rows.items():
            for j, p in row.items():
                col_entries.setdefault(j, {})[i] = p
        m = SparseTransitionMatrix.from_rows(5, col_entries,
                                             convention="column")
        self.assertTrue(m.validate())
        chain_col = MarkovChain(m)
        chain_row = MarkovChain.from_rows(5, rows)
        self.assertEqual(chain_col.out, chain_row.out)

    def test_column_sum_mismatch_raises(self):
        m = SparseTransitionMatrix(2, convention="column")
        m.set(0, 0, 0.5)
        m.set(0, 1, 0.5)  # column 0 sums to 0.5
        m.set(1, 0, 1.0)
        with self.assertRaises(ValidationError):
            m.validate()


class DistributionTests(unittest.TestCase):
    def setUp(self):
        self.chain = MarkovChain.from_rows(5, gamblers_ruin_rows())

    def test_negative_distribution_raises(self):
        with self.assertRaises(ValidationError):
            self.chain.check_distribution([1.5, -0.5, 0.0, 0.0, 0.0])

    def test_bad_sum_raises_not_normalized(self):
        with self.assertRaises(ValidationError) as ctx:
            self.chain.check_distribution([0.3, 0.3, 0.3, 0.0, 0.0])
        self.assertIn("refusing to normalize", str(ctx.exception))

    def test_mass_conservation_and_nonnegativity(self):
        dist = [0.0, 0.0, 1.0, 0.0, 0.0]
        for step, new, mass_error in self.chain.propagate(dist, 500):
            self.assertLess(abs(mass_error), 1e-12)
            self.assertTrue(all(v >= 0 for v in new))
            self.assertAlmostEqual(sum(new), 1.0, places=12)

    def test_absorbing_mass_does_not_leak(self):
        dist = [0.0, 0.0, 1.0, 0.0, 0.0]
        final = dist
        for _, final, _ in self.chain.propagate(dist, 300):
            pass
        self.assertAlmostEqual(final[0], 0.5, places=9)
        self.assertAlmostEqual(final[4], 0.5, places=9)
        self.assertAlmostEqual(sum(final), 1.0, places=12)
        self.assertLess(final[1] + final[2] + final[3], 1e-9)


class StructureTests(unittest.TestCase):
    def test_closed_classes_and_transient(self):
        rows = {
            0: {1: 1.0},
            1: {0: 1.0},
            2: {0: 0.5, 3: 0.5},
            3: {3: 1.0},
        }
        chain = MarkovChain.from_rows(4, rows)
        self.assertEqual(chain.closed_classes(), [[0, 1], [3]])
        self.assertEqual(chain.transient_states(), [2])
        self.assertFalse(chain.is_irreducible())

    def test_irreducible_chain(self):
        rows = {i: {(i + 1) % 3: 1.0} for i in range(3)}
        chain = MarkovChain.from_rows(3, rows)
        self.assertTrue(chain.is_irreducible())
        self.assertEqual(chain.transient_states(), [])


class ExactAbsorptionTests(unittest.TestCase):
    def setUp(self):
        self.chain = MarkovChain.from_rows(5, gamblers_ruin_rows())
        self.report = self.chain.absorption_analysis(method="exact")

    def test_exact_absorption_probabilities(self):
        # P(hit 0 | start i) = (4 - i) / 4
        for i in range(1, 4):
            self.assertEqual(self.report.probabilities[i][0],
                             Fraction(4 - i, 4))
            self.assertEqual(self.report.probabilities[i][1],
                             Fraction(i, 4))

    def test_exact_expected_steps(self):
        # E[duration | start i] = i * (4 - i) for the fair game
        for i in range(1, 4):
            self.assertEqual(self.report.expected_steps[i],
                             Fraction(i * (4 - i)))

    def test_absorbing_states(self):
        self.assertEqual(self.report.expected_steps[0], 0)
        self.assertEqual(self.report.expected_steps[4], 0)
        self.assertEqual(self.report.probabilities[0], {0: Fraction(1)})
        self.assertEqual(self.report.probabilities[4], {1: Fraction(1)})

    def test_probabilities_sum_to_one_exactly(self):
        for i in range(1, 4):
            self.assertEqual(sum(self.report.probabilities[i].values()), 1)


class NonAbsorbingClosedClassTests(unittest.TestCase):
    def setUp(self):
        rows = {
            0: {1: 1.0},
            1: {0: 1.0},
            2: {0: HALF, 3: HALF},
            3: {3: 1.0},
        }
        self.chain = MarkovChain.from_rows(4, rows)
        self.report = self.chain.absorption_analysis(method="exact")

    def test_reachable_non_absorbing_class_reports_probability(self):
        self.assertEqual(self.report.absorbing, [False, True])
        self.assertEqual(self.report.probabilities[2][0], HALF)
        self.assertEqual(self.report.probabilities[2][1], HALF)

    def test_infinite_expectation_when_open_class_reachable(self):
        self.assertEqual(self.report.expected_steps[2], math.inf)
        self.assertEqual(self.report.expected_steps[0], math.inf)
        self.assertEqual(self.report.expected_steps[1], math.inf)
        self.assertEqual(self.report.expected_steps[3], 0)


class IterativeSolverTests(unittest.TestCase):
    def test_assess_convergence_logic(self):
        converged, warning = assess_convergence(1e-12, 1e-13, 1e-10)
        self.assertTrue(converged)
        self.assertIsNone(warning)
        converged, warning = assess_convergence(1e-5, 1e-13, 1e-10)
        self.assertFalse(converged)
        self.assertIn("not a convergence proof", warning)
        converged, warning = assess_convergence(1e-5, 1e-3, 1e-10)
        self.assertFalse(converged)
        self.assertIsNone(warning)

    def test_gauss_seidel_matches_exact(self):
        chain = MarkovChain.from_rows(5, gamblers_ruin_rows())
        solver = chain.transient_solver()
        a = solver._a
        b = [1.0, 1.0, 1.0]
        result = gauss_seidel(a, b, tol=1e-12)
        self.assertTrue(result.converged)
        self.assertLessEqual(result.residuals[-1], 1e-12)
        exact = exact_solve(
            [[Fraction(v) for v in row] for row in a],
            [Fraction(1)] * 3)
        for got, want in zip(result.x, exact):
            self.assertAlmostEqual(got, float(want), places=9)

    def test_error_bound_on_strictly_dominant_system(self):
        a = [[2.0, -0.5, 0.0], [-0.5, 2.0, -0.5], [0.0, -0.5, 2.0]]
        b = [1.0, 0.0, 1.0]
        result = gauss_seidel(a, b, tol=1e-13)
        self.assertTrue(result.converged)
        self.assertIsNotNone(result.error_bound)
        exact = exact_solve(
            [[Fraction(v) for v in row] for row in a],
            [Fraction(v) for v in b])
        true_error = max(abs(g - float(w))
                         for g, w in zip(result.x, exact))
        self.assertLessEqual(true_error, result.error_bound)

    def test_iterative_absorption_analysis_matches_exact(self):
        chain = MarkovChain.from_rows(5, gamblers_ruin_rows())
        exact = chain.absorption_analysis(method="exact")
        it = chain.absorption_analysis(method="iterative", tol=1e-12)
        self.assertTrue(it.systems)
        for label, result in it.systems.items():
            self.assertTrue(result.converged, label)
            self.assertLessEqual(result.residuals[-1], 1e-12)
        for i in range(1, 4):
            for ci in (0, 1):
                self.assertAlmostEqual(it.probabilities[i][ci],
                                       float(exact.probabilities[i][ci]),
                                       places=9)
            self.assertAlmostEqual(it.expected_steps[i],
                                   float(exact.expected_steps[i]), places=9)

    def test_power_iteration_stationary(self):
        rows = {i: {i: HALF, (i + 1) % 5: Fraction(1, 4),
                    (i - 1) % 5: Fraction(1, 4)} for i in range(5)}
        chain = MarkovChain.from_rows(5, rows)
        result = chain.stationary([1.0, 0.0, 0.0, 0.0, 0.0])
        self.assertTrue(result.converged)
        for v in result.distribution:
            self.assertAlmostEqual(v, 0.2, places=9)
        self.assertTrue(all(abs(e) < 1e-12 for e in result.mass_errors))


class RowRevisionReuseTests(unittest.TestCase):
    def setUp(self):
        self.chain = MarkovChain.from_rows(5, gamblers_ruin_rows())
        self.base_solver = self.chain.transient_solver()

    def test_row_revision_reuses_factorization(self):
        revised = self.chain.revised({1: {0: Fraction(3, 4),
                                          2: Fraction(1, 4)}})
        solver2 = revised.transient_solver(base=self.base_solver)
        self.assertTrue(solver2.reuses_factorization)
        got = solver2.solve([1.0, 1.0, 1.0])
        fresh = revised.transient_solver().solve([1.0, 1.0, 1.0])
        want = [Fraction(9, 5), Fraction(16, 5), Fraction(13, 5)]
        for x, xf, w in zip(got, fresh, want):
            self.assertAlmostEqual(x, float(w), places=9)
            self.assertAlmostEqual(x, xf, places=12)

    def test_multi_row_revision_reuse(self):
        revised = self.chain.revised({
            1: {0: Fraction(3, 4), 2: Fraction(1, 4)},
            3: {2: Fraction(1, 4), 4: Fraction(3, 4)},
        })
        solver2 = revised.transient_solver(base=self.base_solver)
        self.assertTrue(solver2.reuses_factorization)
        got = solver2.solve([1.0, 1.0, 1.0])
        fresh = revised.transient_solver().solve([1.0, 1.0, 1.0])
        for x, xf in zip(got, fresh):
            self.assertAlmostEqual(x, xf, places=12)

    def test_revision_with_bad_row_sum_raises(self):
        with self.assertRaises(ValidationError):
            self.chain.revised({1: {0: 0.9}})
        with self.assertRaises(ValidationError):
            self.chain.revised({1: {0: 0.4, 2: 0.4}})

    def test_revision_changing_transient_set_falls_back_to_fresh_lu(self):
        revised = self.chain.revised({1: {1: 1.0}})
        solver2 = revised.transient_solver(base=self.base_solver)
        self.assertFalse(solver2.reuses_factorization)
        got = solver2.solve([1.0] * len(solver2.transient))
        fresh = revised.transient_solver().solve(
            [1.0] * len(solver2.transient))
        for x, xf in zip(got, fresh):
            self.assertAlmostEqual(x, xf, places=12)

    def test_lu_method_in_analysis(self):
        chain = MarkovChain.from_rows(5, gamblers_ruin_rows())
        exact = chain.absorption_analysis(method="exact")
        lu = chain.absorption_analysis(method="lu")
        for i in range(1, 4):
            self.assertAlmostEqual(lu.expected_steps[i],
                                   float(exact.expected_steps[i]), places=9)


if __name__ == "__main__":
    unittest.main()
