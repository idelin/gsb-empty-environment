"""unittest suite for bayes_tree.

Run with: python3.11 -m unittest -v test_bayes_tree
"""
import math
import random
import unittest
from fractions import Fraction

from bayes_tree import (
    BayesianUpdater,
    ConflictError,
    DuplicateObservationError,
    EmpiricalResult,
    ExactRationalModel,
    ObservationShard,
    PriorVersionError,
    UnknownObservationError,
    empirical_posterior,
    merge_shards,
)

HYPS = ("H_fair", "H_head", "H_tail")
UNIFORM = {"H_fair": 1 / 3, "H_head": 1 / 3, "H_tail": 1 / 3}
HEAD = {"H_fair": 0.5, "H_head": 0.8, "H_tail": 0.2}
TAIL = {"H_fair": 0.5, "H_head": 0.2, "H_tail": 0.8}


def assert_posteriors_close(test_case, actual, expected, tol=1e-12):
    test_case.assertEqual(set(actual), set(expected))
    for hypothesis in expected:
        test_case.assertTrue(
            math.isclose(actual[hypothesis], expected[hypothesis], abs_tol=tol),
            f"{hypothesis}: {actual[hypothesis]} !~ {expected[hypothesis]}",
        )


class SequentialUpdateTests(unittest.TestCase):
    def test_prior_is_normalized_in_log_domain(self):
        updater = BayesianUpdater(HYPS, {"H_fair": 2, "H_head": 3, "H_tail": 5})
        posterior = updater.posterior()
        assert_posteriors_close(
            self, posterior, {"H_fair": 0.2, "H_head": 0.3, "H_tail": 0.5}
        )
        self.assertAlmostEqual(sum(posterior.values()), 1.0)
        self.assertAlmostEqual(updater.log_evidence(), 0.0)

    def test_each_step_saves_evidence_value(self):
        updater = BayesianUpdater(HYPS, UNIFORM)
        updater.add_observation("flip1", HEAD)
        updater.add_observation("flip2", TAIL)
        steps = updater.history()
        self.assertEqual([s.step for s in steps], [1, 2, 3])
        operations = [s.operation for s in steps]
        self.assertEqual(operations, ["init", "add:flip1", "add:flip2"])
        self.assertTrue(all(math.isfinite(s.log_evidence) for s in steps))
        self.assertTrue(all(not s.conflict for s in steps))
        self.assertEqual(steps[1].log_evidence, updater.history()[1].log_evidence)

    def test_sequential_matches_direct_bayes(self):
        updater = BayesianUpdater(HYPS, UNIFORM)
        updater.add_observation("flip1", HEAD)
        updater.add_observation("flip2", HEAD)
        # direct computation with a normalized prior
        weights = {
            h: UNIFORM[h] * HEAD[h] * HEAD[h] for h in HYPS
        }
        norm = sum(weights.values())
        assert_posteriors_close(self, updater.posterior(),
                                {h: w / norm for h, w in weights.items()})
        self.assertAlmostEqual(updater.evidence(), norm)

    def test_log_domain_survives_tiny_likelihoods(self):
        updater = BayesianUpdater(("H1", "H2"), {"H1": 0.5, "H2": 0.5})
        for index in range(50):
            updater.add_observation(
                f"o{index}", {"H1": 1e-300, "H2": 1e-301}
            )
        # A naive running product has underflowed to 0 long before this;
        # the log-domain evidence stays finite and the posterior is usable.
        self.assertTrue(math.isfinite(updater.log_evidence()))
        posterior = updater.posterior()
        self.assertGreater(posterior["H1"], 0.99)
        self.assertAlmostEqual(sum(posterior.values()), 1.0)

    def test_late_arrival_order_independent(self):
        ordered = BayesianUpdater(HYPS, UNIFORM)
        ordered.add_observation("o1", HEAD)
        ordered.add_observation("o2", TAIL)
        ordered.add_observation("o3", HEAD)
        late = BayesianUpdater(HYPS, UNIFORM)
        late.add_observation("o3", HEAD)
        late.add_observation("o1", HEAD)
        late.add_observation("o2", TAIL)
        assert_posteriors_close(self, ordered.posterior(), late.posterior())
        self.assertAlmostEqual(ordered.log_evidence(), late.log_evidence())

    def test_correction_equivalent_to_fresh_insert(self):
        updater = BayesianUpdater(HYPS, UNIFORM)
        updater.add_observation("o1", TAIL)
        updater.add_observation("o2", HEAD)
        updater.correct_observation("o1", HEAD)
        reference = BayesianUpdater(HYPS, UNIFORM)
        reference.add_observation("o1", HEAD)
        reference.add_observation("o2", HEAD)
        self.assertEqual(updater.posterior(), reference.posterior())

    def test_zero_prior_hypothesis_stays_impossible(self):
        updater = BayesianUpdater(
            HYPS, {"H_fair": 1, "H_head": 1, "H_tail": 0}
        )
        updater.add_observation("o1", TAIL)
        self.assertEqual(updater.posterior()["H_tail"], 0.0)


