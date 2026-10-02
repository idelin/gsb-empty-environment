import random
import unittest

from secure_sum_sim.sharing import DEFAULT_PRIME, reconstruct, split_secret


class TestSharing(unittest.TestCase):
    def test_roundtrip_mod_p_edge_values(self):
        for secret in [0, 1, 12345, DEFAULT_PRIME - 1, DEFAULT_PRIME + 7]:
            shares = split_secret(secret, 5, DEFAULT_PRIME, "r", "o", list("abcde"))
            self.assertEqual(reconstruct(shares, DEFAULT_PRIME), secret % DEFAULT_PRIME)

    def test_random_roundtrips(self):
        rng = random.Random(42)
        for _ in range(50):
            secret = rng.randrange(DEFAULT_PRIME)
            shares = split_secret(secret, 4, DEFAULT_PRIME, "r", "o", list("abcd"), rng)
            self.assertEqual(reconstruct(shares, DEFAULT_PRIME), secret)

    def test_missing_shares_returns_incomplete(self):
        with self.assertRaises(ValueError):
            reconstruct([], DEFAULT_PRIME)

    def test_mixed_rounds_rejected(self):
        s1 = split_secret(1, 2, DEFAULT_PRIME, "r1", "o", ["a", "b"])
        s2 = split_secret(1, 2, DEFAULT_PRIME, "r2", "o", ["a", "b"])
        with self.assertRaises(ValueError):
            reconstruct([s1[0], s2[1]], DEFAULT_PRIME)

    def test_wrong_expected_round_rejected(self):
        shares = split_secret(1, 2, DEFAULT_PRIME, "r1", "o", ["a", "b"])
        with self.assertRaises(ValueError):
            reconstruct(shares, DEFAULT_PRIME, expected_round="r2")

    def test_mixed_owners_rejected(self):
        s1 = split_secret(1, 2, DEFAULT_PRIME, "r", "o1", ["a", "b"])
        s2 = split_secret(1, 2, DEFAULT_PRIME, "r", "o2", ["a", "b"])
        with self.assertRaises(ValueError):
            reconstruct([s1[0], s2[1]], DEFAULT_PRIME)

    def test_duplicate_shares_rejected(self):
        shares = split_secret(9, 3, DEFAULT_PRIME, "r", "o", list("abc"))
        with self.assertRaises(ValueError):
            reconstruct([shares[0], shares[0]], DEFAULT_PRIME)

    def test_share_values_in_field(self):
        shares = split_secret(7, 6, DEFAULT_PRIME, "r", "o", list("abcdef"))
        for s in shares:
            self.assertTrue(0 <= s.value < DEFAULT_PRIME)


if __name__ == "__main__":
    unittest.main()
