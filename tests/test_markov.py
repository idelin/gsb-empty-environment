"""Unittest suite for the markov package (standard library only)."""

import math
import os
import sys
import unittest
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from markov import (
    Convention,
    DistributionError,
    IndexOutOfRangeError,
    LineSumError,
    MarkovChain,
    MissingLineError,
    NegativeProbabilityError,
    NotConvergedError,
    SparseTransitionMatrix,
    assess_convergence,
)


def gamblers_ruin(n, p=0.5):
    """States 0..n; 0 and n absorbing; interior moves +/-1."""
    lines = {0: {0: 1.0}, n: {n: 1.0}}
    for i in range(1, n):
        lines[i] = {i - 1: 1.0 - p, i + 1: p}
    return SparseTransitionMatrix(n + 1, lines)


def chain_with_nonabsorbing_class():
    """0,1 absorbing; {2,3} closed period-2 class; 4 transient."""
    return SparseTransitionMatrix(5, {
        0: {0: 1.0},
        1: {1: 1.0},
        2: {3: 1.0},
        3: {2: 1.0},
        4: {0: 0.5, 2: 0.5},
    })


class TestValidation(unittest.TestCase):
    def test_missing_line_raises(self):
        with self.assertRaises(MissingLineError):
            SparseTransitionMatrix(3, {0: {0: 1.0}, 1: {1: 1.0}})

    def test_line_sum_mismatch_raises_not_normalized(self):
        with self.assertRaises(LineSumError):
            SparseTransitionMatrix(2, {0: {0: 0.3, 1: 0.3}, 1: {1: 1.0}})

    def test_line_sum_above_one_raises(self):
        with self.assertRaises(LineSumError):
            SparseTransitionMatrix(2, {0: {0: 0.6, 1: 0.6}, 1: {1: 1.0}})

    def test_negative_entry_raises(self):
        with self.assertRaises(NegativeProbabilityError):
            SparseTransitionMatrix(2, {0: {0: 1.5, 1: -0.5}, 1: {1: 1.0}})

    def test_index_out_of_range_raises(self):
        with self.assertRaises(IndexOutOfRangeError):
            SparseTransitionMatrix(2, {0: {0: 0.5, 5: 0.5}, 1: {1: 1.0}})
        with self.assertRaises(IndexOutOfRangeError):
            SparseTransitionMatrix(2, {0: {0: 1.0}, 1: {1: 1.0}, 7: {7: 1.0}})

    def test_explicit_size_required(self):
        with self.assertRaises(ValueError):
            SparseTransitionMatrix(0, {})

    def test_row_revision_validated(self):
        m = gamblers_ruin(4)
        chain = MarkovChain(m)
        with self.assertRaises(LineSumError):
            chain.revise_line(2, {1: 0.9, 3: 0.9})
        # chain left unchanged after failed revision
        self.assertEqual(m.line(2), {1: 0.5, 3: 0.5})

    def test_distribution_validation(self):
        chain = MarkovChain(gamblers_ruin(4))
        with self.assertRaises(DistributionError):
            chain.check_distribution([0.5, 0.6, 0.0, 0.0, 0.0])
        with self.assertRaises(DistributionError):
            chain.check_distribution([1.5, -0.5, 0.0, 0.0, 0.0])
        with self.assertRaises(DistributionError):
            chain.check_distribution([1.0, 0.0])
        chain.check_distribution([0.2, 0.2, 0.2, 0.2, 0.2])  # no raise


class TestConventions(unittest.TestCase):
    def test_row_and_column_conventions_unify(self):
        lines = {0: {0: 1.0}, 1: {0: 0.5, 2: 0.5}, 2: {1: 0.25, 2: 0.75}}
        row_m = SparseTransitionMatrix(3, lines, convention=Convention.ROW)
        col_m = SparseTransitionMatrix(3, lines, convention=Convention.COLUMN)
        self.assertEqual(row_m.lines, col_m.lines)
        d0 = [0.1, 0.3, 0.6]
        self.assertEqual(MarkovChain(row_m).apply(d0), MarkovChain(col_m).apply(d0))

    def test_convention_attribute(self):
        m = SparseTransitionMatrix(1, {0: {0: 1.0}}, convention=Convention.COLUMN)
        self.assertEqual(m.convention, Convention.COLUMN)


class TestClassification(unittest.TestCase):
    def test_closed_classes_and_absorbing_states(self):
        chain = MarkovChain(chain_with_nonabsorbing_class())
        summary = chain.diagnostics()
        self.assertFalse(summary["irreducible"])
        self.assertEqual(sorted(summary["absorbing_states"]), [0, 1])
        self.assertEqual(summary["non_absorbing_closed_classes"], [[2, 3]])
        self.assertEqual(summary["transient_states"], [4])

    def test_irreducible_cycle(self):
        m = SparseTransitionMatrix(3, {
            0: {1: 1.0}, 1: {2: 1.0}, 2: {0: 1.0},
        })
        chain = MarkovChain(m)
        self.assertTrue(chain.irreducible)
        self.assertEqual(chain.structure.transient_states, ())

    def test_absorbing_self_loop_detected(self):
        chain = MarkovChain(gamblers_ruin(4))
        absorbing = [
            cc.states[0]
            for cc in chain.structure.closed_classes
            if cc.is_absorbing_state
        ]
        self.assertEqual(sorted(absorbing), [0, 4])