class ConflictAndRetractionTests(unittest.TestCase):
    TWO = ("H1", "H2")
    TWO_PRIOR = {"H1": 0.5, "H2": 0.5}

    def test_contradiction_reports_conflict_then_retraction_recovers(self):
        updater = BayesianUpdater(self.TWO, self.TWO_PRIOR)
        updater.add_observation("e1", {"H1": 1.0, "H2": 0.0})
        record = updater.add_observation("e2", {"H1": 0.0, "H2": 1.0})
        self.assertTrue(record.conflict)
        self.assertTrue(updater.is_conflict())
        with self.assertRaises(ConflictError):
            updater.posterior()
        with self.assertRaises(ConflictError):
            updater.log_evidence()
        before = BayesianUpdater(self.TWO, self.TWO_PRIOR)
        before.add_observation("e1", {"H1": 1.0, "H2": 0.0})
        updater.retract_observation("e2")
        self.assertFalse(updater.is_conflict())
        # recovered state is exactly the state before the contradiction
        self.assertEqual(updater.posterior(), before.posterior())
        self.assertAlmostEqual(updater.posterior()["H1"], 1.0)

    def test_zero_likelihood_retraction_is_exact_no_division(self):
        # 3 observations occupy 4 slots, so adding/retracting a 4th never
        # re-grows the tree; leaf-for-leaf reconstruction must be bit exact.
        updater = BayesianUpdater(HYPS, UNIFORM)
        updater.add_observation("o1", HEAD)
        updater.add_observation("o2", TAIL)
        updater.add_observation("o3", HEAD)
        expected = updater.posterior()
        expected_logz = updater.log_evidence()
        updater.add_observation(
            "zero", {"H_fair": 0.0, "H_head": 0.0, "H_tail": 0.0}
        )
        self.assertTrue(updater.is_conflict())
        updater.retract_observation("zero")
        self.assertEqual(updater.posterior(), expected)
        self.assertEqual(updater.log_evidence(), expected_logz)

    def test_all_zero_likelihood_never_resets_to_uniform(self):
        updater = BayesianUpdater(self.TWO, self.TWO_PRIOR)
        updater.add_observation("supporting", {"H1": 0.9, "H2": 0.1})
        record = updater.add_observation("bad", {"H1": 0.0, "H2": 0.0})
        self.assertEqual(record.log_evidence, float("-inf"))
        with self.assertRaises(ConflictError):
            updater.posterior()
        updater.retract_observation("bad")
        posterior = updater.posterior()
        self.assertAlmostEqual(posterior["H1"], 0.9)
        self.assertAlmostEqual(posterior["H2"], 0.1)

    def test_partial_zero_likelihood_does_not_conflict(self):
        updater = BayesianUpdater(self.TWO, self.TWO_PRIOR)
        updater.add_observation("e1", {"H1": 0.4, "H2": 0.0})
        self.assertEqual(updater.posterior()["H2"], 0.0)
        self.assertAlmostEqual(updater.posterior()["H1"], 1.0)

    def test_unknown_observation_errors(self):
        updater = BayesianUpdater(self.TWO, self.TWO_PRIOR)
        with self.assertRaises(UnknownObservationError):
            updater.correct_observation("nope", {"H1": 1, "H2": 1})
        with self.assertRaises(UnknownObservationError):
            updater.retract_observation("nope")

    def test_duplicate_observation_id_not_double_updated(self):
        updater = BayesianUpdater(self.TWO, self.TWO_PRIOR)
        updater.add_observation("e1", {"H1": 0.8, "H2": 0.2})
        snapshot = updater.posterior()
        with self.assertRaises(DuplicateObservationError):
            updater.add_observation("e1", {"H1": 0.8, "H2": 0.2})
        self.assertEqual(updater.observation_count, 1)
        self.assertEqual(updater.observation_ids(), frozenset({"e1"}))
        self.assertEqual(updater.posterior(), snapshot)


