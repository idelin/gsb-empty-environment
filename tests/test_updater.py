import math
import unittest

from bayes import BayesianUpdater, ConflictError, exact_posterior

PRIORS = {"h1": 0.5, "h2": 0.3, "h3": 0.2}


def assert_posterior_close(tc, post, expected, places=12):
    tc.assertEqual(set(post), set(expected))
    for h in post:
        tc.assertAlmostEqual(post[h], expected[h], places=places)


class TestNormalization(unittest.TestCase):
    def test_posterior_sums_to_one_in_log_domain(self):
        u = BayesianUpdater(PRIORS)
        u.apply("a", {"h1": 0.9, "h2": 0.4, "h3": 0.1})
        u.apply("b", {"h1": 0.2, "h2": 0.8, "h3": 0.5})
        post = u.posterior()
        self.assertAlmostEqual(sum(post.values()), 1.0, places=15)

    def test_matches_exact_rational_reference(self):
        u = BayesianUpdater(PRIORS)
        obs = [
            ("a", {"h1": 0.9, "h2": 0.4, "h3": 0.1}),
            ("b", {"h1": 0.2, "h2": 0.8, "h3": 0.5}),
            ("c", {"h1": 0.7, "h3": 0.3}),  # partial dict: h2 neutral
        ]
        for obs_id, lik in obs:
            u.apply(obs_id, lik)
        exact = exact_posterior(PRIORS, [lik for _, lik in obs])
        assert_posterior_close(
            self, u.posterior(), {h: float(p) for h, p in exact.items()}
        )

    def test_evidence_recorded_every_step(self):
        u = BayesianUpdater(PRIORS)
        u.apply("a", {"h1": 0.9, "h2": 0.4, "h3": 0.1})
        u.apply("b", {"h1": 0.2, "h2": 0.8, "h3": 0.5})
        u.correct("a", {"h1": 0.8, "h2": 0.5, "h3": 0.2})
        u.retract("b")
        events = [r.event for r in u.history]
        self.assertEqual(events, ["init", "apply", "apply", "correct", "retract"])
        for rec in u.history[1:]:
            self.assertFalse(rec.conflict)
            self.assertTrue(math.isfinite(rec.log_evidence))
        self.assertEqual(u.history[-1].log_evidence, u.log_evidence)


class TestConflict(unittest.TestCase):
    def test_zero_evidence_enters_conflict_not_uniform(self):
        u = BayesianUpdater({"h1": 0.5, "h2": 0.5})
        u.apply("a", {"h1": 1.0, "h2": 0.0})
        u.apply("b", {"h1": 0.0, "h2": 1.0})  # contradicts "a"
        self.assertTrue(u.in_conflict)
        self.assertTrue(u.history[-1].conflict)
        self.assertEqual(u.history[-1].log_evidence, float("-inf"))
        with self.assertRaises(ConflictError):
            u.posterior()

    def test_all_zero_likelihood_not_reset_to_uniform(self):
        u = BayesianUpdater({"h1": 0.5, "h2": 0.5})
        u.apply("z", {"h1": 0.0, "h2": 0.0})
        self.assertTrue(u.in_conflict)
        with self.assertRaises(ConflictError):
            u.posterior()
        u.retract("z")
        assert_posterior_close(self, u.posterior(), {"h1": 0.5, "h2": 0.5})

    def test_retracting_contradictory_evidence_recovers(self):
        priors = {"h1": 0.6, "h2": 0.4}
        u = BayesianUpdater(priors)
        u.apply("a", {"h1": 0.9, "h2": 0.1})
        u.apply("b", {"h1": 0.0, "h2": 1.0})  # kills h1
        u.apply("c", {"h1": 1.0, "h2": 0.0})  # kills h2 -> conflict
        self.assertTrue(u.in_conflict)
        u.retract("c")
        self.assertFalse(u.in_conflict)
        exact = exact_posterior(priors, [{"h1": 0.9, "h2": 0.1}, {"h1": 0.0, "h2": 1.0}])
        assert_posterior_close(
            self, u.posterior(), {h: float(p) for h, p in exact.items()}
        )
        self.assertAlmostEqual(u.posterior()["h2"], 1.0, places=15)

    def test_zero_likelihood_recovered_without_division(self):
        priors = {"h1": 0.5, "h2": 0.5}
        u = BayesianUpdater(priors)
        u.apply("a", {"h1": 0.0, "h2": 0.3})
        u.apply("b", {"h1": 0.7, "h2": 0.7})
        u.retract("a")  # would need division by zero without the tree
        exact = exact_posterior(priors, [{"h1": 0.7, "h2": 0.7}])
        assert_posterior_close(
            self, u.posterior(), {h: float(p) for h, p in exact.items()}
        )


