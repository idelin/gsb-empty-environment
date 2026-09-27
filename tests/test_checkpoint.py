"""Tests for checkpoint save/restore of adaptive samplers.

Key invariant: restoring a checkpoint reuses stored weights verbatim and
never recomputes them against a new proposal density.
"""

import json
import os
import random
import tempfile
import unittest

from importance_sampling import AdaptiveIS, DiscreteDistribution

TARGET = DiscreteDistribution({0: 0.5, 1: 0.3, 2: 0.2}, name="target")
Q1 = DiscreteDistribution({0: 0.9, 1: 0.08, 2: 0.02}, name="q1")
Q2 = DiscreteDistribution({0: 0.05, 1: 0.15, 2: 0.8}, name="q2")


def f(x):
    return float(x)


class CheckpointCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = os.path.join(self.tmp.name, "checkpoint.json")

    def tearDown(self):
        self.tmp.cleanup()

    def make_sampler(self):
        sampler = AdaptiveIS(TARGET, seed=1234)
        sampler.run_iteration(Q1, 6, random.Random(1))
        return sampler


class TestCheckpointRoundTrip(CheckpointCase):
    def test_roundtrip_preserves_samples_weights_and_metadata(self):
        sampler = self.make_sampler()
        sampler.save_checkpoint(self.path)
        restored = AdaptiveIS.load_checkpoint(self.path, TARGET)
        self.assertEqual(restored.seed, 1234)
        self.assertEqual(restored.n, sampler.n)
        for original, loaded in zip(sampler.samples, restored.samples):
            self.assertEqual(original, loaded)
        self.assertEqual(
            sampler.estimate(f).self_normalized,
            restored.estimate(f).self_normalized,
        )

    def test_checkpoint_records_seed_and_sample_count(self):
        self.make_sampler().save_checkpoint(self.path)
        with open(self.path, "r", encoding="utf-8") as fh:
            payload = json.load(fh)
        self.assertEqual(payload["seed"], 1234)
        self.assertEqual(payload["n"], 6)
        self.assertEqual(len(payload["samples"]), 6)


class TestResumeDoesNotRecompute(CheckpointCase):
    def test_resume_with_new_proposal_keeps_old_weights(self):
        sampler = self.make_sampler()
        sampler.save_checkpoint(self.path)
        old_weights = [s.log_weight for s in sampler.samples]

        restored = AdaptiveIS.load_checkpoint(self.path, TARGET)
        restored.run_iteration(Q2, 6, random.Random(2))  # continue with a NEW proposal

        for s, old_w in zip(restored.samples[:6], old_weights):
            self.assertEqual(s.log_weight, old_w)
            # Sanity: the frozen weight is generally NOT what the new
            # proposal density would give for that point.
        recomputed = [
            TARGET.log_pdf(s.x) - Q2.log_pdf(s.x) for s in restored.samples[:6]
        ]
        self.assertNotEqual([s.log_weight for s in restored.samples[:6]], recomputed)

    def test_stored_weights_are_used_verbatim(self):
        # Tamper with a stored weight; restore must trust the file,
        # proving weights are not recomputed on load.
        sampler = self.make_sampler()
        sampler.save_checkpoint(self.path)
        with open(self.path, "r", encoding="utf-8") as fh:
            payload = json.load(fh)
        payload["samples"][0]["log_weight"] = 5.0
        with open(self.path, "w", encoding="utf-8") as fh:
            json.dump(payload, fh)

        restored = AdaptiveIS.load_checkpoint(self.path, TARGET)
        self.assertEqual(restored.samples[0].log_weight, 5.0)
        logw = [s.log_weight for s in restored.samples]
        fvals = [f(s.x) for s in restored.samples]
        from importance_sampling import ordinary_is, self_normalized_is

        result = restored.estimate(f)
        self.assertAlmostEqual(result.ordinary, ordinary_is(logw, fvals))
        self.assertAlmostEqual(result.self_normalized, self_normalized_is(logw, fvals))


if __name__ == "__main__":
    unittest.main()
