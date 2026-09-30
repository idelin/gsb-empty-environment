import unittest
from fractions import Fraction

from dpproj.noise import NoiseLedger


class TestNoiseLedger(unittest.TestCase):
    def test_repeat_release_reuses_draw(self):
        ledger = NoiseLedger(123)
        first = ledger.laplace("r:bucket:a", Fraction(2))
        second = ledger.laplace("r:bucket:a", Fraction(2))
        self.assertEqual(first, second)
        self.assertEqual(len(ledger.draws), 1)

    def test_deterministic_recompute_from_seed(self):
        keys = ["r:bucket:a", "r:bucket:b", "r:total"]
        a = NoiseLedger(99)
        vals_a = [a.laplace(k, Fraction(3, 2)) for k in keys]
        b = NoiseLedger(99)
        vals_b = [b.laplace(k, Fraction(3, 2)) for k in keys]
        self.assertEqual(vals_a, vals_b)

    def test_different_seeds_differ(self):
        a = NoiseLedger(1).laplace("k", Fraction(1))
        b = NoiseLedger(2).laplace("k", Fraction(1))
        self.assertNotEqual(a, b)

    def test_scale_mismatch_on_same_key_rejected(self):
        ledger = NoiseLedger(5)
        ledger.laplace("k", Fraction(1))
        with self.assertRaises(ValueError):
            ledger.laplace("k", Fraction(2))

    def test_json_roundtrip_preserves_draws_and_rng(self):
        ledger = NoiseLedger(77)
        v1 = ledger.laplace("a", Fraction(1))
        restored = NoiseLedger.from_json(ledger.to_json())
        self.assertEqual(restored.laplace("a", Fraction(1)), v1)
        # A fresh key after restore must continue the same RNG stream.
        v2 = ledger.laplace("b", Fraction(1))
        self.assertEqual(restored.laplace("b", Fraction(1)), v2)

    def test_invalid_scale_rejected(self):
        with self.assertRaises(ValueError):
            NoiseLedger(0).laplace("k", Fraction(0))


if __name__ == "__main__":
    unittest.main()
