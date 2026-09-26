import array
import json
import os
import tempfile
import unittest

from loudnorm import WriteCancelled, plan_normalization, write_output
from loudnorm import fixtures
from loudnorm.writer import COMPLETE_SUFFIX, PART_SUFFIX


class TestWriter(unittest.TestCase):
    def setUp(self):
        self.samples, self.rate, self.specs, self.kwargs = \
            fixtures.feasible_case()
        self.plan = plan_normalization(
            self.samples, self.rate, self.specs, **self.kwargs)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = os.path.join(self.tmp.name, "out.f32")

    def test_successful_write_publishes_marker(self):
        write_output(self.path, self.samples, self.rate, self.plan,
                     chunk_size=4096)
        self.assertTrue(os.path.exists(self.path))
        self.assertFalse(os.path.exists(self.path + PART_SUFFIX))
        with open(self.path + COMPLETE_SUFFIX, encoding="utf-8") as fh:
            marker = json.load(fh)
        self.assertTrue(marker["complete"])
        self.assertEqual(marker["input_digest"], self.plan.input_digest)
        self.assertEqual(marker["num_samples"], len(self.samples))
        self.assertEqual(marker["sample_rate"], self.rate)
        pcm = array.array("f")
        with open(self.path, "rb") as fh:
            pcm.frombytes(fh.read())
        self.assertEqual(len(pcm), len(self.samples))

    def test_cancelled_write_leaves_no_file_and_no_marker(self):
        calls = {"n": 0}

        def cancel_after_two_chunks():
            calls["n"] += 1
            return calls["n"] > 2

        with self.assertRaises(WriteCancelled):
            write_output(self.path, self.samples, self.rate, self.plan,
                         chunk_size=1024,
                         cancel_check=cancel_after_two_chunks)
        self.assertFalse(os.path.exists(self.path))
        self.assertFalse(os.path.exists(self.path + PART_SUFFIX))
        self.assertFalse(os.path.exists(self.path + COMPLETE_SUFFIX))

    def test_digest_mismatch_aborts_before_writing(self):
        corrected = list(self.samples)
        corrected[0] += 0.5
        with self.assertRaises(Exception):
            write_output(self.path, corrected, self.rate, self.plan)
        self.assertFalse(os.path.exists(self.path))
        self.assertFalse(os.path.exists(self.path + COMPLETE_SUFFIX))


if __name__ == "__main__":
    unittest.main()
