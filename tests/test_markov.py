import math
import random
import unittest
from fractions import Fraction

from markov import (Chain, absorption_probabilities, expected_hitting_times,
                    conditional_expected_time, absorption_residuals,
                    hitting_time_residuals, probability_sum_witness,
                    check_strong_lumpability, lumped_chain,
                    estimate_absorption, sample_path, revise_row)

F = Fraction


def demo_chain():
    # 0,1 transient; 2,3 absorbing; 4 self-loop-1 trap (non-target).
    return Chain([
        [F(1, 2), F(1, 4), F(1, 8), F(1, 8), F(0)],
        [F(1, 3), F(1, 3), F(1, 6), F(0),    F(1, 6)],
        [F(0),    F(0),    F(1),    F(0),    F(0)],
        [F(0),    F(0),    F(0),    F(1),    F(0)],
        [F(0),    F(0),    F(0),    F(0),    F(1)],
    ])


class TestChainStructure(unittest.TestCase):
    def test_closed_classes_and_absorbing_states(self):
        c = demo_chain()
        self.assertEqual(sorted(map(sorted, c.closed_classes())),
                         [[2], [3], [4]])
        self.assertEqual(c.absorbing_states(), [2, 3, 4])
        self.assertEqual(c.transient_states(), [0, 1])

    def test_periodic_closed_class(self):
        c = Chain([[F(0), F(1)], [F(1), F(0)]])
        self.assertEqual(c.closed_classes(), [frozenset({0, 1})])
        self.assertEqual(c.absorbing_states(), [])

    def test_invalid_matrices_rejected(self):
        with self.assertRaises(ValueError):  # row sum != 1
            Chain([[F(1, 2), F(1, 4)], [F(0), F(1)]])
        with self.assertRaises(ValueError):  # negative entry
            Chain([[F(3, 2), F(-1, 2)], [F(0), F(1)]])
        with self.assertRaises(ValueError):  # not square
            Chain([[F(1), F(0), F(0)], [F(0), F(1), F(0)]])
        with self.assertRaises(ValueError):  # empty
            Chain([])


class TestExactAbsorption(unittest.TestCase):
    def setUp(self):
        self.c = demo_chain()

    def test_absorption_probabilities_exact(self):
        p = absorption_probabilities(self.c, {2})
        self.assertEqual(p[0], F(1, 2))
        self.assertEqual(p[1], F(1, 2))
        self.assertEqual(p[2], F(1))
        self.assertEqual(p[3], F(0))

    def test_self_loop_trap_never_absorbs_into_target(self):
        p = absorption_probabilities(self.c, {2})
        self.assertEqual(p[4], F(0))  # P[4][4] == 1, non-target

    def test_probabilities_over_absorbing_states_sum_to_one(self):
        expected = {0: (F(1, 2), F(1, 3), F(1, 6)),
                    1: (F(1, 2), F(1, 6), F(1, 3))}
        for start, (p2, p3, p4) in expected.items():
            parts, total = probability_sum_witness(self.c, start)
            self.assertEqual(total, 1)          # <= 1, here exactly 1
            self.assertEqual(parts[frozenset({2})], p2)
            self.assertEqual(parts[frozenset({3})], p3)
            self.assertEqual(parts[frozenset({4})], p4)

    def test_expected_hitting_times(self):
        t = expected_hitting_times(self.c)
        self.assertEqual(t[0], F(11, 3))
        self.assertEqual(t[1], F(10, 3))
        self.assertEqual(t[2], F(0))

    def test_unreachable_target_gives_infinite_expectation(self):
        t = expected_hitting_times(self.c, targets={2})
        self.assertEqual(t[3], math.inf)
        self.assertEqual(t[4], math.inf)  # self-loop-1 trap: never absorbed
        self.assertEqual(t[0], F(11, 3))

    def test_conditional_expected_time(self):
        cond = conditional_expected_time(self.c, {2})
        self.assertEqual(cond[0], F(11, 3))
        self.assertEqual(cond[1], F(10, 3))
        self.assertEqual(cond[2], F(0))

    def test_conditional_expectation_undefined_not_zero(self):
        cond = conditional_expected_time(self.c, {2})
        # hit probability 0 -> undefined (None), must never be reported as 0
        self.assertIsNone(cond[3])
        self.assertIsNone(cond[4])
        self.assertNotEqual(cond[3], 0)

    def test_residuals_are_exactly_zero(self):
        p = absorption_probabilities(self.c, {2})
        self.assertTrue(all(r == 0
                            for r in absorption_residuals(self.c, {2}, p).values()))
        t = expected_hitting_times(self.c)
        res = hitting_time_residuals(self.c, {2, 3, 4}, t)
        self.assertEqual(set(res), {0, 1})
        self.assertTrue(all(r == 0 for r in res.values()))

    def test_target_must_be_closed_class(self):
        with self.assertRaises(ValueError):
            absorption_probabilities(self.c, {0, 1})


