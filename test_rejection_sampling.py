import random
import unittest

from rejection_sampling import (
    AcceptedRecord,
    BoundViolationError,
    CancellationToken,
    Certificate,
    DomainNotCoveredError,
    EmpiricalReport,
    Envelope,
    ExactAnalysis,
    Partition,
    RecoveryMismatchError,
    RejectionSampler,
    SamplingResult,
    Status,
    UncertifiedBoundError,
    UniformFiniteProposal,
    analyze_finite,
    empirical_report,
    recover_confirmed_samples,
    total_variation,
    verify_envelope_exactly,
    verify_partition_exactly,
)

DOMAIN = list(range(10))
WEIGHTS = [1.0, 2.0, 3.0, 4.0, 5.0, 4.0, 3.0, 2.0, 1.0, 0.5]


def target(x):
    return WEIGHTS[x]


def make_proposal():
    return UniformFiniteProposal(DOMAIN)


def certified_envelope(bound=50.0):
    return Envelope(
        [
            Partition(
                name="all",
                contains=lambda x: True,
                bound=bound,
                certificate=Certificate("bound checked by caller"),
            )
        ]
    )


def partitioned_envelope():
    # per-partition bounds: max ratio on {0..4} is 5/0.1 = 50, on {5..9} is 4/0.1 = 40
    return Envelope(
        [
            Partition(
                name="low",
                contains=lambda x: x < 5,
                bound=50.0,
                certificate=Certificate("max ratio on low half is 50"),
            ),
            Partition(
                name="high",
                contains=lambda x: x >= 5,
                bound=40.0,
                certificate=Certificate("max ratio on high half is 40"),
            ),
        ]
    )


class TestBasicSampling(unittest.TestCase):
    def test_completes_requested_count(self):
        sampler = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=1234
        )
        result = sampler.sample(100)
        self.assertEqual(result.status, Status.COMPLETED)
        self.assertEqual(result.accepted, 100)
        self.assertEqual(result.remaining, 0)
        self.assertEqual(len(result.records), 100)

    def test_seed_and_sample_count_recorded(self):
        sampler = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=99
        )
        result = sampler.sample(10)
        self.assertEqual(result.seed, 99)
        self.assertEqual(result.accepted, 10)

    def test_caller_injected_rng(self):
        rng = random.Random(7)
        sampler = RejectionSampler(
            target, make_proposal(), certified_envelope(), rng=rng, seed=7
        )
        result = sampler.sample(20)
        self.assertEqual(result.accepted, 20)
        self.assertEqual(result.seed, 7)

    def test_missing_rng_and_seed_rejected(self):
        with self.assertRaises(ValueError):
            RejectionSampler(target, make_proposal(), certified_envelope())

    def test_acceptance_rate_denominator_is_attempts(self):
        sampler = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=5
        )
        result = sampler.sample(50)
        self.assertGreater(result.attempts, 0)
        self.assertAlmostEqual(
            result.acceptance_rate, result.accepted / result.attempts
        )

    def test_replay_same_seed_same_samples(self):
        r1 = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=42
        ).sample(30)
        r2 = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=42
        ).sample(30)
        self.assertEqual(r1.samples, r2.samples)
        self.assertEqual(r1.attempts, r2.attempts)


