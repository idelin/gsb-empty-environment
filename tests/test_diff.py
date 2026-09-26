import unittest

from tensorfusion.diff import run_exhaustive


class TestExhaustiveDifferential(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.summary = run_exhaustive()

    def test_no_mismatches(self):
        self.assertEqual(self.summary.mismatches, [])

    def test_all_value_cases_bitwise(self):
        s = self.summary
        self.assertEqual(s.bitwise_ok, s.cases - s.error_cases)
        self.assertGreater(s.cases, 1000)

    def test_error_cases_present_and_matched(self):
        self.assertGreater(self.summary.error_cases, 0)

    def test_fusion_reduces_buffers(self):
        s = self.summary
        self.assertLess(s.buffers_fused, s.buffers_unfused)
        self.assertLess(s.reads_fused, s.reads_unfused)
        self.assertLess(s.writes_fused, s.writes_unfused)


if __name__ == "__main__":
    unittest.main()