class TestSimulation(unittest.TestCase):
    def test_seed_and_sample_size_recorded_and_labeled(self):
        c = demo_chain()
        rng = random.Random(42)  # injected by the caller
        est = estimate_absorption(c, 0, rng, 5000, seed=42)
        self.assertEqual(est.kind, "empirical")
        self.assertEqual(est.seed, 42)
        self.assertEqual(est.n_samples, 5000)
        self.assertAlmostEqual(est.absorption_frequencies[2], 0.5, delta=0.05)
        self.assertAlmostEqual(est.absorption_frequencies[3], 1 / 3, delta=0.05)
        self.assertAlmostEqual(est.absorption_frequencies[4], 1 / 6, delta=0.05)
        self.assertAlmostEqual(est.mean_steps, 11 / 3, delta=0.2)

    def test_reproducible_with_same_seed(self):
        c = demo_chain()
        a = estimate_absorption(c, 0, random.Random(7), 200, seed=7)
        b = estimate_absorption(c, 0, random.Random(7), 200, seed=7)
        self.assertEqual(a.absorption_counts, b.absorption_counts)

    def test_self_loop_state_absorbs_immediately(self):
        c = demo_chain()
        state, steps = sample_path(c, 4, random.Random(1))
        self.assertEqual((state, steps), (4, 0))


class TestLumping(unittest.TestCase):
    def setUp(self):
        self.c = demo_chain()

    def test_invalid_partition_gives_exact_witness(self):
        ok, w = check_strong_lumpability(self.c, [{0, 1}, {2}, {3}, {4}])
        self.assertFalse(ok)
        self.assertEqual((w.state_i, w.state_j), (0, 1))
        self.assertEqual(w.group, 0)
        self.assertEqual(w.target_group, 0)
        self.assertEqual(w.rate_i, F(3, 4))   # exact Fractions, not floats
        self.assertEqual(w.rate_j, F(2, 3))
        self.assertIsInstance(w.rate_i, Fraction)

    def test_blind_compression_refused(self):
        with self.assertRaises(ValueError):
            lumped_chain(self.c, [{0, 1}, {2}, {3}, {4}])

    def test_valid_partition_compresses(self):
        groups = [{0}, {1}, {2}, {3, 4}]
        ok, w = check_strong_lumpability(self.c, groups)
        self.assertTrue(ok)
        self.assertIsNone(w)
        lc = lumped_chain(self.c, groups)
        self.assertEqual(lc.n, 4)
        self.assertEqual(lc.matrix[3], [F(0), F(0), F(0), F(1)])
        # group-level absorption probability matches the original chain
        p_lumped = absorption_probabilities(lc, {2})
        p_orig = absorption_probabilities(self.c, {2})
        self.assertEqual(p_lumped[0], p_orig[0])

    def test_non_partition_rejected(self):
        with self.assertRaises(ValueError):
            check_strong_lumpability(self.c, [{0, 1}, {2}, {3}])  # missing 4
        with self.assertRaises(ValueError):
            check_strong_lumpability(self.c, [{0, 1}, {1, 2}, {3}, {4}])


class TestRowRevision(unittest.TestCase):
    def setUp(self):
        self.c = demo_chain()

    def test_incremental_matches_full_resolve(self):
        delta = [F(0), F(-1, 8), F(1, 8), F(0), F(0)]
        rep = revise_row(self.c, 0, delta)
        self.assertEqual(rep.old_row_sum, 1)
        self.assertEqual(rep.delta_sum, 0)
        self.assertEqual(rep.new_row_sum, 1)
        self.assertTrue(rep.bounds_ok)
        self.assertEqual(rep.min_entry, F(0))
        self.assertEqual(rep.max_entry, F(1, 2))
        self.assertEqual(rep.affected_states, frozenset({0, 1, 2, 3, 4}))
        self.assertEqual(sorted(map(sorted, rep.affected_closed_classes)),
                         [[2], [3], [4]])
        # exact revised values, checked by hand
        self.assertEqual(rep.incremental_probs[frozenset({2})][0], F(9, 14))
        self.assertEqual(rep.incremental_probs[frozenset({2})][1], F(4, 7))
        # state-by-state agreement with the full re-solve
        self.assertEqual(rep.max_abs_diff, 0)
        for cls in rep.affected_closed_classes:
            for s in range(self.c.n):
                incr, full = rep.state_comparison[(cls, s)]
                self.assertEqual(incr, full)
        # residual evidence: every equation of the revised chain holds exactly
        for res in rep.residuals.values():
            self.assertTrue(all(r == 0 for r in res.values()))

    def test_unaffected_region_uses_boundary_values(self):
        c = Chain([
            [F(1, 2), F(1, 2), F(0),    F(0)],
            [F(1),    F(0),    F(0),    F(0)],
            [F(0),    F(0),    F(1, 2), F(1, 2)],
            [F(0),    F(0),    F(0),    F(1)],
        ])
        rep = revise_row(c, 2, [F(0), F(0), F(-1, 4), F(1, 4)])
        self.assertEqual(rep.affected_states, frozenset({2, 3}))
        self.assertEqual(sorted(map(sorted, rep.affected_closed_classes)), [[3]])
        incr = rep.incremental_probs[frozenset({3})]
        self.assertEqual(incr[2], F(1))           # recomputed
        self.assertEqual(incr[0], F(0))           # boundary from old chain
        self.assertEqual(incr[1], F(0))
        self.assertEqual(rep.max_abs_diff, 0)

    def test_invalid_revision_rejected(self):
        with self.assertRaises(ValueError):  # row sum broken
            revise_row(self.c, 0, [F(1, 8), F(0), F(0), F(0), F(0)])
        with self.assertRaises(ValueError):  # negative probability
            revise_row(self.c, 0, [F(0), F(-1, 2), F(1, 2), F(0), F(0)])
        with self.assertRaises(ValueError):  # wrong delta length
            revise_row(self.c, 0, [F(0)])


if __name__ == "__main__":
    unittest.main()
