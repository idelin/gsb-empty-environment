import random
import threading
import unittest
from fractions import Fraction

from rejection_sampling import (
    CALLER_CERTIFIED,
    EXACT_FINITE_VERIFICATION,
    CertificateRequiredError,
    EnvelopeViolationError,
    PartitionedEnvelope,
    RejectionSampler,
    SamplerState,
    check_rejection_counts,
    constant_envelope,
    exact_finite_distribution,
    expected_acceptance_probability,
    verify_sample_distribution,
)

DOMAIN = list(range(10))
WEIGHTS = [1, 2, 3, 4, 5, 5, 4, 3, 2, 1]  # total 30


def target(x):
    return WEIGHTS[x]


def proposal_pdf(x):
    return 0.1


def proposal_sample(rng):
    return rng.randrange(10)


def make_envelope(bound=50.0, **kwargs):
    if "certificate" not in kwargs:
        kwargs.setdefault("exact_domain", DOMAIN)
    return constant_envelope(target, proposal_pdf, bound, **kwargs)


def make_sampler(seed=42, bound=50.0, **env_kwargs):
    env = make_envelope(bound, **env_kwargs)
    rng = random.Random(seed)
    return RejectionSampler(
        target=target,
        proposal_pdf=proposal_pdf,
        proposal_sample=proposal_sample,
        envelope=env,
        rng=rng,
        seed=seed,
    )