class BatchEquivalenceTests(unittest.TestCase):
    OPS = [
        ("add", "o1", HEAD),
        ("add", "o2", TAIL),
        ("correct", "o1", HEAD),
        ("retract", "o2"),
        ("add", "o3", HEAD),
    ]

    def test_batch_identical_to_sequential(self):
        batched = BayesianUpdater(HYPS, UNIFORM)
        batched.apply_batch(self.OPS)
        sequential = BayesianUpdater(HYPS, UNIFORM)
        sequential.add_observation("o1", HEAD)
        sequential.add_observation("o2", TAIL)
        sequential.correct_observation("o1", HEAD)
        sequential.retract_observation("o2")
        sequential.add_observation("o3", HEAD)
        self.assertEqual(batched.posterior(), sequential.posterior())
        self.assertEqual(
            [r.log_evidence for r in batched.history()],
            [r.log_evidence for r in sequential.history()],
        )
        self.assertEqual(
            [r.operation for r in batched.history()],
            [r.operation for r in sequential.history()],
        )

    def test_empty_batch_changes_nothing(self):
        updater = BayesianUpdater(HYPS, UNIFORM)
        records = updater.apply_batch([])
        self.assertEqual(records, ())
        self.assertEqual(updater.history()[0].operation, "init")


class PriorVersionTests(unittest.TestCase):
    def test_stale_version_rejected(self):
        updater = BayesianUpdater(HYPS, UNIFORM, prior_version=3)
        with self.assertRaises(PriorVersionError):
            updater.update_prior(UNIFORM, version=3)
        with self.assertRaises(PriorVersionError):
            updater.update_prior(UNIFORM, version=2)
        self.assertEqual(updater.prior_version, 3)

    def test_invalid_prior_update_is_atomic(self):
        updater = BayesianUpdater(HYPS, UNIFORM)
        before = updater.posterior()
        with self.assertRaises(ValueError):
            updater.update_prior(
                {"H_fair": 0, "H_head": 0, "H_tail": 0}, version=2
            )
        self.assertEqual(updater.prior_version, 1)
        self.assertEqual(updater.posterior(), before)

    def test_valid_prior_update_recomputes_normalized_evidence(self):
        updater = BayesianUpdater(HYPS, UNIFORM)
        updater.add_observation("o1", HEAD)
        old_logz = updater.log_evidence()
        new_prior = {"H_fair": 0.1, "H_head": 0.8, "H_tail": 0.1}
        record = updater.update_prior(new_prior, version=2)
        self.assertEqual(record.prior_version, 2)
        self.assertNotEqual(updater.log_evidence(), old_logz)
        direct = {
            h: 0.1 if h != "H_head" else 0.8 for h in HYPS
        }
        weights = {h: direct[h] * HEAD[h] for h in HYPS}
        norm = sum(weights.values())
        assert_posteriors_close(
            self, updater.posterior(), {h: w / norm for h, w in weights.items()}
        )

    def test_prior_update_into_conflict_keeps_retractable_state(self):
        updater = BayesianUpdater(("H1", "H2"), {"H1": 0.5, "H2": 0.5})
        updater.add_observation("e1", {"H1": 1.0, "H2": 0.0})
        updater.update_prior({"H1": 0.0, "H2": 1.0}, version=2)
        self.assertTrue(updater.is_conflict())
        updater.update_prior({"H1": 0.5, "H2": 0.5}, version=3)
        self.assertFalse(updater.is_conflict())
        self.assertAlmostEqual(updater.posterior()["H1"], 1.0)


