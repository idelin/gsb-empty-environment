"""Reliability: enumerate small finite programs and cross-check engines."""
import random
import unittest

from handle_verifier.enumerate import (ReliabilityReport, check_program,
                                       gen_program)


def run_report(seed, count):
    rng = random.Random(seed)
    report = ReliabilityReport()
    for i in range(count):
        report.total += 1
        check_program(i, gen_program(rng), report)
    return report


class TestEnumeration(unittest.TestCase):
    def assert_reliable(self, report):
        self.assertEqual(report.pending, [],
                         "small finite programs must be fully explored")
        self.assertEqual(report.soundness_violations, [],
                         "analyzer missed a concrete error")
        self.assertEqual(report.exactness_violations, [],
                         "analyzer reported a phantom error")
        self.assertEqual(report.replay_failures, [],
                         "counterexample did not replay")
        self.assertGreater(report.concrete_runs, 0)

    def test_random_small_programs_seed_a(self):
        self.assert_reliable(run_report(20241001, 80))

    def test_random_small_programs_seed_b(self):
        self.assert_reliable(run_report(777, 60))


if __name__ == "__main__":
    unittest.main()
