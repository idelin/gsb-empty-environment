"""Membership proofs: exhaustive small-tree checks and tamper rejection."""

from __future__ import annotations

import unittest

from merkle_evidence.proofs import (
    Checkpoint,
    MembershipProof,
    verify_membership,
)

from .helpers import VERSION_A, VERSION_B, make_digests, make_tree


def proof_for(n: int, index: int, version: str = VERSION_A) -> MembershipProof:
    tree = make_tree(n, version)
    digests = make_digests(n)
    return MembershipProof(version, n, index, digests[index],
                           tuple(tree.audit_path(index)))


def checkpoint_for(n: int, version: str = VERSION_A) -> Checkpoint:
    return Checkpoint(version, n, make_tree(n, version).root())


class TestMembershipExhaustive(unittest.TestCase):
    def test_all_positions_all_small_sizes(self):
        for n in range(1, 21):
            ckpt = checkpoint_for(n)
            for i in range(n):
                self.assertTrue(verify_membership(proof_for(n, i), ckpt),
                                f"n={n} i={i}")

    def test_single_leaf_tree(self):
        proof = proof_for(1, 0)
        self.assertEqual(proof.path, ())
        self.assertTrue(verify_membership(proof, checkpoint_for(1)))


class TestMembershipTamper(unittest.TestCase):
    def setUp(self):
        self.n = 7  # odd levels on every path
        self.ckpt = checkpoint_for(self.n)
        self.digests = make_digests(self.n)

    def test_tampered_leaf_content_fails(self):
        for i in range(self.n):
            proof = proof_for(self.n, i)
            bad = bytes([proof.record_digest[0] ^ 1]) + proof.record_digest[1:]
            tampered = MembershipProof(VERSION_A, self.n, i, bad, proof.path)
            self.assertFalse(verify_membership(tampered, self.ckpt), f"i={i}")

    def test_tampered_leaf_position_fails(self):
        # Proof generated for index i must not verify at any j != i.
        for i in range(self.n):
            proof = proof_for(self.n, i)
            for j in range(self.n):
                if i == j:
                    continue
                moved = MembershipProof(VERSION_A, self.n, j,
                                        proof.record_digest, proof.path)
                self.assertFalse(verify_membership(moved, self.ckpt),
                                 f"i={i} j={j}")

    def test_duplicate_content_positions_not_swappable(self):
        # Identical record content at positions 1 and 4 keeps positional
        # identity: the proof for one position must fail at the other.
        d = make_digests(1)[0]
        digests = [d if i in (1, 4) else x
                   for i, x in enumerate(make_digests(self.n))]
        from merkle_evidence.tree import MerkleTree
        tree = MerkleTree(digests, VERSION_A)
        ckpt = Checkpoint(VERSION_A, self.n, tree.root())
        p1 = MembershipProof(VERSION_A, self.n, 1, d, tuple(tree.audit_path(1)))
        p4 = MembershipProof(VERSION_A, self.n, 4, d, tuple(tree.audit_path(4)))
        self.assertTrue(verify_membership(p1, ckpt))
        self.assertTrue(verify_membership(p4, ckpt))
        swapped = MembershipProof(VERSION_A, self.n, 4, d, p1.path)
        self.assertFalse(verify_membership(swapped, ckpt))
        swapped_back = MembershipProof(VERSION_A, self.n, 1, d, p4.path)
        self.assertFalse(verify_membership(swapped_back, ckpt))

    def test_tampered_tree_size_fails(self):
        # Regression: a proof must not verify under a different claimed
        # size even if the recomputed hash accidentally matches the root.
        proof = proof_for(self.n, 4)
        for bad_size in (self.n - 1, self.n + 1, self.n + 5):
            tampered = MembershipProof(VERSION_A, bad_size, 4,
                                       proof.record_digest, proof.path)
            self.assertFalse(verify_membership(tampered, self.ckpt),
                             f"size={bad_size}")

    def test_tampered_path_order_fails(self):
        proof = proof_for(self.n, 4)
        self.assertGreaterEqual(len(proof.path), 2)
        reversed_path = tuple(reversed(proof.path))
        tampered = MembershipProof(VERSION_A, self.n, 4,
                                   proof.record_digest, reversed_path)
        self.assertFalse(verify_membership(tampered, self.ckpt))
        swapped = (proof.path[1], proof.path[0]) + proof.path[2:]
        tampered2 = MembershipProof(VERSION_A, self.n, 4,
                                    proof.record_digest, swapped)
        self.assertFalse(verify_membership(tampered2, self.ckpt))

    def test_truncated_and_extended_paths_fail(self):
        proof = proof_for(self.n, 4)
        short = MembershipProof(VERSION_A, self.n, 4, proof.record_digest,
                                proof.path[:-1])
        self.assertFalse(verify_membership(short, self.ckpt))
        # A prover-supplied "padding leaf" hash is never accepted.
        padded = MembershipProof(VERSION_A, self.n, 4, proof.record_digest,
                                 proof.path + (b"\x00" * 32,))
        self.assertFalse(verify_membership(padded, self.ckpt))

    def test_wrong_version_fails(self):
        proof = proof_for(self.n, 4)
        other = MembershipProof(VERSION_B, self.n, 4, proof.record_digest,
                                proof.path)
        self.assertFalse(verify_membership(other, self.ckpt))
        # Proof genuinely made under another version also fails here.
        self.assertFalse(verify_membership(proof_for(self.n, 4, VERSION_B),
                                           self.ckpt))

    def test_wrong_checkpoint_root_fails(self):
        proof = proof_for(self.n, 4)
        other_ckpt = checkpoint_for(self.n, VERSION_B)
        self.assertFalse(verify_membership(proof, other_ckpt))


if __name__ == "__main__":
    unittest.main()
