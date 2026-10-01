import random
import unittest

from modelcheck.encoding import decode_state, encode_state
from modelcheck.normalizers import IdentityNormalizer, PermutationNormalizer


class TestNormalizers(unittest.TestCase):
    def test_identity_roundtrip(self):
        norm = IdentityNormalizer()
        states = [None, True, 0, -7, "hello", b"\x00\x01", (1, "a", (2, 3)),
                  frozenset({1, 2, 3}), ("nested", (frozenset({"x"}),))]
        for state in states:
            key, recovery = norm.normalize(state)
            self.assertEqual(norm.recover(key, recovery), state)

    def test_permutation_roundtrip_preserves_recovery_mapping(self):
        norm = PermutationNormalizer()
        rng = random.Random(42)
        for _ in range(500):
            state = tuple(rng.randint(0, 5) for _ in range(rng.randint(0, 6)))
            key, recovery = norm.normalize(state)
            # recovery mapping restores the exact original state
            self.assertEqual(norm.recover(key, recovery), state)
            # canonical key is the sorted tuple
            self.assertEqual(decode_state(key), tuple(sorted(state)))

    def test_permutations_share_canonical_key(self):
        norm = PermutationNormalizer()
        key1, rec1 = norm.normalize((1, 3, 2))
        key2, rec2 = norm.normalize((3, 1, 2))
        self.assertEqual(key1, key2)
        self.assertNotEqual(rec1, rec2)  # recovery mappings differ
        self.assertEqual(norm.recover(key1, rec1), (1, 3, 2))
        self.assertEqual(norm.recover(key2, rec2), (3, 1, 2))

    def test_encoding_is_canonical(self):
        self.assertEqual(encode_state(frozenset({3, 1, 2})),
                         encode_state(frozenset({2, 3, 1})))
        self.assertEqual(encode_state((1, (2, 3))), encode_state((1, (2, 3))))


if __name__ == "__main__":
    unittest.main()
