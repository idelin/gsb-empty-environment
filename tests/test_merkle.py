"""Unittest suite for the ordered-evidence Merkle tree.

Covers: membership, padding/odd-size consistency, batch shared-path
compression, append-only consistency, position identity of duplicate
content, version binding / cross-version splice resistance, sender-
padding distrust, and deterministic evidence-log replay.
"""

from __future__ import annotations

import itertools
import json
import unittest

from merkle_evidence import (
    SCHEME_VERSION,
    EvidenceLog,
    MerkleTree,
    ProofError,
    ReplayError,
    batch_proof,
    consistency_proof,
    is_valid,
    membership_proof,
    verify_batch,
    verify_consistency,
    verify_membership,
)
from merkle_evidence.hashing import empty_root, pad_subtree_root
from merkle_evidence.ids import PAD_MARKER
from merkle_evidence.proofs import BatchProof, ConsistencyProof, MembershipProof

V = SCHEME_VERSION
OTHER_V = "merkle-evidence/2"


def make_tree(n: int, version: str = V, prefix: bytes = b"rec-") -> MerkleTree:
    tree = MerkleTree(version)
    for i in range(n):
        tree.append(prefix + f"{i:03d}".encode())
    return tree


def clone_membership(p: MembershipProof, **changes) -> MembershipProof:
    d = p.to_dict()
    d.update(changes)
    return MembershipProof.from_dict(d)


class TestMembership(unittest.TestCase):
    def test_all_sizes_and_indices_verify(self):
        for n in range(1, 17):
            tree = make_tree(n)
            root = tree.root()
            for i in range(n):
                proof = membership_proof(tree, i)
                verify_membership(proof, root, V)  # must not raise

    def test_odd_levels_handled_consistently(self):
        # Odd leaf counts pad to the next power of two; roots are stable
        # and distinct across sizes, and every proof verifies.
        roots = set()
        for n in (1, 3, 5, 7, 9, 13):
            tree = make_tree(n)
            root = tree.root()
            self.assertNotIn(root, roots)
            roots.add(root)
            for i in range(n):
                verify_membership(membership_proof(tree, i), root, V)

    def test_root_is_deterministic(self):
        self.assertEqual(make_tree(6).root(), make_tree(6).root())

    def test_empty_tree_root(self):
        tree = MerkleTree(V)
        self.assertEqual(tree.root(), empty_root(V))
        with self.assertRaises(ValueError):
            membership_proof(tree, 0)

    def test_tampered_leaf_content_fails(self):
        tree = make_tree(7)
        proof = membership_proof(tree, 3)
        forged = clone_membership(proof, record=b"forged".hex())
        with self.assertRaises(ProofError):
            verify_membership(forged, tree.root(), V)

    def test_tampered_leaf_position_fails(self):
        tree = make_tree(7)
        root = tree.root()
        for i in range(7):
            proof = membership_proof(tree, i)
            for j in range(7):
                if i == j:
                    continue
                moved = clone_membership(proof, leaf_index=j)
                with self.assertRaises(ProofError):
                    verify_membership(moved, root, V)

    def test_tampered_tree_size_fails(self):
        tree = make_tree(7)
        root = tree.root()
        proof = membership_proof(tree, 3)
        for bad_size in (1, 6, 8, 9, 16):
            resized = clone_membership(proof, tree_size=bad_size)
            with self.assertRaises(ProofError, msg=f"size {bad_size}"):
                verify_membership(resized, root, V)

    def test_tampered_path_order_fails(self):
        tree = make_tree(8)  # full tree: every sibling entry is a real hash
        root = tree.root()
        proof = membership_proof(tree, 0)
        self.assertEqual(len(proof.siblings), 3)
        for a, b in itertools.permutations(range(3), 2):
            sibs = list(proof.siblings)
            sibs[a], sibs[b] = sibs[b], sibs[a]
            reordered = clone_membership(proof, siblings=sibs)
            with self.assertRaises(ProofError):
                verify_membership(reordered, root, V)

    def test_wrong_expected_root_fails(self):
        tree = make_tree(5)
        other = make_tree(6)
        proof = membership_proof(tree, 2)
        with self.assertRaises(ProofError):
            verify_membership(proof, other.root(), V)

    def test_wrong_version_fails(self):
        tree = make_tree(5)
        proof = membership_proof(tree, 2)
        with self.assertRaises(ProofError):
            verify_membership(proof, tree.root(), OTHER_V)

    def test_proof_serialization_roundtrip(self):
        tree = make_tree(9)
        proof = membership_proof(tree, 7)
        blob = json.dumps(proof.to_dict())
        restored = MembershipProof.from_dict(json.loads(blob))
        verify_membership(restored, tree.root(), V)