class TestPropagation(unittest.TestCase):
    def test_mass_conservation_with_absorbing_states(self):
        chain = MarkovChain(gamblers_ruin(4))
        records = list(chain.propagate([0.0, 0.0, 1.0, 0.0, 0.0], 200))
        self.assertEqual(len(records), 200)
        for rec in records:
            self.assertAlmostEqual(rec.mass_error, 0.0, places=12)
        final = records[-1].distribution
        # mass is never lost: it accumulates at the absorbing states
        self.assertAlmostEqual(final[0] + final[4], 1.0, places=6)
        self.assertAlmostEqual(final[4], 0.5, places=6)  # ruin prob from 2

    def test_propagation_records_increment(self):
        chain = MarkovChain(gamblers_ruin(4))
        rec = next(chain.propagate([0.0, 0.0, 1.0, 0.0, 0.0], 1))
        self.assertEqual(rec.step, 1)
        self.assertAlmostEqual(rec.increment, 1.0)  # state 2: 1.0 -> 0.0


class TestStationaryIterative(unittest.TestCase):
    def test_converges_to_uniform_on_cycle(self):
        n = 5
        lines = {i: {(i + 1) % n: 0.6, (i - 1) % n: 0.4} for i in range(n)}
        chain = MarkovChain(SparseTransitionMatrix(n, lines))
        result = chain.stationary_iterative([1.0, 0, 0, 0, 0], tol=1e-12)
        self.assertTrue(result.converged)
        self.assertLessEqual(result.residual_norm, 1e-12)
        for v in result.distribution:
            self.assertAlmostEqual(v, 0.2, places=9)

    def test_not_converged_reports_residual(self):
        n = 5
        lines = {i: {(i + 1) % n: 0.6, (i - 1) % n: 0.4} for i in range(n)}
        chain = MarkovChain(SparseTransitionMatrix(n, lines))
        result = chain.stationary_iterative([1.0, 0, 0, 0, 0], tol=1e-15,
                                            max_iter=2)
        self.assertFalse(result.converged)
        self.assertGreater(result.residual_norm, 1e-15)

    def test_small_increment_is_not_convergence_proof(self):
        converged, warning = assess_convergence(1e-9, 1e-13, 1e-12)
        self.assertFalse(converged)
        self.assertIn("not proof of convergence", warning)
        converged, warning = assess_convergence(1e-13, 1e-13, 1e-12)
        self.assertTrue(converged)
        self.assertEqual(warning, "")


class TestAbsorptionExact(unittest.TestCase):
    def test_gamblers_ruin_probabilities_exact(self):
        chain = MarkovChain(gamblers_ruin(4))
        res = chain.absorption(method="exact")
        # class 0 -> state 0, class 1 -> state 4; P(hit 4 | i) = i/4
        for i in (1, 2, 3):
            self.assertEqual(res.probabilities[i][1], Fraction(i, 4))
            self.assertEqual(res.probabilities[i][0], Fraction(4 - i, 4))

    def test_gamblers_ruin_expected_steps_exact(self):
        chain = MarkovChain(gamblers_ruin(4))
        res = chain.absorption(method="exact")
        for i in (1, 2, 3):
            self.assertEqual(res.expected_steps[i], Fraction(i * (4 - i)))
        self.assertEqual(res.expected_steps[0], 0)
        self.assertEqual(res.expected_steps[4], 0)

    def test_dense_float_matches_exact(self):
        chain = MarkovChain(gamblers_ruin(6, p=0.4))
        exact = chain.absorption(method="exact")
        dense = chain.absorption(method="dense")
        for s in exact.probabilities:
            for c, p in exact.probabilities[s].items():
                self.assertAlmostEqual(dense.probabilities[s][c], float(p),
                                       places=12)
        for s, t in exact.expected_steps.items():
            self.assertAlmostEqual(dense.expected_steps[s], float(t), places=9)


