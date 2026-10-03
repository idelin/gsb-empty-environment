"""Append-only consistency proofs: exhaustive (m, n) + tampering."""

from __future__ import annotations

import unittest

from merkle_evidence.hashing import EMPTY_ROOT
from merkle_evidence.proofs import (
    Checkpoint,
    ConsistencyProof,
    verify_consistency,
)

from .helpers import VERSION_A, VERSION_B, make_tree


def consistency_for(m: int, n: int, version: str = VERSION_A
                    ) -> ConsistencyProof:
    tree = make_tree(n, version)
    return ConsistencyProof(version, m, n, tuple(tree.consistency_proof(m)))


def checkpoint_for(n: int, version: str = VERSION_A) -> Checkpoint:
    return Checkpoint(version, n, make_tree(n, version).root())


class TestConsistencyExhaustive(unittest.TestCase):
    def test_all_prefix_pairs(self):
        for n in range(1, 18):
            new_ckpt = checkpoint_for(n)
            for m in range(0, n + 1):
                old_ckpt = checkpoint_for(m) if m else Checkpoint(
                    VERSION_A, 0, EMPTY_ROOT)
                proof = consistency_for(m, n)
                self.assertTrue(
                    verify_consistency(proof, old_ckpt, new_ckpt),
                    f"m={m} n={n}")

    def test_empty_old_tree(self):
        proof = consistency_for(0, 5)
        self.assertEqual(proof.hashes, ())
        old = Checkpoint(VERSION_A, 0, EMPTY_ROOT)
        self.assertTrue(verify_consistency(proof, old, checkpoint_for(5)))

    def test_same_size(self):
        proof = consistency_for(5, 5)
        self.assertEqual(proof.hashes, ())
        ckpt = checkpoint_for(5)
        self.assertTrue(verify_consistency(proof, ckpt, ckpt))


class TestConsistencyTamper(unittest.TestCase):
    def setUp(self):
        self.m, self.n = 3, 7
        self.old_ckpt = checkpoint_for(self.m)
        self.new_ckpt = checkpoint_for(self.n)
        self.proof = consistency_for(self.m, self.n)
        self.assertGreaterEqual(len(self.proof.hashes), 2)

    def rebuild(self, **kw) -> ConsistencyProof:
        args = dict(log_version=VERSION_A, old_size=self.m,
                    new_size=self.n, hashes=self.proof.hashes)
        args.update(kw)
        return ConsistencyProof(**args)

    def test_tampered_sizes_fail(self):
        self.assertFalse(verify_consistency(
            self.rebuild(old_size=self.m + 1), self.old_ckpt, self.new_ckpt))
        self.assertFalse(verify_consistency(
            self.rebuild(new_size=self.n + 1), self.old_ckpt, self.new_ckpt))
        self.assertFalse(verify_consistency(
            self.rebuild(old_size=self.n, new_size=self.m),
            self.old_ckpt, self.new_ckpt))

    def test_tampered_hash_order_fails(self):
        hashes = self.proof.hashes
        reordered = (hashes[1], hashes[0]) + hashes[2:]
        self.assertFalse(verify_consistency(
            self.rebuild(hashes=reordered), self.old_ckpt, self.new_ckpt))
        self.assertFalse(verify_consistency(
            self.rebuild(hashes=tuple(reversed(hashes))),
            self.old_ckpt, self.new_ckpt))

    def test_truncated_and_extended_fail(self):
        self.assertFalse(verify_consistency(
            self.rebuild(hashes=self.proof.hashes[:-1]),
            self.old_ckpt, self.new_ckpt))
        self.assertFalse(verify_consistency(
            self.rebuild(hashes=self.proof.hashes + (b"\x00" * 32,)),
            self.old_ckpt, self.new_ckpt))

    def test_wrong_checkpoints_fail(self):
        # Old checkpoint that is not a real prefix state.
        fake_old = checkpoint_for(self.m, VERSION_B)
        fake_old = Checkpoint(VERSION_A, self.m, fake_old.root)
        self.assertFalse(verify_consistency(
            self.proof, fake_old, self.new_ckpt))
        # New checkpoint from a different (non-prefix) history.
        other_new = Checkpoint(VERSION_A, self.n,
                               make_tree(self.n, VERSION_A, tag="x").root())
        self.assertFalse(verify_consistency(
            self.proof, self.old_ckpt, other_new))

    def test_cross_version_splice_fails(self):
        # Proof generated under VERSION_B must not verify VERSION_A
        # checkpoints, and vice versa.
        proof_b = consistency_for(self.m, self.n, VERSION_B)
        self.assertFalse(verify_consistency(
            proof_b, self.old_ckpt, self.new_ckpt))
        # Claiming VERSION_A while carrying VERSION_B hashes also fails.
        spliced = self.rebuild(hashes=proof_b.hashes)
        self.assertFalse(verify_consistency(
            spliced, self.old_ckpt, self.new_ckpt))


if __name__ == "__main__":
    unittest.main()