class TestDuplicateContent(unittest.TestCase):
    def test_duplicates_keep_position_identity(self):
        tree = MerkleTree(V)
        for i in range(6):
            tree.append(b"same-content" if i in (2, 5) else f"u{i}".encode())
        root = tree.root()
        p2 = membership_proof(tree, 2)
        p5 = membership_proof(tree, 5)
        verify_membership(p2, root, V)
        verify_membership(p5, root, V)
        # Identical content, but proofs are not interchangeable.
        swapped = clone_membership(p2, leaf_index=5)
        with self.assertRaises(ProofError):
            verify_membership(swapped, root, V)
        swapped = clone_membership(p5, leaf_index=2)
        with self.assertRaises(ProofError):
            verify_membership(swapped, root, V)
        # Leaf hashes differ despite identical content.
        self.assertNotEqual(p2.to_dict()["record"] and tree.levels()[0][2],
                            tree.levels()[0][5])


class TestPaddingDistrust(unittest.TestCase):
    def test_real_sibling_cannot_be_hidden_as_padding(self):
        tree = make_tree(7)
        root = tree.root()
        proof = membership_proof(tree, 3)  # all siblings are real nodes
        self.assertNotIn(PAD_MARKER, proof.siblings)
        for level in range(len(proof.siblings)):
            sibs = list(proof.siblings)
            sibs[level] = PAD_MARKER  # sender claims "recompute it yourself"
            forged = clone_membership(proof, siblings=sibs)
            with self.assertRaises(ProofError, msg=f"level {level}"):
                verify_membership(forged, root, V)

    def test_sender_padding_hash_must_match_recomputation(self):
        tree = make_tree(5)  # index 4 has a padding sibling at level 0
        root = tree.root()
        proof = membership_proof(tree, 4)
        self.assertEqual(proof.siblings[0], PAD_MARKER)
        # Sender supplies a bogus hash where padding belongs: rejected.
        forged = clone_membership(
            proof, siblings=[(b"\x00" * 32).hex()] + proof.siblings[1:])
        with self.assertRaises(ProofError):
            verify_membership(forged, root, V)
        # Sender supplies the *correct* recomputed padding hash: accepted,
        # because the verifier checks it against its own recomputation.
        honest = clone_membership(
            proof,
            siblings=[pad_subtree_root(V, 0, 5).hex()] + proof.siblings[1:],
        )
        verify_membership(honest, root, V)


class TestBatchProof(unittest.TestCase):
    def test_all_leaf_subsets_of_small_tree(self):
        n = 5
        tree = make_tree(n)
        root = tree.root()
        for r in range(1, n + 1):
            for combo in itertools.combinations(range(n), r):
                proof = batch_proof(tree, list(combo))
                verify_batch(proof, root, V)

    def test_shared_paths_compress(self):
        tree = make_tree(8)
        proof = batch_proof(tree, [0, 1, 2, 3])
        # 4 members x 3 levels = 12 naive siblings; sharing reduces this.
        self.assertLess(len(proof.nodes), 12)
        verify_batch(proof, tree.root(), V)

    def test_tampered_batch_fails(self):
        tree = make_tree(5)
        root = tree.root()
        proof = batch_proof(tree, [1, 3])
        # Records permuted relative to indices.
        d = proof.to_dict()
        d["records"] = [d["records"][1], d["records"][0]]
        with self.assertRaises(ProofError):
            verify_batch(BatchProof.from_dict(d), root, V)
        # A required node removed.
        d = proof.to_dict()
        d["nodes"].pop(next(iter(d["nodes"])))
        with self.assertRaises(ProofError):
            verify_batch(BatchProof.from_dict(d), root, V)
        # An extra (unused) node added: anti-splicing strictness.
        d = proof.to_dict()
        d["nodes"]["0:7"] = "00" * 32
        with self.assertRaises(ProofError):
            verify_batch(BatchProof.from_dict(d), root, V)
        # Index shifted.
        d = proof.to_dict()
        d["indices"] = [1, 4]
        with self.assertRaises(ProofError):
            verify_batch(BatchProof.from_dict(d), root, V)
        # Tree size tampered.
        d = proof.to_dict()
        d["tree_size"] = 6
        with self.assertRaises(ProofError):
            verify_batch(BatchProof.from_dict(d), root, V)

    def test_batch_serialization_roundtrip(self):
        tree = make_tree(6)
        proof = batch_proof(tree, [0, 2, 5])
        restored = BatchProof.from_dict(json.loads(json.dumps(proof.to_dict())))
        verify_batch(restored, tree.root(), V)