class TestNonAbsorbingClosedClass(unittest.TestCase):
    def test_probabilities_reported_and_expectation_infinite(self):
        chain = MarkovChain(chain_with_nonabsorbing_class())
        for method in ("dense", "exact", "iterative"):
            res = chain.absorption(method=method)
            # classes: 0 -> {0}, 1 -> {1}, 2 -> {2,3}
            self.assertAlmostEqual(res.probabilities[4][0], 0.5)
            self.assertAlmostEqual(res.probabilities[4][2], 0.5)
            self.assertEqual(res.expected_steps[4], math.inf)
            self.assertEqual(res.expected_steps[2], math.inf)
            self.assertEqual(res.expected_steps[0], 0)
            self.assertEqual(
                res.diagnostics["non_absorbing_closed_classes"], [[2, 3]]
            )

    def test_irreducible_chain_all_infinite_expectation(self):
        m = SparseTransitionMatrix(3, {
            0: {1: 1.0}, 1: {2: 1.0}, 2: {0: 1.0},
        })
        res = MarkovChain(m).absorption()
        self.assertEqual(res.transient_states, ())
        for s in range(3):
            self.assertEqual(res.expected_steps[s], math.inf)
            self.assertEqual(res.probabilities[s], {0: 1})


class TestIterativeSolver(unittest.TestCase):
    def test_iterative_matches_dense_with_residual_certificate(self):
        chain = MarkovChain(gamblers_ruin(40))
        dense = chain.absorption(method="dense")
        it = chain.absorption(method="iterative", tol=1e-12)
        for s in dense.probabilities:
            for c, p in dense.probabilities[s].items():
                self.assertAlmostEqual(it.probabilities[s][c], p, places=9)
        for s in dense.expected_steps:
            self.assertAlmostEqual(it.expected_steps[s],
                                   dense.expected_steps[s], places=6)
        for rhs in it.diagnostics["rhs"].values():
            self.assertTrue(rhs["converged"])
            self.assertLessEqual(rhs["residual_norm"], 1e-12)

    def test_not_converged_raises(self):
        chain = MarkovChain(gamblers_ruin(40))
        with self.assertRaises(NotConvergedError):
            chain.absorption(method="iterative", tol=1e-14, max_iter=1)


class TestRowRevisionReuse(unittest.TestCase):
    def test_revision_reuses_factorization_and_matches_fresh(self):
        lines = {0: {0: 1.0}, 4: {4: 1.0},
                 1: {0: 0.4, 2: 0.6}, 2: {1: 0.4, 3: 0.6}, 3: {2: 0.4, 4: 0.6}}
        chain = MarkovChain(SparseTransitionMatrix(5, lines))
        before = chain.absorption(method="dense")
        self.assertFalse(any(before.diagnostics["reused_factorization"]))
        chain.revise_line(2, {1: 0.5, 3: 0.5})
        after = chain.absorption(method="dense")
        self.assertTrue(any(after.diagnostics["reused_factorization"]))
        # compare against a freshly built chain
        fresh_lines = dict(lines)
        fresh_lines[2] = {1: 0.5, 3: 0.5}
        fresh = MarkovChain(SparseTransitionMatrix(5, fresh_lines))
        reference = fresh.absorption(method="dense")
        for s in reference.probabilities:
            for c, p in reference.probabilities[s].items():
                self.assertAlmostEqual(after.probabilities[s][c], p, places=12)
            self.assertAlmostEqual(after.expected_steps[s],
                                   reference.expected_steps[s], places=9)

    def test_revision_changing_structure_drops_solver(self):
        lines = {0: {0: 1.0}, 1: {0: 0.5, 2: 0.5}, 2: {1: 0.5, 2: 0.5}}
        chain = MarkovChain(SparseTransitionMatrix(3, lines))
        chain.absorption(method="dense")
        chain.revise_line(2, {2: 1.0})  # state 2 becomes absorbing
        res = chain.absorption(method="dense")
        self.assertEqual(res.transient_states, (1,))
        self.assertAlmostEqual(res.probabilities[1][0], 0.5)
        self.assertAlmostEqual(res.probabilities[1][1], 0.5)


class TestLargeModel(unittest.TestCase):
    def test_large_random_walk_iterative(self):
        n = 400  # states 0..400, ends absorbing
        chain = MarkovChain(gamblers_ruin(n))
        omega = 2.0 / (1.0 + math.sin(math.pi / n))
        res = chain.absorption(method="iterative", tol=1e-10, omega=omega,
                               max_iter=200000)
        mid = n // 2
        self.assertAlmostEqual(res.probabilities[mid][1], 0.5, places=8)
        self.assertAlmostEqual(res.expected_steps[mid], mid * (n - mid),
                               delta=0.5)
        for rhs in res.diagnostics["rhs"].values():
            self.assertTrue(rhs["converged"])
            self.assertLessEqual(rhs["residual_norm"], 1e-10)

    def test_large_model_mass_conservation(self):
        n = 400
        chain = MarkovChain(gamblers_ruin(n))
        dist = [0.0] * (n + 1)
        dist[n // 2] = 1.0
        worst = 0.0
        for rec in chain.propagate(dist, 300):
            worst = max(worst, abs(rec.mass_error))
        self.assertLess(worst, 1e-9)


if __name__ == "__main__":
    unittest.main()
