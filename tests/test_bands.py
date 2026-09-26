import unittest

from filterverify.bands import BandSpec


class BandSpecTest(unittest.TestCase):
    def test_valid_spec(self):
        spec = BandSpec(8000.0, (100.0, 1000.0), (1800.0, 3900.0))
        self.assertEqual(spec.nyquist, 4000.0)
        self.assertAlmostEqual(spec.normalized(2000.0), 0.25)

    def test_rejects_edge_at_or_above_nyquist(self):
        with self.assertRaises(ValueError):
            BandSpec(8000.0, (100.0, 1000.0), (1800.0, 4000.0))
        with self.assertRaises(ValueError):
            BandSpec(8000.0, (100.0, 1000.0), (1800.0, 5000.0))

    def test_rejects_overlap_and_bad_edges(self):
        with self.assertRaises(ValueError):
            BandSpec(8000.0, (100.0, 2000.0), (1500.0, 3000.0))
        with self.assertRaises(ValueError):
            BandSpec(8000.0, (1000.0, 100.0), (1800.0, 3000.0))


if __name__ == "__main__":
    unittest.main()
