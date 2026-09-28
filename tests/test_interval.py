import unittest

from binomial_power import clopper_pearson, wilson


class TestClopperPearson(unittest.TestCase):
    def test_zero_trials_rejected(self):
        with self.assertRaises(ValueError):
            clopper_pearson(0, 0)
        with self.assertRaises(ValueError):
            wilson(0, 0)

    def test_all_rejections_still_has_width(self):
        lo, hi = clopper_pearson(20, 20, alpha=0.05)
        self.assertEqual(hi, 1.0)
        self.assertLess(lo, 1.0)
        self.assertGreater(hi - lo, 0.0)
        # known closed form: lower bound = (alpha/2)^(1/n)
        self.assertAlmostEqual(lo, 0.025 ** (1 / 20), places=10)

    def test_zero_rejections(self):
        lo, hi = clopper_pearson(0, 20, alpha=0.05)
        self.assertEqual(lo, 0.0)
        self.assertGreater(hi, 0.0)
        self.assertAlmostEqual(hi, 1 - 0.025 ** (1 / 20), places=10)

    def test_interval_ordered_and_inside_unit(self):
        for x in range(0, 11):
            lo, hi = clopper_pearson(x, 10)
            self.assertLessEqual(0.0, lo)
            self.assertLess(lo, hi)
            self.assertLessEqual(hi, 1.0)

    def test_monotone_in_x(self):
        los, his = zip(*(clopper_pearson(x, 30) for x in range(31)))
        self.assertEqual(list(los), sorted(los))
        self.assertEqual(list(his), sorted(his))

    def test_shrinks_with_n(self):
        w_small = clopper_pearson(5, 10)[1] - clopper_pearson(5, 10)[0]
        w_large = clopper_pearson(500, 1000)[1] - clopper_pearson(500, 1000)[0]
        self.assertLess(w_large, w_small)

    def test_invalid_x(self):
        with self.assertRaises(ValueError):
            clopper_pearson(11, 10)


if __name__ == "__main__":
    unittest.main()
