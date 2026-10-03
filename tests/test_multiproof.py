"""Compressed shared-path multi proofs: exhaustive subsets + tampering."""

from __future__ import annotations

import itertools
import unittest

from merkle_evidence.proofs import Checkpoint, MultiProof, verify_multi

from .helpers import VERSION_A, VERSION_B, make_digests, make_tree


def multi_for(n: int, indices, version: str = VERSION_A) -> MultiProof:
    tree = make_tree(n, version)
    digests = make_digests(n)
    idxs = tuple(sorted(indices))
    return MultiProof(version, n, idxs,
                      tuple(digests[i] for i in idxs),
                      tuple(tree.multi_proof_hashes(idxs)))


def checkpoint_for(n: int, version: str = VERSION_A) -> Checkpoint:
    return Checkpoint(version, n, make_tree(n, version).root())


class TestMultiExhaustive(unittest.TestCase):
    def test_all_leaf_subsets_of_small_trees(self):
        # Enumerate every non-empty leaf subset for sizes 1..8.
        for n in range(1, 9):
            ckpt = checkpoint_for(n)
            for r in range(1, n + 1):
                for subset in itertools.combinations(range(n), r):
                    proof = multi_for(n, subset)
                    self.assertTrue(verify_multi(proof, ckpt),
                                    f"n={n} subset={subset}")

    def test_compression_shares_paths(self):
        n = 8
        tree = make_tree(n)
        for subset in itertools.combinations(range(n), 3):
            proof = multi_for(n, subset)
            individual = sum(len(tree.audit_path(i)) for i in subset)
            self.assertLessEqual(len(proof.hashes), individual)
        # Adjacent pair shares its whole path above the parent.
        pair = multi_for(n, (2, 3))
        individual = len(tree.audit_path(2)) + len(tree.audit_path(3))
        self.assertLess(len(pair.hashes), individual)

    def test_full_tree_subset_needs_no_hashes(self):
        n = 6
        proof = multi_for(n, range(n))
        self.assertEqual(proof.hashes, ())
        self.assertTrue(verify_multi(proof, checkpoint_for(n)))

    def test_singleton_matches_membership(self):
        n = 7
        proof = multi_for(n, (4,))
        self.assertTrue(verify_multi(proof, checkpoint_for(n)))


class TestMultiTamper(unittest.TestCase):
    def setUp(self):
        self.n = 7
        self.ckpt = checkpoint_for(self.n)
        self.subset = (1, 2, 5)
        self.proof = multi_for(self.n, self.subset)

    def rebuild(self, **kw) -> MultiProof:
        args = dict(log_version=VERSION_A, tree_size=self.n,
                    leaf_indices=self.subset,
                    record_digests=self.proof.record_digests,
                    hashes=self.proof.hashes)
        args.update(kw)
        return MultiProof(**args)

    def test_tampered_member_content_fails(self):
        digests = list(self.proof.record_digests)
        digests[1] = bytes([digests[1][0] ^ 1]) + digests[1][1:]
        self.assertFalse(verify_multi(
            self.rebuild(record_digests=tuple(digests)), self.ckpt))

    def test_tampered_positions_fail(self):
        # Move the whole proof one position to the right.
        moved = tuple(i + 1 for i in self.subset)
        self.assertFalse(verify_multi(
            self.rebuild(leaf_indices=moved), self.ckpt))
        # Swap two positions (digests stay aligned with original order).
        swapped = (2, 1, 5)
        self.assertFalse(verify_multi(
            self.rebuild(leaf_indices=swapped), self.ckpt))

    def test_unsorted_or_duplicate_indices_rejected(self):
        self.assertFalse(verify_multi(
            self.rebuild(leaf_indices=(5, 1, 2)), self.ckpt))
        self.assertFalse(verify_multi(
            self.rebuild(leaf_indices=(1, 1, 2, 5),
                         record_digests=self.proof.record_digests[:1]
                         + self.proof.record_digests), self.ckpt))

    def test_tampered_tree_size_fails(self):
        for bad in (self.n - 1, self.n + 1):
            self.assertFalse(verify_multi(
                self.rebuild(tree_size=bad), self.ckpt), f"size={bad}")

    def test_tampered_hash_order_fails(self):
        hashes = self.proof.hashes
        self.assertGreaterEqual(len(hashes), 2)
        reordered = (hashes[1], hashes[0]) + hashes[2:]
        self.assertFalse(verify_multi(
            self.rebuild(hashes=reordered), self.ckpt))
        self.assertFalse(verify_multi(
            self.rebuild(hashes=tuple(reversed(hashes))), self.ckpt))

    def test_truncated_and_extended_hash_list_fails(self):
        self.assertFalse(verify_multi(
            self.rebuild(hashes=self.proof.hashes[:-1]), self.ckpt))
        self.assertFalse(verify_multi(
            self.rebuild(hashes=self.proof.hashes + (b"\x00" * 32,)),
            self.ckpt))

    def test_wrong_version_fails(self):
        self.assertFalse(verify_multi(
            self.rebuild(log_version=VERSION_B), self.ckpt))
        other = multi_for(self.n, self.subset, VERSION_B)
        self.assertFalse(verify_multi(other, self.ckpt))

    def test_empty_proof_rejected(self):
        self.assertFalse(verify_multi(
            self.rebuild(leaf_indices=(), record_digests=(), hashes=()),
            self.ckpt))


if __name__ == "__main__":
    unittest.main()
