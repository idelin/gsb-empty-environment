import random
import unittest

from loudnorm import (
    Plan,
    PlanMismatchError,
    apply_plan,
    plan_normalization,
)
from loudnorm import fixtures


class TestChunkedApplication(unittest.TestCase):
    def setUp(self):
        self.samples, self.rate, self.specs, self.kwargs = \
            fixtures.feasible_case()
        self.plan = plan_normalization(
            self.samples, self.rate, self.specs, **self.kwargs)
        self.whole = apply_plan(self.samples, self.rate, self.plan)

    def test_random_chunks_match_whole_block_exactly(self):
        rng = random.Random(20260926)
        for _ in range(25):
            chunk = rng.randint(1, 7777)
            out = apply_plan(self.samples, self.rate, self.plan,
                             chunk_size=chunk)
            self.assertEqual(out, self.whole,
                             "chunk_size=%d diverged" % chunk)

    def test_chunk_size_one_matches(self):
        out = apply_plan(self.samples, self.rate, self.plan, chunk_size=1)
        self.assertEqual(out, self.whole)


class TestSerialization(unittest.TestCase):
    def setUp(self):
        self.samples, self.rate, self.specs, self.kwargs = \
            fixtures.feasible_case()
        self.plan = plan_normalization(
            self.samples, self.rate, self.specs, **self.kwargs)

    def test_json_round_trip_preserves_rendering(self):
        restored = Plan.from_json(self.plan.to_json())
        self.assertEqual(restored.to_dict(), self.plan.to_dict())
        self.assertEqual(apply_plan(self.samples, self.rate, restored),
                         apply_plan(self.samples, self.rate, self.plan))

    def test_serialized_plan_applies_in_chunks(self):
        restored = Plan.from_json(self.plan.to_json(indent=2))
        out = apply_plan(self.samples, self.rate, restored, chunk_size=501)
        self.assertEqual(out, apply_plan(self.samples, self.rate, self.plan))

    def test_version_mismatch_rejected(self):
        data = self.plan.to_dict()
        data["version"] = 999
        with self.assertRaises(ValueError):
            Plan.from_dict(data)


class TestInputDigest(unittest.TestCase):
    def setUp(self):
        self.samples, self.rate, self.specs, self.kwargs = \
            fixtures.feasible_case()
        self.plan = plan_normalization(
            self.samples, self.rate, self.specs, **self.kwargs)

    def test_source_correction_invalidates_plan(self):
        corrected = list(self.samples)
        corrected[123] += 0.01
        with self.assertRaises(PlanMismatchError):
            apply_plan(corrected, self.rate, self.plan)

    def test_wrong_sample_rate_rejected(self):
        with self.assertRaises(PlanMismatchError):
            apply_plan(self.samples, self.rate + 1, self.plan)

    def test_wrong_length_rejected(self):
        with self.assertRaises(PlanMismatchError):
            apply_plan(self.samples[:-1], self.rate, self.plan)

    def test_verification_can_be_skipped_explicitly(self):
        corrected = list(self.samples)
        corrected[123] += 0.01
        out = apply_plan(corrected, self.rate, self.plan, verify=False)
        self.assertEqual(len(out), len(corrected))


if __name__ == "__main__":
    unittest.main()
