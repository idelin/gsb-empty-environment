import tempfile
import unittest
from fractions import Fraction
from pathlib import Path

from noise import NoiseLedger, discrete_laplace
import random


class TestNoise(unittest.TestCase):
    def test_reproducible_given_seed(self):
        a = NoiseLedger(master_seed=42)
        b = NoiseLedger(master_seed=42)
        for key in ("leaf:x", "group:y", "total"):
            va = a.noisy_value(key, 10, Fraction(5))
            vb = b.noisy_value(key, 10, Fraction(5))
            self.assertEqual(va, vb)

    def test_repeated_publish_never_redraws(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "ledger.json"
            ledger = NoiseLedger(master_seed=7, path=path)
            first = ledger.noisy_value("leaf:a", 100, Fraction(2))
            self.assertFalse(first[2])
            before = path.read_text()
            again = ledger.noisy_value("leaf:a", 100, Fraction(2))
            self.assertTrue(again[2])
            self.assertEqual(first[:2], again[:2])
            self.assertEqual(before, path.read_text())
            # A fresh ledger over the same file also reuses the draw.
            reloaded = NoiseLedger(master_seed=999, path=path)
            third = reloaded.noisy_value("leaf:a", 100, Fraction(2))
            self.assertEqual(first[:2], third[:2])

    def test_keys_are_independent_draws(self):
        ledger = NoiseLedger(master_seed=1)
        ledger.noisy_value("a", 0, Fraction(1))
        ledger.noisy_value("b", 0, Fraction(1))
        self.assertEqual(set(ledger.entries), {"a", "b"})

    def test_discrete_laplace_is_integer_and_reproducible(self):
        r1 = random.Random(123)
        r2 = random.Random(123)
        draws1 = [discrete_laplace(r1, Fraction(3)) for _ in range(50)]
        draws2 = [discrete_laplace(r2, Fraction(3)) for _ in range(50)]
        self.assertEqual(draws1, draws2)
        self.assertTrue(all(isinstance(d, int) for d in draws1))


if __name__ == "__main__":
    unittest.main()