class TestEnvelopeTrust(unittest.TestCase):
    def test_uncertified_partition_rejected(self):
        env = Envelope(
            [Partition(name="all", contains=lambda x: True, bound=50.0)]
        )
        sampler = RejectionSampler(target, make_proposal(), env, seed=1)
        with self.assertRaises(UncertifiedBoundError):
            sampler.sample(1)

    def test_exactly_verified_partition_accepted(self):
        env = Envelope(
            [Partition(name="all", contains=lambda x: True, bound=50.0)]
        )
        report = verify_envelope_exactly(env, target, make_proposal(), DOMAIN)
        self.assertEqual(report.kind, "exact")
        self.assertTrue(report.all_ok)
        self.assertTrue(env.partitions[0].verified_exactly)
        sampler = RejectionSampler(target, make_proposal(), env, seed=1)
        self.assertEqual(sampler.sample(5).accepted, 5)

    def test_exact_verification_detects_bad_bound(self):
        env = Envelope(
            [Partition(name="all", contains=lambda x: True, bound=10.0)]
        )
        report = verify_envelope_exactly(env, target, make_proposal(), DOMAIN)
        self.assertFalse(report.all_ok)
        self.assertFalse(report.per_partition["all"])
        self.assertFalse(env.partitions[0].verified_exactly)

    def test_uncovered_domain_point_raises(self):
        env = Envelope(
            [
                Partition(
                    name="low",
                    contains=lambda x: x < 5,
                    bound=50.0,
                    certificate=Certificate("c"),
                )
            ]
        )
        with self.assertRaises(DomainNotCoveredError):
            verify_envelope_exactly(env, target, make_proposal(), DOMAIN)


class TestBoundViolation(unittest.TestCase):
    def test_violation_raises_not_clamps(self):
        # certificate lies: true max ratio is 50, bound says 20
        env = certified_envelope(bound=20.0)
        sampler = RejectionSampler(target, make_proposal(), env, seed=0)
        with self.assertRaises(BoundViolationError):
            sampler.sample(1000)

    def test_violation_in_exact_analysis(self):
        env = certified_envelope(bound=20.0)
        with self.assertRaises(BoundViolationError):
            analyze_finite(DOMAIN, target, make_proposal(), env)

    def test_proposal_zero_mass_with_positive_target(self):
        class BadProposal:
            def sample(self, rng):
                return 0

            def pmf(self, x):
                return 0.0

        env = certified_envelope()
        sampler = RejectionSampler(target, BadProposal(), env, seed=0)
        with self.assertRaises(BoundViolationError):
            sampler.sample(1)


class TestAdaptiveRefinement(unittest.TestCase):
    def test_refine_with_certificates(self):
        env = certified_envelope()
        env.refine(
            "all",
            [
                Partition(
                    name="low",
                    contains=lambda x: x < 5,
                    bound=50.0,
                    certificate=Certificate("low half max ratio 50"),
                ),
                Partition(
                    name="high",
                    contains=lambda x: x >= 5,
                    bound=40.0,
                    certificate=Certificate("high half max ratio 40"),
                ),
            ],
            domain=DOMAIN,
        )
        self.assertEqual([p.name for p in env.partitions], ["low", "high"])
        sampler = RejectionSampler(target, make_proposal(), env, seed=3)
        self.assertEqual(sampler.sample(10).accepted, 10)

    def test_refine_with_exact_verification(self):
        env = certified_envelope()
        env.refine(
            "all",
            [
                Partition(name="low", contains=lambda x: x < 5, bound=50.0),
                Partition(name="high", contains=lambda x: x >= 5, bound=40.0),
            ],
            target=target,
            proposal=make_proposal(),
            domain=DOMAIN,
        )
        self.assertTrue(all(p.verified_exactly for p in env.partitions))

    def test_refine_without_trust_raises(self):
        env = certified_envelope()
        with self.assertRaises(UncertifiedBoundError):
            env.refine(
                "all",
                [Partition(name="low", contains=lambda x: x < 5, bound=50.0)],
            )

    def test_refine_with_wrong_bound_fails_exact_check(self):
        env = certified_envelope()
        with self.assertRaises(BoundViolationError):
            env.refine(
                "all",
                [
                    Partition(name="low", contains=lambda x: x < 5, bound=50.0),
                    Partition(name="high", contains=lambda x: x >= 5, bound=10.0),
                ],
                target=target,
                proposal=make_proposal(),
                domain=DOMAIN,
            )

    def test_refine_leaving_gap_raises(self):
        env = certified_envelope()
        with self.assertRaises(DomainNotCoveredError):
            env.refine(
                "all",
                [
                    Partition(
                        name="tiny",
                        contains=lambda x: x < 2,
                        bound=50.0,
                        certificate=Certificate("c"),
                    )
                ],
                domain=DOMAIN,
            )


