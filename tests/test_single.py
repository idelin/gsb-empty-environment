import json
import tempfile
import unittest
from pathlib import Path

from bounded_mean import (
    EMPIRICAL,
    EXACT,
    MET,
    NOT_MET,
    RecordingRandom,
    StagedEstimator,
    write_evidence,
)
from bounded_mean.bounds import stage_delta


def bernoulli_sampler(p):
    def sample(rng):
        return 1.0 if rng.random() < p else 0.0
    return sample


class TestConstantVariable(unittest.TestCase):
    def test_constant_converges_immediately_exact(self):
        rng = RecordingRandom(seed=1)
        est = StagedEstimator(
            name="const", low=0.5, high=0.5, epsilon=0.01, delta=0.05,
            budget=10, sampler=lambda r: 0.5, rng=rng,
        )
        res = est.run()
        self.assertEqual(res.status, MET)
        self.assertEqual(res.conclusion_kind, EXACT)
        self.assertEqual(res.n_samples, 0)
        self.assertEqual(res.radius, 0.0)
        self.assertEqual(rng.draws, 0)


class TestStagedStopping(unittest.TestCase):
    def test_met_when_target_reached(self):
        rng = RecordingRandom(seed=42)
        est = StagedEstimator(
            name="bern03", low=0.0, high=1.0, epsilon=0.05, delta=0.05,
            budget=20000, sampler=bernoulli_sampler(0.3), rng=rng,
        )
        res = est.run()
        self.assertEqual(res.status, MET)
        self.assertEqual(res.conclusion_kind, EMPIRICAL)
        self.assertLessEqual(res.radius, 0.05)
        self.assertLess(res.n_samples, 20000)  # stopped early
        self.assertEqual(res.seed, 42)
        self.assertTrue(res.stages[-1].met)

    def test_budget_exhausted_reports_not_met(self):
        rng = RecordingRandom(seed=7)
        est = StagedEstimator(
            name="tight", low=0.0, high=1.0, epsilon=0.01, delta=0.05,
            budget=100, sampler=bernoulli_sampler(0.5), rng=rng,
        )
        res = est.run()
        self.assertEqual(res.status, NOT_MET)
        self.assertEqual(res.n_samples, 100)
        self.assertGreater(res.radius, 0.01)
        self.assertIn("NOT", res.guarantee)
        # Full stopping evidence preserved: one record per checkpoint.
        self.assertEqual(len(res.stages), len(est.checkpoints))
        self.assertFalse(any(s.met for s in res.stages))

    def test_stage_budgets_never_exceed_delta(self):
        # Peeking at every single sample is still safe: zeta-2 allocation.
        rng = RecordingRandom(seed=3)
        budget = 500
        est = StagedEstimator(
            name="peek", low=0.0, high=1.0, epsilon=0.02, delta=0.05,
            budget=budget, sampler=bernoulli_sampler(0.5), rng=rng,
            checkpoints=list(range(1, budget + 1)),
        )
        est.run()
        total = sum(stage_delta(0.05, j) for j in range(1, budget + 1))
        self.assertLess(total, 0.05)

    def test_endpoint_rule_fast_convergence(self):
        # All-zero Bernoulli observations: converges far earlier than
        # Hoeffding would allow (which needs ~1600+ samples here).
        rng = RecordingRandom(seed=11)
        est = StagedEstimator(
            name="rare", low=0.0, high=1.0, epsilon=0.05, delta=0.05,
            budget=4096, sampler=lambda r: 0.0, rng=rng,
            bernoulli_endpoint_rule=True,
        )
        res = est.run()
        self.assertEqual(res.status, MET)
        self.assertLessEqual(res.n_samples, 512)
        self.assertEqual(res.stages[-1].rule, "bernoulli-endpoint")

    def test_out_of_bounds_sample_rejected(self):
        rng = RecordingRandom(seed=5)
        est = StagedEstimator(
            name="bad", low=0.0, high=1.0, epsilon=0.1, delta=0.05,
            budget=10, sampler=lambda r: 2.0, rng=rng,
        )
        with self.assertRaises(ValueError):
            est.run()


class TestEvidence(unittest.TestCase):
    def test_evidence_roundtrip(self):
        rng = RecordingRandom(seed=99)
        est = StagedEstimator(
            name="ev", low=0.0, high=1.0, epsilon=0.01, delta=0.05,
            budget=64, sampler=bernoulli_sampler(0.5), rng=rng,
        )
        res = est.run()
        self.assertEqual(res.status, NOT_MET)
        with tempfile.TemporaryDirectory() as tmp:
            path = write_evidence(res, Path(tmp) / "evidence.json")
            payload = json.loads(path.read_text())
        ev = payload["evidence"]
        self.assertEqual(ev["status"], NOT_MET)
        self.assertEqual(ev["seed"], 99)
        self.assertEqual(ev["n_samples"], 64)
        self.assertEqual(len(ev["stages"]), len(est.checkpoints))
        self.assertIn("recorded_at_utc", payload)


if __name__ == "__main__":
    unittest.main()
