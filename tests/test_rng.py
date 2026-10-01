import random
import unittest

from bounded_mean import RecordingRandom


class TestRecordingRandom(unittest.TestCase):
    def test_seed_and_draws_recorded(self):
        rng = RecordingRandom(seed=1234)
        self.assertEqual(rng.seed, 1234)
        self.assertEqual(rng.draws, 0)
        rng.random()
        rng.random()
        self.assertEqual(rng.draws, 2)

    def test_reproducible(self):
        a = [RecordingRandom(seed=7).random() for _ in range(1)]
        r1, r2 = RecordingRandom(seed=7), RecordingRandom(seed=7)
        self.assertEqual([r1.random() for _ in range(5)],
                         [r2.random() for _ in range(5)])

    def test_wrap_external_rng(self):
        rng = RecordingRandom(rng=random.Random(0))
        self.assertIsNone(rng.seed)  # seed unknown -> recorded as None
        rng.random()
        self.assertEqual(rng.draws, 1)

    def test_seed_and_rng_mutually_exclusive(self):
        with self.assertRaises(ValueError):
            RecordingRandom(seed=1, rng=random.Random(1))


if __name__ == "__main__":
    unittest.main()