class ShardMergeTests(unittest.TestCase):
    def test_merge_does_not_double_count_shared_observation(self):
        shard_a = ObservationShard(HYPS, name="A")
        shard_a.add_observation("o1", HEAD)
        shard_a.add_observation("o2", TAIL)
        shard_a.add_observation("shared", HEAD)
        shard_b = ObservationShard(HYPS, name="B")
        shard_b.add_observation("shared", HEAD)  # replicated exactly
        shard_b.add_observation("o3", TAIL)
        merged = merge_shards(HYPS, UNIFORM, [shard_a, shard_b])
        self.assertEqual(
            merged.observation_ids(), frozenset({"o1", "o2", "o3", "shared"})
        )
        reference = BayesianUpdater(HYPS, UNIFORM)
        for obs_id, likelihood in (
            ("o1", HEAD), ("o2", TAIL), ("shared", HEAD), ("o3", TAIL)
        ):
            reference.add_observation(obs_id, likelihood)
        self.assertEqual(merged.posterior(), reference.posterior())
        self.assertAlmostEqual(merged.log_evidence(), reference.log_evidence())

    def test_merge_conflicting_replicates_raises(self):
        shard_a = ObservationShard(("H1", "H2"))
        shard_a.add_observation("x", {"H1": 0.3, "H2": 0.7})
        shard_b = ObservationShard(("H1", "H2"))
        shard_b.add_observation("x", {"H1": 0.9, "H2": 0.1})
        with self.assertRaises(ConflictError):
            merge_shards(("H1", "H2"), {"H1": 0.5, "H2": 0.5},
                         [shard_a, shard_b])

    def test_independent_shards_recover_late_arrival(self):
        late = ObservationShard(("H1", "H2"))
        late.add_observation("early_obs", {"H1": 0.6, "H2": 0.4})
        main = ObservationShard(("H1", "H2"))
        main.add_observation("later_obs", {"H1": 0.4, "H2": 0.6})
        merged = merge_shards(
            ("H1", "H2"), {"H1": 0.5, "H2": 0.5}, [late, main]
        )
        reference = BayesianUpdater(("H1", "H2"), {"H1": 0.5, "H2": 0.5})
        reference.add_observation("early_obs", {"H1": 0.6, "H2": 0.4})
        reference.add_observation("later_obs", {"H1": 0.4, "H2": 0.6})
        self.assertEqual(merged.posterior(), reference.posterior())