class TestObservationLifecycle(unittest.TestCase):
    def test_duplicate_id_never_updates_twice(self):
        u = BayesianUpdater(PRIORS)
        lik = {"h1": 0.9, "h2": 0.4, "h3": 0.1}
        rec1 = u.apply("a", lik)
        rec2 = u.apply("a", lik)
        self.assertIsNotNone(rec1)
        self.assertIsNone(rec2)
        self.assertEqual(u.observation_ids, ("a",))
        exact = exact_posterior(PRIORS, [lik])
        assert_posterior_close(
            self, u.posterior(), {h: float(p) for h, p in exact.items()}
        )

    def test_correction_replaces_contribution(self):
        u = BayesianUpdater(PRIORS)
        u.apply("a", {"h1": 0.9, "h2": 0.4, "h3": 0.1})
        u.correct("a", {"h1": 0.1, "h2": 0.4, "h3": 0.9})
        exact = exact_posterior(PRIORS, [{"h1": 0.1, "h2": 0.4, "h3": 0.9}])
        assert_posterior_close(
            self, u.posterior(), {h: float(p) for h, p in exact.items()}
        )
        with self.assertRaises(KeyError):
            u.correct("missing", {"h1": 1.0})

    def test_late_arrivals_order_invariant(self):
        obs = [
            ("a", {"h1": 0.9, "h2": 0.4, "h3": 0.1}),
            ("b", {"h1": 0.2, "h2": 0.8, "h3": 0.5}),
            ("c", {"h1": 0.6, "h2": 0.6, "h3": 0.6}),
        ]
        u1 = BayesianUpdater(PRIORS)
        for obs_id, lik in obs:
            u1.apply(obs_id, lik)
        u2 = BayesianUpdater(PRIORS)
        for obs_id, lik in reversed(obs):
            u2.apply(obs_id, lik)
        for h in PRIORS:
            self.assertAlmostEqual(u1.posterior()[h], u2.posterior()[h], places=12)

    def test_batch_equals_sequential(self):
        obs = [
            ("a", {"h1": 0.9, "h2": 0.4, "h3": 0.1}),
            ("b", {"h1": 0.2, "h2": 0.8, "h3": 0.5}),
            ("c", {"h1": 0.3, "h2": 0.3, "h3": 0.9}),
            ("a", {"h1": 0.9, "h2": 0.4, "h3": 0.1}),  # duplicate inside batch
        ]
        seq = BayesianUpdater(PRIORS)
        for obs_id, lik in obs:
            seq.apply(obs_id, lik)
        bat = BayesianUpdater(PRIORS)
        bat.apply_batch(obs)
        self.assertEqual(seq.posterior(), bat.posterior())
        self.assertEqual(bat.observation_ids, ("a", "b", "c"))