class TestBudgetAndCancellation(unittest.TestCase):
    def test_budget_exhausted_returns_partial(self):
        sampler = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=11
        )
        result = sampler.sample(1000, max_attempts=10)
        self.assertEqual(result.status, Status.BUDGET_EXHAUSTED)
        self.assertEqual(result.attempts, 10)
        self.assertLess(result.accepted, 1000)
        self.assertEqual(result.remaining, 1000 - result.accepted)
        self.assertEqual(len(result.samples), result.accepted)

    def test_zero_budget_returns_nothing(self):
        sampler = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=11
        )
        result = sampler.sample(5, max_attempts=0)
        self.assertEqual(result.status, Status.BUDGET_EXHAUSTED)
        self.assertEqual(result.accepted, 0)
        self.assertEqual(result.attempts, 0)
        self.assertEqual(result.acceptance_rate, 0.0)

    def test_cancellation_returns_completed_only(self):
        token = CancellationToken()

        class CancellingProposal(UniformFiniteProposal):
            def __init__(self, domain, token, after):
                super().__init__(domain)
                self._token = token
                self._after = after
                self._calls = 0

            def sample(self, rng):
                self._calls += 1
                if self._calls > self._after:
                    self._token.cancel()
                return super().sample(rng)

        proposal = CancellingProposal(DOMAIN, token, after=6)
        sampler = RejectionSampler(
            target, proposal, certified_envelope(), seed=2
        )
        result = sampler.sample(1000, token=token)
        self.assertEqual(result.status, Status.CANCELLED)
        self.assertLess(result.accepted, 1000)
        self.assertEqual(result.remaining, 1000 - result.accepted)
        self.assertEqual(len(result.records), result.accepted)

    def test_precancelled_token(self):
        token = CancellationToken()
        token.cancel()
        sampler = RejectionSampler(
            target, make_proposal(), certified_envelope(), seed=2
        )
        result = sampler.sample(10, token=token)
        self.assertEqual(result.status, Status.CANCELLED)
        self.assertEqual(result.accepted, 0)
        self.assertEqual(result.attempts, 0)


class TestRecovery(unittest.TestCase):
    def test_recover_confirmed_positions(self):
        env = certified_envelope()
        result = RejectionSampler(
            target, make_proposal(), env, seed=777
        ).sample(25)
        recovered = recover_confirmed_samples(
            target, make_proposal(), env, result.seed, result.records
        )
        self.assertEqual(recovered, list(result.samples))

    def test_recover_subset_of_records(self):
        env = certified_envelope()
        result = RejectionSampler(
            target, make_proposal(), env, seed=777
        ).sample(25)
        subset = result.records[::5]
        recovered = recover_confirmed_samples(
            target, make_proposal(), env, result.seed, subset
        )
        self.assertEqual(recovered, [r.value for r in subset])

    def test_recovery_detects_tampered_record(self):
        env = certified_envelope()
        result = RejectionSampler(
            target, make_proposal(), env, seed=777
        ).sample(5)
        bad = AcceptedRecord(
            value=(result.records[0].value + 1) % 10,
            attempt_index=result.records[0].attempt_index,
            draws_used=result.records[0].draws_used,
        )
        with self.assertRaises(RecoveryMismatchError):
            recover_confirmed_samples(
                target, make_proposal(), env, result.seed, [bad]
            )

    def test_wrong_seed_does_not_reproduce(self):
        env = certified_envelope()
        result = RejectionSampler(
            target, make_proposal(), env, seed=1
        ).sample(50)
        other = RejectionSampler(
            target, make_proposal(), env, seed=2
        ).sample(50)
        self.assertNotEqual(result.samples, other.samples)