class ExactRationalCrossCheckTests(unittest.TestCase):
    PRIOR = {"H_fair": Fraction(1, 3), "H_head": Fraction(1, 3), "H_tail": Fraction(1, 3)}
    HEAD_F = {"H_fair": Fraction(1, 2), "H_head": Fraction(4, 5), "H_tail": Fraction(1, 5)}
    TAIL_F = {"H_fair": Fraction(1, 2), "H_head": Fraction(1, 5), "H_tail": Fraction(4, 5)}

    def setUp(self):
        self.exact = ExactRationalModel(HYPS, self.PRIOR)
        for obs_id, likelihood in (
            ("f1", self.HEAD_F), ("f2", self.TAIL_F), ("f3", self.HEAD_F)
        ):
            self.exact.add_observation(obs_id, likelihood)
        self.approx = BayesianUpdater(
            HYPS,
            {h: float(self.PRIOR[h]) for h in HYPS},
        )
        for obs_id, likelihood in (
            ("f1", self.HEAD_F), ("f2", self.TAIL_F), ("f3", self.HEAD_F)
        ):
            self.approx.add_observation(
                obs_id, {h: float(likelihood[h]) for h in HYPS}
            )

    def test_float_model_matches_exact_fractions(self):
        exact_posterior = self.exact.posterior()
        for hypothesis in HYPS:
            self.assertAlmostEqual(
                self.approx.posterior()[hypothesis],
                float(exact_posterior[hypothesis]),
                places=12,
            )
            self.assertTrue(isinstance(exact_posterior[hypothesis], Fraction))

    def test_evidence_matches_exact(self):
        self.assertAlmostEqual(
            self.approx.evidence(), float(self.exact.evidence()), places=14
        )
        self.assertTrue(isinstance(self.exact.evidence(), Fraction))

    def test_retraction_matches_exactly(self):
        self.exact.retract_observation("f2")
        self.approx.retract_observation("f2")
        for hypothesis in HYPS:
            self.assertAlmostEqual(
                self.approx.posterior()[hypothesis],
                float(self.exact.posterior()[hypothesis]),
                places=12,
            )

    def test_exact_model_reports_conflict(self):
        two = ("A", "B")
        model = ExactRationalModel(two, {"A": Fraction(1, 2), "B": Fraction(1, 2)})
        model.add_observation("e1", {"A": Fraction(1), "B": Fraction(0)})
        model.add_observation("e2", {"A": Fraction(0), "B": Fraction(1)})
        with self.assertRaises(ConflictError):
            model.posterior()
        model.retract_observation("e2")
        self.assertEqual(model.posterior()["A"], Fraction(1))


class EmpiricalEstimateTests(unittest.TestCase):
    def test_injected_rng_seed_and_samples_recorded_and_labelled(self):
        observations = {"o1": HEAD, "o2": TAIL, "o3": HEAD}
        seed = 20260928
        rng = random.Random(seed)
        result = empirical_posterior(
            HYPS, UNIFORM, observations, rng=rng, seed=seed, samples=20000
        )
        self.assertIsInstance(result, EmpiricalResult)
        self.assertEqual(result.label, "empirical")
        self.assertEqual(result.seed, seed)
        self.assertEqual(result.samples, 20000)
        self.assertAlmostEqual(sum(result.estimate.values()), 1.0)
        exact = BayesianUpdater(HYPS, UNIFORM)
        for obs_id, likelihood in observations.items():
            exact.add_observation(obs_id, likelihood)
        labelled_exact = exact.exact_posterior()
        self.assertEqual(labelled_exact.label, "exact")
        for hypothesis in HYPS:
            self.assertAlmostEqual(
                result.estimate[hypothesis],
                labelled_exact.posterior[hypothesis],
                delta=0.02,
            )

    def test_empirical_run_is_reproducible(self):
        observations = {"o1": HEAD, "o2": TAIL}
        first = empirical_posterior(
            HYPS, UNIFORM, observations,
            rng=random.Random(7), seed=7, samples=2000,
        )
        second = empirical_posterior(
            HYPS, UNIFORM, observations,
            rng=random.Random(7), seed=7, samples=2000,
        )
        self.assertEqual(first.estimate, second.estimate)
        different = empirical_posterior(
            HYPS, UNIFORM, observations,
            rng=random.Random(8), seed=8, samples=2000,
        )
        self.assertNotEqual(first.estimate, different.estimate)

    def test_empirical_conflict_raises(self):
        observations = {
            "e1": {"H1": 1.0, "H2": 0.0},
            "e2": {"H1": 0.0, "H2": 1.0},
        }
        with self.assertRaises(ConflictError):
            empirical_posterior(
                ("H1", "H2"), {"H1": 0.5, "H2": 0.5}, observations,
                rng=random.Random(1), seed=1, samples=10,
            )


if __name__ == "__main__":
    unittest.main()
