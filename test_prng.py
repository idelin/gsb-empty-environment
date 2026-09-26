"""Tests for the explicit key-driven PRNG core."""

import unittest

from keyrng import KeyCollisionError, KeyRegistry, Tensor, fold_in, key, random_bits, split, uniform
from keyrng.prng import GOLDEN, MASK64


def reference_mix64(x: int) -> int:
    """Independent reimplementation of the SplitMix64 finaliser."""
    x &= 0xFFFFFFFFFFFFFFFF
    x ^= x >> 30
    x = (x * 0xBF58476D1CE4E5B9) & 0xFFFFFFFFFFFFFFFF
    x ^= x >> 27
    x = (x * 0x94D049BB133111EB) & 0xFFFFFFFFFFFFFFFF
    x ^= x >> 31
    return x & 0xFFFFFFFFFFFFFFFF


def reference_bits(k, count):
    """Independent reimplementation of random_bits for validation."""
    return [
        reference_mix64(k.a ^ reference_mix64((k.b + i * GOLDEN) & MASK64))
        for i in range(count)
    ]


class TestKeyDeterminism(unittest.TestCase):
    def test_same_key_same_shape_identical(self):
        k = key(1234)
        self.assertTrue(uniform(k, (3, 4)).exact_equal(uniform(k, (3, 4))))

    def test_different_keys_differ(self):
        a = uniform(key(1), (64,))
        b = uniform(key(2), (64,))
        self.assertFalse(a.exact_equal(b))

    def test_same_key_different_shape_same_prefix(self):
        # The stream is a pure function of (key, counter); shape only
        # decides how many words are consumed.
        k = key(7)
        flat = uniform(k, (6,))
        matrix = uniform(k, (2, 3))
        self.assertEqual(flat.data, matrix.data)

    def test_fold_in_changes_stream(self):
        k = key(0)
        self.assertFalse(
            uniform(fold_in(k, "a"), (8,)).exact_equal(uniform(fold_in(k, "b"), (8,)))
        )


class TestStateIndependentSubstreams(unittest.TestCase):
    def test_interleaved_consumption_does_not_change_streams(self):
        ka, kb = split(key(99))
        # Sequential: draw everything from ka, then everything from kb.
        seq_a1, seq_a2 = uniform(ka, (5,)), uniform(ka, (5,))
        seq_b1, seq_b2 = uniform(kb, (5,)), uniform(kb, (5,))
        # Interleaved execution order.
        int_a1 = uniform(ka, (5,))
        int_b1 = uniform(kb, (5,))
        int_a2 = uniform(ka, (5,))
        int_b2 = uniform(kb, (5,))
        self.assertTrue(seq_a1.exact_equal(int_a1))
        self.assertTrue(seq_a2.exact_equal(int_a2))
        self.assertTrue(seq_b1.exact_equal(int_b1))
        self.assertTrue(seq_b2.exact_equal(int_b2))

    def test_split_subkeys_are_distinct_and_order_free(self):
        subs = split(key(2024), 4)
        self.assertEqual(len({s.data for s in subs}), 4)
        forward = [uniform(s, (4,)) for s in subs]
        backward = [uniform(s, (4,)) for s in reversed(subs)][::-1]
        for f, b in zip(forward, backward):
            self.assertTrue(f.exact_equal(b))

    def test_subkeys_differ_from_parent(self):
        parent = key(5)
        (child,) = split(parent, 1)
        self.assertFalse(
            uniform(parent, (16,)).exact_equal(uniform(child, (16,)))
        )


class TestIndependentNumericalValidation(unittest.TestCase):
    def test_random_bits_matches_independent_reference(self):
        for seed in (0, 1, 2**32, 2**63 + 7):
            k = key(seed)
            self.assertEqual(random_bits(k, 40), reference_bits(k, 40))

    def test_uniform_matches_top53bit_formula(self):
        k = key(31415)
        bits = reference_bits(k, 50)
        expected = [(w >> 11) * 2.0**-53 for w in bits]
        self.assertEqual(uniform(k, (50,)).data, expected)

    def test_uniform_statistical_sanity(self):
        samples = uniform(key(2718), (20000,))
        self.assertTrue(all(0.0 <= u < 1.0 for u in samples.data))
        mean = samples.mean()
        self.assertAlmostEqual(mean, 0.5, delta=0.02)
        var = sum((u - mean) ** 2 for u in samples.data) / len(samples.data)
        self.assertAlmostEqual(var, 1.0 / 12.0, delta=0.01)


class TestEdgeCases(unittest.TestCase):
    def test_empty_tensor(self):
        k = key(1)
        for shape in ((0,), (0, 3), (2, 0, 4)):
            t = uniform(k, shape)
            self.assertEqual(t.shape, shape)
            self.assertEqual(t.data, [])
        self.assertEqual(random_bits(k, 0), [])

    def test_invalid_arguments(self):
        with self.assertRaises(ValueError):
            key(-1)
        with self.assertRaises(ValueError):
            split(key(0), 0)
        with self.assertRaises(ValueError):
            random_bits(key(0), -1)
        with self.assertRaises(ValueError):
            uniform(key(0), (2, -1))

    def test_strict_key_registry_detects_reuse(self):
        registry = KeyRegistry()
        k = key(8)
        registry.register(k)
        with self.assertRaises(KeyCollisionError):
            registry.register(k)
        registry.register(fold_in(k, "other"))
        self.assertEqual(len(registry), 2)


if __name__ == "__main__":
    unittest.main()
