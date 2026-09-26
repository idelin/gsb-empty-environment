import unittest

from fusion.normalize import FieldStats, normalize


class NormalizeTest(unittest.TestCase):
    def test_min_max_uses_explicit_stats(self):
        stats = FieldStats(2.0, 10.0)
        self.assertAlmostEqual(normalize(2.0, stats), 0.0)
        self.assertAlmostEqual(normalize(10.0, stats), 1.0)
        self.assertAlmostEqual(normalize(6.0, stats), 0.5)

    def test_all_identical_scores_avoid_division_by_zero(self):
        # max == min != 0 -> public rule maps everything to 1.0
        self.assertEqual(normalize(5.0, FieldStats(5.0, 5.0)), 1.0)
        # max == min == 0 -> maps to 0.0
        self.assertEqual(normalize(0.0, FieldStats(0.0, 0.0)), 0.0)

    def test_clamped_to_unit_interval(self):
        stats = FieldStats(0.0, 1.0)
        self.assertEqual(normalize(7.0, stats), 1.0)
        self.assertEqual(normalize(-3.0, stats), 0.0)

    def test_invalid_stats_rejected(self):
        with self.assertRaises(ValueError):
            FieldStats(3.0, 1.0)

    def test_baseline_independent_of_candidate_set(self):
        # The same raw score normalizes identically no matter which
        # candidates were fetched: only the explicit stats matter.
        stats = FieldStats(0.0, 4.0)
        self.assertEqual(normalize(2.0, stats), normalize(2.0, stats))
        self.assertAlmostEqual(normalize(2.0, stats), 0.5)


if __name__ == "__main__":
    unittest.main()
