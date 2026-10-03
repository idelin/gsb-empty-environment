"""Tree shape, padding rule and odd-level handling."""

from __future__ import annotations

import unittest

from merkle_evidence.hashing import EMPTY_ROOT, leaf_hash, node_hash, sha256
from merkle_evidence.tree import MerkleTree, split_point

from .helpers import VERSION_A, VERSION_B, make_digests, make_tree


class TestSplitPoint(unittest.TestCase):
    def test_split_is_largest_power_of_two_strictly_below(self):
        self.assertEqual(split_point(2), 1)
        self.assertEqual(split_point(3), 2)
        self.assertEqual(split_point(4), 2)
        self.assertEqual(split_point(5), 4)
        self.assertEqual(split_point(7), 4)
        self.assertEqual(split_point(8), 4)
        self.assertEqual(split_point(9), 8)
        self.assertEqual(split_point(16), 8)
        self.assertEqual(split_point(17), 16)

    def test_split_rejects_tiny(self):
        with self.assertRaises(ValueError):
            split_point(1)


class TestPaddingRule(unittest.TestCase):
    """RULE-PAD-PROMOTE v1: odd nodes are promoted unchanged, never
    doubled and never paired with a transmitted padding leaf."""

    def test_odd_leaf_promoted_not_doubled(self):
        digests = make_digests(3)
        tree = MerkleTree(digests, VERSION_A)
        leaves = [leaf_hash(i, VERSION_A, d) for i, d in enumerate(digests)]
        promoted = node_hash(node_hash(leaves[0], leaves[1]), leaves[2])
        doubled = node_hash(node_hash(leaves[0], leaves[1]),
                            node_hash(leaves[2], leaves[2]))
        self.assertEqual(tree.root(), promoted)
        self.assertNotEqual(tree.root(), doubled)

    def test_odd_levels_consistent_at_every_level(self):
        # n=6: level splits 4|2; the 2-subtree splits 1|1 -- no padding.
        digests = make_digests(6)
        tree = MerkleTree(digests, VERSION_A)
        lv = [leaf_hash(i, VERSION_A, d) for i, d in enumerate(digests)]
        expected = node_hash(
            node_hash(node_hash(lv[0], lv[1]), node_hash(lv[2], lv[3])),
            node_hash(lv[4], lv[5]),
        )
        self.assertEqual(tree.root(), expected)

    def test_recursive_definition_matches_for_all_sizes(self):
        for n in range(1, 33):
            digests = make_digests(n)
            tree = MerkleTree(digests, VERSION_A)

            def mth(start: int, length: int) -> bytes:
                if length == 1:
                    return leaf_hash(start, VERSION_A, digests[start])
                k = split_point(length)
                return node_hash(mth(start, k), mth(start + k, length - k))

            self.assertEqual(tree.root(), mth(0, n), f"size {n}")

    def test_empty_tree_root(self):
        tree = MerkleTree([], VERSION_A)
        self.assertEqual(tree.root(), EMPTY_ROOT)
        self.assertEqual(tree.root(), sha256(b""))

    def test_deterministic_and_version_bound(self):
        self.assertEqual(make_tree(7, VERSION_A).root(),
                         make_tree(7, VERSION_A).root())
        self.assertNotEqual(make_tree(7, VERSION_A).root(),
                            make_tree(7, VERSION_B).root())

    def test_leaf_position_bound(self):
        # Same content at different positions -> different leaves.
        d = make_digests(1)[0]
        self.assertNotEqual(leaf_hash(0, VERSION_A, d),
                            leaf_hash(1, VERSION_A, d))


if __name__ == "__main__":
    unittest.main()
