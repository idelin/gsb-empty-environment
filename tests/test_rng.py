import unittest

from bootstrap_ci import RandomSource


class TestRandomSource(unittest.TestCase):
    def test_same_seed_same_streams(self):
        a = RandomSource(42)
        b = RandomSource(42)
        for sample_id in range(20):
            self.assertEqual(a.stream(sample_id).random(),
                             b.stream(sample_id).random())

    def test_parallel_order_does_not_change_results(self):
        # Replicates computed in any order (e.g. shuffled across workers)
        # must yield identical per-replicate randomness.
        src = RandomSource(7)
        forward = {i: src.stream(i).random() for i in range(50)}
        reversed_order = {}
        for i in reversed(range(50)):
            reversed_order[i] = src.stream(i).random()
        self.assertEqual(forward, reversed_order)

    def test_different_seed_different_streams(self):
        a = RandomSource(1)
        b = RandomSource(2)
        self.assertNotEqual([a.stream(i).random() for i in range(5)],
                            [b.stream(i).random() for i in range(5)])

    def test_seed_must_be_int(self):
        with self.assertRaises(TypeError):
            RandomSource("1234")


if __name__ == "__main__":
    unittest.main()
