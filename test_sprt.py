import math
import random
import unittest

from sprt import (BinomialSPRT, Decision, EvidenceProcess, HypothesisError,
                  SharedBudget, decide_from_log_lr,
                  exact_operating_characteristics, simulate)


class TestBoundaryRule(unittest.TestCase):
    def test_exact_boundary_no_epsilon(self):
        # Equality triggers acceptance; one ulp inside does not.
        self.assertIs(decide_from_log_lr(2.0, 2.0, -3.0), Decision.ACCEPT_H1)
        self.assertIs(decide_from_log_lr(-3.0, 2.0, -3.0), Decision.ACCEPT_H0)
        just_below = math.nextafter(2.0, -math.inf)
        just_above = math.nextafter(-3.0, math.inf)
        self.assertIs(decide_from_log_lr(just_below, 2.0, -3.0),
                      Decision.CONTINUE)
        self.assertIs(decide_from_log_lr(just_above, 2.0, -3.0),
                      Decision.CONTINUE)

    def test_invalid_config(self):
        for args in [(0.5, 0.5, 0.05, 0.05, 10),      # p0 == p1
                     (-0.1, 0.5, 0.05, 0.05, 10),      # p0 out of range
                     (0.3, 0.5, 0.0, 0.05, 10),        # alpha = 0
                     (0.3, 0.5, 0.6, 0.6, 10),         # alpha + beta >= 1
                     (0.3, 0.5, 0.05, 0.05, 0)]:       # n_max < 1
            with self.assertRaises(HypothesisError):
                BinomialSPRT(*args)


class TestZeroLikelihood(unittest.TestCase):
    def test_p0_zero_single_success_decides(self):
        s = BinomialSPRT(0.0, 0.6, 0.05, 0.05, 100)
        self.assertIs(s.step(1), Decision.ACCEPT_H1)  # +inf log-LR
        self.assertEqual(s.n, 1)

    def test_p1_one_single_failure_decides(self):
        s = BinomialSPRT(0.4, 1.0, 0.05, 0.05, 100)
        self.assertIs(s.step(0), Decision.ACCEPT_H0)  # -inf log-LR
        self.assertEqual(s.n, 1)

    def test_p0_zero_failures_continue(self):
        s = BinomialSPRT(0.0, 0.6, 0.05, 0.05, 100)
        d = s.step(0)
        self.assertIn(d, (Decision.CONTINUE, Decision.ACCEPT_H0))
        self.assertTrue(math.isfinite(s.log_lr))

    def test_degenerate_sampling_p_equals_one(self):
        # p = 1: all successes, deterministic path; exact E[n] must match
        # the closed-form ceil(log_a / inc1).
        p0, p1, alpha, beta, n_max = 0.3, 0.7, 0.05, 0.05, 500
        res = exact_operating_characteristics(p0, p1, alpha, beta, n_max, p=1.0)
        probe = BinomialSPRT(p0, p1, alpha, beta, n_max)
        expected = math.ceil(probe.log_a / probe._inc1)
        self.assertAlmostEqual(res.p_accept_h1, 1.0)
        self.assertAlmostEqual(res.expected_n, float(expected))


class TestUndecidedAndStickiness(unittest.TestCase):
    def test_undecided_at_n_max(self):
        # Nearly identical hypotheses with huge bounds: never crosses.
        s = BinomialSPRT(0.5, 0.51, 1e-9, 1e-9, 5)
        rng = random.Random(7)
        last = None
        for _ in range(5):
            last = s.step(1 if rng.random() < 0.5 else 0)
        self.assertIs(last, Decision.UNDECIDED)
        # Terminal states are sticky and consume nothing further.
        self.assertIs(s.step(1), Decision.UNDECIDED)
        self.assertEqual(s.n, 5)


class TestCheckpoint(unittest.TestCase):
    def test_resume_consistency(self):
        rng = random.Random(123)
        data = [1 if rng.random() < 0.6 else 0 for _ in range(400)]
        a = BinomialSPRT(0.4, 0.6, 0.05, 0.05, 400)
        for x in data[:137]:
            a.step(x)
        b = BinomialSPRT.from_snapshot(a.snapshot())
        for x in data[137:]:
            da, db = a.step(x), b.step(x)
            self.assertIs(da, db)
        self.assertEqual(a.n, b.n)
        self.assertEqual(a.k, b.k)
        self.assertEqual(a.log_lr, b.log_lr)  # exact, not recomputed
        self.assertIs(a.status, b.status)

    def test_snapshot_roundtrip_fields(self):
        s = BinomialSPRT(0.4, 0.6, 0.05, 0.05, 50)
        for x in (1, 0, 1, 1):
            s.step(x)
        snap = s.snapshot()
        r = BinomialSPRT.from_snapshot(snap)
        self.assertEqual((r.n, r.k, r.log_lr, r.status),
                         (s.n, s.k, s.log_lr, s.status))