class TestConsistency(unittest.TestCase):
    def _incremental_roots(self, n: int):
        tree = MerkleTree(V)
        roots = [tree.root()]
        for i in range(n):
            tree.append(f"rec-{i:03d}".encode())
            roots.append(tree.root())
        return tree, roots

    def test_all_size_pairs_small_trees(self):
        n = 9
        tree, roots = self._incremental_roots(n)
        for old in range(0, n + 1):
            for new in range(old, n + 1):
                sub = MerkleTree(V)
                sub.records = tree.records[:new]
                proof = consistency_proof(sub, old)
                verify_consistency(proof, roots[old], roots[new], V)

    def test_tampered_consistency_fails(self):
        tree, roots = self._incremental_roots(7)
        proof = consistency_proof(tree, 5)
        # Wrong old root.
        with self.assertRaises(ProofError):
            verify_consistency(proof, roots[4], roots[7], V)
        # Wrong new root.
        with self.assertRaises(ProofError):
            verify_consistency(proof, roots[5], roots[6], V)
        # Sizes tampered.
        d = proof.to_dict()
        d["old_size"] = 4
        with self.assertRaises(ProofError):
            verify_consistency(ConsistencyProof.from_dict(d),
                               roots[4], roots[7], V)
        # A node corrupted.
        d = proof.to_dict()
        key = next(iter(d["new_nodes"]))
        d["new_nodes"][key] = "ff" * 32
        with self.assertRaises(ProofError):
            verify_consistency(ConsistencyProof.from_dict(d),
                               roots[5], roots[7], V)
        # old/new node sets swapped.
        d = proof.to_dict()
        d["old_nodes"], d["new_nodes"] = d["new_nodes"], d["old_nodes"]
        with self.assertRaises(ProofError):
            verify_consistency(ConsistencyProof.from_dict(d),
                               roots[5], roots[7], V)

    def test_consistency_serialization_roundtrip(self):
        tree, roots = self._incremental_roots(6)
        proof = consistency_proof(tree, 3)
        restored = ConsistencyProof.from_dict(
            json.loads(json.dumps(proof.to_dict())))
        verify_consistency(restored, roots[3], roots[6], V)

    def test_empty_old_tree(self):
        tree, roots = self._incremental_roots(4)
        proof = consistency_proof(tree, 0)
        verify_consistency(proof, roots[0], roots[4], V)


class TestCrossVersionSplice(unittest.TestCase):
    def test_membership_splice_fails(self):
        t1 = make_tree(6, version=V)
        t2 = make_tree(6, version=OTHER_V)
        p1 = membership_proof(t1, 2)
        p2 = membership_proof(t2, 2)
        # Leaf+record from v1, path from v2, claimed as v1.
        splice = clone_membership(p1, siblings=p2.siblings)
        with self.assertRaises(ProofError):
            verify_membership(splice, t1.root(), V)
        # Path from v1, version string flipped to v2.
        splice = clone_membership(p1, version=OTHER_V)
        with self.assertRaises(ProofError):
            verify_membership(splice, t2.root(), OTHER_V)
        # Root from v1, proof from v2.
        with self.assertRaises(ProofError):
            verify_membership(p2, t1.root(), OTHER_V)

    def test_batch_splice_fails(self):
        t1 = make_tree(5, version=V)
        t2 = make_tree(5, version=OTHER_V)
        b1 = batch_proof(t1, [1, 2])
        b2 = batch_proof(t2, [1, 2])
        d = b1.to_dict()
        d["nodes"] = b2.to_dict()["nodes"]
        with self.assertRaises(ProofError):
            verify_batch(BatchProof.from_dict(d), t1.root(), V)

    def test_consistency_splice_fails(self):
        def build(version):
            tree = MerkleTree(version)
            roots = [tree.root()]
            for i in range(6):
                tree.append(f"rec-{i:03d}".encode())
                roots.append(tree.root())
            return tree, roots

        t1, r1 = build(V)
        t2, r2 = build(OTHER_V)
        c1 = consistency_proof(t1, 3)
        c2 = consistency_proof(t2, 3)
        d = c1.to_dict()
        d["new_nodes"] = c2.to_dict()["new_nodes"]
        with self.assertRaises(ProofError):
            verify_consistency(ConsistencyProof.from_dict(d), r1[3], r1[6], V)


class TestEvidenceReplay(unittest.TestCase):
    def test_replay_roundtrip(self):
        log = EvidenceLog()
        for i in range(7):
            log.append_fact(f"evidence-{i}".encode())
        expected = log.snapshot()
        replayed = EvidenceLog.replay(log.to_json())
        self.assertEqual(replayed, expected)

    def test_replay_detects_tampering(self):
        log = EvidenceLog()
        for i in range(4):
            log.append_fact(f"evidence-{i}".encode())
        log.snapshot()
        doc = json.loads(log.to_json())
        # Tamper with a recorded payload.
        doc["steps"][1]["payload"] = "ff" * 8
        with self.assertRaises(ReplayError):
            EvidenceLog.replay(doc)
        # Tamper with a snapshot root.
        doc = json.loads(log.to_json())
        doc["steps"][-1]["root"] = "00" * 32
        with self.assertRaises(ReplayError):
            EvidenceLog.replay(doc)
        # Tamper with step ordering.
        doc = json.loads(log.to_json())
        doc["steps"][0], doc["steps"][1] = doc["steps"][1], doc["steps"][0]
        with self.assertRaises(ReplayError):
            EvidenceLog.replay(doc)

    def test_step_and_fact_ids_stable(self):
        log = EvidenceLog()
        fid0 = log.append_fact(b"a")
        fid1 = log.append_fact(b"b")
        log.snapshot()
        self.assertEqual((fid0, fid1), ("fact:000000@1", "fact:000001@1"))
        self.assertEqual(
            [s["step_id"] for s in log.steps],
            ["step:000000@1", "step:000001@1", "step:000002@1"],
        )
        self.assertTrue(all(s["version"] == V for s in log.steps))


if __name__ == "__main__":
    unittest.main()