class TestEnvelopeCertificates(unittest.TestCase):
    def test_partition_requires_certificate_or_exact_verification(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        with self.assertRaises(CertificateRequiredError):
            env.add_partition(0.0, 5.0, 50.0)

    def test_caller_certificate_recorded(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        cert = env.add_partition(0.0, 10.0, 50.0, certificate={"proof": "by caller"})
        self.assertEqual(cert.method, CALLER_CERTIFIED)
        self.assertEqual(env.bound_at(3), 50.0)

    def test_exact_verification_passes_with_valid_bound(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        cert = env.add_partition(0.0, 10.0, 50.0, exact_domain=DOMAIN)
        self.assertEqual(cert.method, EXACT_FINITE_VERIFICATION)
        self.assertEqual(cert.evidence["max_ratio"], 50.0)

    def test_exact_verification_rejects_invalid_bound(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        with self.assertRaises(EnvelopeViolationError):
            env.add_partition(0.0, 10.0, 49.9, exact_domain=DOMAIN)

    def test_overlapping_partitions_rejected(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        env.add_partition(0.0, 5.0, 50.0, certificate="c")
        with self.assertRaises(ValueError):
            env.add_partition(4.0, 8.0, 50.0, certificate="c")

    def test_uncovered_point_raises(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        env.add_partition(0.0, 5.0, 50.0, certificate="c")
        with self.assertRaises(KeyError):
            env.bound_at(7)


class TestAdaptiveRefinement(unittest.TestCase):
    def test_refine_tiles_parent_and_certifies_each_subpartition(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        env.add_partition(0.0, 10.0, 50.0, certificate="coarse")
        certs = env.refine(
            0.0,
            10.0,
            [
                {"lo": 0.0, "hi": 5.0, "bound": 50.0, "exact_domain": DOMAIN},
                {"lo": 5.0, "hi": 10.0, "bound": 50.0, "exact_domain": DOMAIN},
            ],
        )
        self.assertEqual(len(certs), 2)
        self.assertTrue(all(c.method == EXACT_FINITE_VERIFICATION for c in certs))
        self.assertEqual(env.bound_at(2), 50.0)
        self.assertEqual(env.bound_at(8), 50.0)

    def test_failed_refinement_is_atomic(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        env.add_partition(0.0, 10.0, 50.0, certificate="coarse")
        with self.assertRaises(EnvelopeViolationError):
            env.refine(
                0.0,
                10.0,
                [
                    {"lo": 0.0, "hi": 5.0, "bound": 50.0, "exact_domain": DOMAIN},
                    {"lo": 5.0, "hi": 10.0, "bound": 10.0, "exact_domain": DOMAIN},
                ],
            )
        # Original partition untouched.
        self.assertEqual(env.bound_at(8), 50.0)
        self.assertEqual(env.partitions, [(0.0, 10.0)])

    def test_refinement_requires_tiling(self):
        env = PartitionedEnvelope(target, proposal_pdf)
        env.add_partition(0.0, 10.0, 50.0, certificate="coarse")
        with self.assertRaises(ValueError):
            env.refine(
                0.0,
                10.0,
                [{"lo": 0.0, "hi": 4.0, "bound": 50.0, "certificate": "c"}],
            )


class TestEnvelopeViolationAtSampleTime(unittest.TestCase):
    def test_violation_raises_immediately_and_no_samples_returned(self):
        # Caller lies: certificate claims bound 40 but true max ratio is 50.
        sampler = make_sampler(seed=7, bound=40.0, certificate={"false": "claim"})
        with self.assertRaises(EnvelopeViolationError):
            sampler.sample(100, budget=10_000)

    def test_violation_is_not_clamped_to_one(self):
        sampler = make_sampler(seed=7, bound=40.0, certificate="lie")
        try:
            sampler.sample(100, budget=10_000)
        except EnvelopeViolationError as exc:
            self.assertIn("aborting", str(exc))
        else:
            self.fail("expected EnvelopeViolationError instead of silent clamping")


class TestBudgetAndCancellation(unittest.TestCase):
    def test_budget_exhaustion_returns_partial_and_remaining(self):
        sampler = make_sampler(seed=1)
        result = sampler.sample(1000, budget=10)
        self.assertEqual(result.reason, "budget_exhausted")
        self.assertFalse(result.completed)
        self.assertEqual(result.attempts, 10)
        self.assertLessEqual(result.accepted, 10)
        self.assertEqual(result.remaining, 1000 - result.accepted)

    def test_zero_budget_returns_no_samples(self):
        sampler = make_sampler(seed=1)
        result = sampler.sample(5, budget=0)
        self.assertEqual(result.reason, "budget_exhausted")
        self.assertEqual(result.samples, [])
        self.assertEqual(result.remaining, 5)

    def test_cancellation_event_stops_sampling(self):
        event = threading.Event()
        calls = {"n": 0}

        def counting_proposal(rng):
            calls["n"] += 1
            if calls["n"] >= 7:
                event.set()
            return proposal_sample(rng)

        env = make_envelope(certificate="c")
        sampler = RejectionSampler(
            target=target,
            proposal_pdf=proposal_pdf,
            proposal_sample=counting_proposal,
            envelope=env,
            rng=random.Random(3),
            seed=3,
        )
        result = sampler.sample(100, cancel=event)
        self.assertEqual(result.reason, "cancelled")
        self.assertLess(result.attempts, 100)
        self.assertEqual(result.remaining, 100 - result.accepted)

    def test_completed_run_reports_completed(self):
        sampler = make_sampler(seed=5)
        result = sampler.sample(20, budget=10_000)
        self.assertEqual(result.reason, "completed")
        self.assertTrue(result.completed)
        self.assertEqual(result.remaining, 0)


class TestStatisticsAndLabels(unittest.TestCase):
    def test_acceptance_rate_denominator_is_attempts(self):
        sampler = make_sampler(seed=11)
        result = sampler.sample(30, budget=10_000)
        self.assertAlmostEqual(
            result.acceptance_rate, result.accepted / result.attempts
        )

    def test_seed_recorded_on_result(self):
        sampler = make_sampler(seed=1234)
        result = sampler.sample(5, budget=1000)
        self.assertEqual(result.seed, 1234)

    def test_exact_vs_empirical_labels(self):
        exact = exact_finite_distribution(target, DOMAIN)
        self.assertEqual(exact["label"], "exact")
        self.assertEqual(sum(exact["probabilities"].values()), Fraction(1))
        sampler = make_sampler(seed=21)
        result = sampler.sample(4000, budget=100_000)
        check = verify_sample_distribution(result.samples, target, DOMAIN, 0.05)
        self.assertEqual(check["empirical"]["label"], "empirical")
        self.assertTrue(check["within_tolerance"], check["max_deviation"])

    def test_rejection_counts_match_exact_expectation(self):
        bound = 50.0
        expected = expected_acceptance_probability(target, proposal_pdf, DOMAIN, bound)
        self.assertEqual(expected, Fraction(30, 50))
        sampler = make_sampler(seed=99, bound=bound, certificate="c")
        result = sampler.sample(3000, budget=100_000)
        report = check_rejection_counts(result, expected, tolerance=0.05)
        self.assertEqual(report.rejected, report.attempts - report.accepted)
        self.assertTrue(
            report.within_tolerance,
            f"observed {report.observed_acceptance} vs exact {float(expected)}",
        )


class TestCheckpointRestore(unittest.TestCase):
    def test_state_roundtrip_reproduces_uninterrupted_run(self):
        # Uninterrupted reference run of 12 samples.
        ref = make_sampler(seed=2024)
        ref_result = ref.sample(12, budget=10_000)

        # Split run: 5 samples, checkpoint, restore into a fresh sampler, continue.
        first = make_sampler(seed=2024)
        part1 = first.sample(5, budget=10_000)
        checkpoint = first.state
        self.assertIsInstance(checkpoint, SamplerState)
        self.assertEqual(len(checkpoint.accepted_positions), 5)

        second = make_sampler(seed=999)  # different seed; state must override
        second.restore(checkpoint)
        part2 = second.sample(7, budget=10_000)

        self.assertEqual(part1.samples + part2.samples, ref_result.samples)
        self.assertEqual(
            second.accepted_positions, tuple(ref.state.accepted_positions)
        )
        self.assertEqual(second.total_attempts, ref.state.attempts)

    def test_accepted_positions_are_attempt_indices(self):
        sampler = make_sampler(seed=8)
        sampler.sample(10, budget=10_000)
        positions = sampler.accepted_positions
        self.assertEqual(len(positions), 10)
        self.assertTrue(all(1 <= p <= sampler.total_attempts for p in positions))
        self.assertEqual(list(positions), sorted(positions))


if __name__ == "__main__":
    unittest.main()