class TestSharedBudget(unittest.TestCase):
    def test_shared_budget_no_double_count(self):
        budget = SharedBudget(10)
        s1 = BinomialSPRT(0.4, 0.6, 1e-6, 1e-6, 1000, budget=budget, label="a")
        s2 = BinomialSPRT(0.4, 0.6, 1e-6, 1e-6, 1000, budget=budget, label="b")
        rng = random.Random(99)
        for _ in range(10):
            (s1 if rng.random() < 0.5 else s2).step(rng.random() < 0.5)
        self.assertEqual(s1.n + s2.n, 10)
        self.assertEqual(budget.remaining, 0)
        n1_before = s1.n
        # Interruption: budget exhausted, state untouched.
        self.assertIs(s1.step(1), Decision.BUDGET_EXHAUSTED)
        self.assertEqual(s1.n, n1_before)
        # Resume after refill continues from the same state.
        budget.refill(3)
        self.assertIsNot(s1.step(1), Decision.BUDGET_EXHAUSTED)
        self.assertEqual(s1.n, n1_before + 1)
        self.assertEqual(budget.consumed, 11)
        self.assertEqual(budget.remaining, 2)


class TestEvidenceProcess(unittest.TestCase):
    def test_query_reflects_stream(self):
        s = BinomialSPRT(0.4, 0.6, 0.05, 0.05, 100, label="ev")
        for x in (1, 1, 0, 1):
            s.step(x)
        q = EvidenceProcess(s).query()
        self.assertEqual(q["n"], 4)
        self.assertEqual(q["successes"], 3)
        self.assertAlmostEqual(q["e_value"], math.exp(s.log_lr))
        self.assertEqual(q["decision"], s.status.name)
        self.assertIn("declared stopping rule", q["guarantee"])


class TestExactRecursion(unittest.TestCase):
    def test_probabilities_sum_to_one(self):
        res = exact_operating_characteristics(0.4, 0.6, 0.05, 0.05, 200, p=0.5)
        total = res.p_accept_h0 + res.p_accept_h1 + res.p_undecided
        self.assertAlmostEqual(total, 1.0, places=12)

    def test_wald_error_bounds_approximately(self):
        p0, p1, alpha, beta, n_max = 0.4, 0.6, 0.05, 0.05, 1000
        r0 = exact_operating_characteristics(p0, p1, alpha, beta, n_max, p=p0)
        r1 = exact_operating_characteristics(p0, p1, alpha, beta, n_max, p=p1)
        self.assertLessEqual(r0.type1_error, alpha + 0.02)
        self.assertLessEqual(r1.type2_error, beta + 0.02)
        self.assertAlmostEqual(r0.p_undecided, 0.0, places=9)
        self.assertAlmostEqual(r1.p_undecided, 0.0, places=9)

    def test_exact_matches_deterministic_trajectory(self):
        # p = 0: all failures, accept H0 after a fixed number of steps.
        p0, p1, alpha, beta, n_max = 0.3, 0.7, 0.05, 0.05, 500
        res = exact_operating_characteristics(p0, p1, alpha, beta, n_max, p=0.0)
        probe = BinomialSPRT(p0, p1, alpha, beta, n_max)
        expected = math.ceil(-probe.log_b / -probe._inc0)
        self.assertAlmostEqual(res.p_accept_h0, 1.0)
        self.assertAlmostEqual(res.expected_n, float(expected))


class TestSimulation(unittest.TestCase):
    def test_empirical_close_to_exact(self):
        p0, p1, alpha, beta, n_max = 0.4, 0.6, 0.05, 0.05, 300
        exact = exact_operating_characteristics(p0, p1, alpha, beta, n_max,
                                                p=0.6)
        rng = random.Random(20260928)
        emp = simulate(p0, p1, alpha, beta, n_max, p=0.6, n_rep=4000,
                       rng=rng, seed=20260928)
        self.assertEqual(emp.seed, 20260928)
        self.assertEqual(emp.label, "empirical (Monte Carlo)")
        self.assertEqual(exact.label, "exact (discrete recursion)")
        self.assertAlmostEqual(emp.p_accept_h1, exact.p_accept_h1, delta=0.02)
        self.assertAlmostEqual(emp.expected_n, exact.expected_n, delta=1.5)
        self.assertGreater(emp.total_samples, 0)

    def test_direction_of_decisions(self):
        rng0, rng1 = random.Random(1), random.Random(2)
        r0 = simulate(0.4, 0.6, 0.05, 0.05, 300, p=0.4, n_rep=500, rng=rng0)
        r1 = simulate(0.4, 0.6, 0.05, 0.05, 300, p=0.6, n_rep=500, rng=rng1)
        self.assertGreater(r0.p_accept_h0, 0.9)
        self.assertGreater(r1.p_accept_h1, 0.9)


if __name__ == "__main__":
    unittest.main()