class TestShardMerge(unittest.TestCase):
    def _shard(self, obs):
        u = BayesianUpdater(PRIORS)
        for obs_id, lik in obs:
            u.apply(obs_id, lik)
        return u

    def test_merge_does_not_double_count(self):
        shared = [
            ("o4", {"h1": 0.5, "h2": 0.5, "h3": 0.5}),
            ("o5", {"h1": 0.1, "h2": 0.9, "h3": 0.3}),
        ]
        left = [("o1", {"h1": 0.9, "h2": 0.2, "h3": 0.1}),
                ("o2", {"h1": 0.4, "h2": 0.4, "h3": 0.9}),
                ("o3", {"h1": 0.8, "h2": 0.1, "h3": 0.2})] + shared
        right = shared + [("o6", {"h1": 0.2, "h2": 0.7, "h3": 0.7}),
                          ("o7", {"h1": 0.6, "h2": 0.2, "h3": 0.6}),
                          ("o8", {"h1": 0.3, "h2": 0.3, "h3": 0.8})]
        merged = self._shard(left).merge(self._shard(right))
        self.assertEqual(len(merged.observation_ids), 8)
        exact = exact_posterior(PRIORS, [lik for _, lik in left + right[2:]])
        assert_posterior_close(
            self, merged.posterior(), {h: float(p) for h, p in exact.items()}
        )
        doubled = exact_posterior(
            PRIORS, [lik for _, lik in left + right]  # o4, o5 counted twice
        )
        self.assertNotAlmostEqual(
            merged.posterior()["h1"], float(doubled["h1"]), places=6
        )

    def test_merge_rejects_conflicting_duplicates(self):
        a = self._shard([("o1", {"h1": 0.5, "h2": 0.5, "h3": 0.5})])
        b = self._shard([("o1", {"h1": 0.1, "h2": 0.9, "h3": 0.5})])
        with self.assertRaises(ConflictError):
            a.merge(b)


class TestModelUpdate(unittest.TestCase):
    def test_prior_version_update_recomputes_evidence(self):
        u = BayesianUpdater(PRIORS, version=1)
        obs = [{"h1": 0.9, "h2": 0.4, "h3": 0.1}, {"h1": 0.2, "h2": 0.8, "h3": 0.5}]
        u.apply_batch(enumerate(obs))
        new_priors = {"h1": 0.2, "h2": 0.5, "h3": 0.3}
        log_z = u.update_model(new_priors, version=2)
        self.assertEqual(u.version, 2)
        self.assertAlmostEqual(u.log_evidence, log_z, places=15)
        exact = exact_posterior(new_priors, obs)
        assert_posterior_close(
            self, u.posterior(), {h: float(p) for h, p in exact.items()}
        )
        self.assertEqual(u.history[-1].event, "model")

    def test_hypothesis_set_change(self):
        u = BayesianUpdater({"h1": 0.5, "h2": 0.5}, version=1)
        u.apply("a", {"h1": 0.9, "h2": 0.1})
        u.update_model({"h1": 0.4, "h2": 0.3, "h3": 0.3}, version=2)
        self.assertEqual(u.hypotheses, ("h1", "h2", "h3"))
        exact = exact_posterior(
            {"h1": 0.4, "h2": 0.3, "h3": 0.3}, [{"h1": 0.9, "h2": 0.1}]
        )
        assert_posterior_close(
            self, u.posterior(), {h: float(p) for h, p in exact.items()}
        )

    def test_failed_update_is_atomic(self):
        u = BayesianUpdater({"h1": 0.5, "h2": 0.5}, version=1)
        u.apply("a", {"h1": 0.9, "h2": 0.0})
        before = u.posterior()
        with self.assertRaises(ConflictError):
            u.update_model({"h2": 1.0}, version=2)  # "a" zeros every hypothesis
        self.assertEqual(u.version, 1)
        self.assertEqual(u.hypotheses, ("h1", "h2"))
        self.assertEqual(u.posterior(), before)

    def test_version_must_increase(self):
        u = BayesianUpdater(PRIORS, version=2)
        with self.assertRaises(ValueError):
            u.update_model(PRIORS, version=2)


if __name__ == "__main__":
    unittest.main()