class TestExactVsEmpirical(unittest.TestCase):
    def test_exact_analysis_values(self):
        env = certified_envelope(bound=50.0)
        exact = analyze_finite(DOMAIN, target, make_proposal(), env)
        self.assertEqual(exact.kind, "exact")
        # p_acc = sum(w) / global_bound = 25.5 / 50
        self.assertAlmostEqual(exact.acceptance_probability, 25.5 / 50.0)
        self.assertAlmostEqual(
            exact.expected_attempts_per_sample, 50.0 / 25.5
        )
        self.assertAlmostEqual(
            exact.expected_rejections_per_sample, 50.0 / 25.5 - 1.0
        )
        total_w = sum(WEIGHTS)
        for x in DOMAIN:
            self.assertAlmostEqual(
                exact.output_distribution[x], WEIGHTS[x] / total_w
            )

    def test_partitioned_envelope_same_output_distribution(self):
        exact_flat = analyze_finite(DOMAIN, target, make_proposal(), certified_envelope())
        exact_part = analyze_finite(DOMAIN, target, make_proposal(), partitioned_envelope())
        for x in DOMAIN:
            self.assertAlmostEqual(
                exact_flat.output_distribution[x],
                exact_part.output_distribution[x],
            )
        # acceptance uses the global constant (max partition bound = 50 both)
        self.assertAlmostEqual(
            exact_part.acceptance_probability, exact_flat.acceptance_probability
        )

    def test_tighter_envelope_improves_acceptance(self):
        # loose certified bound 80 everywhere
        loose = certified_envelope(bound=80.0)
        exact_loose = analyze_finite(DOMAIN, target, make_proposal(), loose)
        self.assertAlmostEqual(exact_loose.acceptance_probability, 25.5 / 80.0)
        # adaptively refine with exact verification to the true per-half bounds
        loose.refine(
            "all",
            [
                Partition(name="low", contains=lambda x: x < 5, bound=50.0),
                Partition(name="high", contains=lambda x: x >= 5, bound=40.0),
            ],
            target=target,
            proposal=make_proposal(),
            domain=DOMAIN,
        )
        exact_tight = analyze_finite(DOMAIN, target, make_proposal(), loose)
        self.assertAlmostEqual(exact_tight.acceptance_probability, 25.5 / 50.0)
        self.assertGreater(
            exact_tight.acceptance_probability, exact_loose.acceptance_probability
        )
        # output distribution is still exactly the target
        total_w = sum(WEIGHTS)
        for x in DOMAIN:
            self.assertAlmostEqual(
                exact_tight.output_distribution[x], WEIGHTS[x] / total_w
            )

    def test_empirical_matches_exact_within_tolerance(self):
        env = certified_envelope()
        exact = analyze_finite(DOMAIN, target, make_proposal(), env)
        result = RejectionSampler(
            target, make_proposal(), env, seed=2024
        ).sample(20000)
        emp = empirical_report(result)
        self.assertEqual(emp.kind, "empirical")
        self.assertEqual(emp.samples, 20000)
        self.assertEqual(emp.rejections, emp.attempts - emp.samples)
        # empirical acceptance rate vs exact acceptance probability
        self.assertAlmostEqual(
            emp.acceptance_rate, exact.acceptance_probability, delta=0.01
        )
        # empirical rejection count vs exact expectation
        expected_rejections = (
            exact.expected_rejections_per_sample * emp.samples
        )
        self.assertAlmostEqual(
            emp.rejections, expected_rejections, delta=0.1 * expected_rejections
        )
        # empirical distribution vs exact output distribution
        tv = total_variation(exact.output_distribution, emp.distribution)
        self.assertLess(tv, 0.05)

    def test_labels_are_distinct(self):
        env = certified_envelope()
        exact = analyze_finite(DOMAIN, target, make_proposal(), env)
        result = RejectionSampler(
            target, make_proposal(), env, seed=8
        ).sample(10)
        emp = empirical_report(result)
        self.assertEqual(exact.kind, "exact")
        self.assertEqual(emp.kind, "empirical")
        self.assertNotEqual(exact.kind, emp.kind)


if __name__ == "__main__":
    unittest.main()
