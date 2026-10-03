"""Replay, stable IDs/versions, and cross-version splice resistance."""

from __future__ import annotations

import unittest

from merkle_evidence.model import EvidenceLog, EvidenceRecord, Fact, Rule, Step
from merkle_evidence.proofs import (
    Checkpoint,
    MembershipProof,
    MultiProof,
    verify_membership,
    verify_multi,
)
from merkle_evidence.replay import replay_log
from merkle_evidence.tree import MerkleTree

from .helpers import VERSION_A, VERSION_B, make_digests, make_log, make_record


class TestModel(unittest.TestCase):
    def test_digest_is_deterministic(self):
        self.assertEqual(make_record(1).digest(), make_record(1).digest())

    def test_stable_ids_and_versions_are_bound(self):
        base = make_record(1)
        other_fact_id = EvidenceRecord(
            base.record_id, base.version,
            facts=(Fact("FACT-9999", 1, "synthetic observation #1"),),
            rules=base.rules, steps=base.steps)
        self.assertNotEqual(base.digest(), other_fact_id.digest())
        other_fact_version = EvidenceRecord(
            base.record_id, base.version,
            facts=(Fact("FACT-0001", 2, "synthetic observation #1"),),
            rules=base.rules, steps=base.steps)
        self.assertNotEqual(base.digest(), other_fact_version.digest())

    def test_validate_catches_dangling_refs(self):
        step = Step("STEP-X", 1, "collect", fact_refs=(("FACT-X", 1),))
        rec = EvidenceRecord("REC-X", 1, facts=(), rules=(), steps=(step,))
        with self.assertRaises(ValueError):
            rec.validate()


class TestReplay(unittest.TestCase):
    def test_honest_log_replays_ok(self):
        log = make_log(7)
        tree = MerkleTree(log.digests(), log.log_version)
        report = replay_log(log, expected_root=tree.root())
        self.assertTrue(report.ok)
        self.assertEqual(report.size, 7)
        self.assertEqual(report.root, tree.root())

    def test_tampered_fact_version_detected(self):
        log = make_log(7)
        tree = MerkleTree(log.digests(), log.log_version)
        records = list(log.records)
        records[3] = make_record(4, fact_version=99)
        tampered = EvidenceLog(log.log_id, log.log_version, tuple(records))
        report = replay_log(tampered, expected_root=tree.root())
        self.assertFalse(report.ok)

    def test_reordered_records_detected(self):
        log = make_log(7)
        tree = MerkleTree(log.digests(), log.log_version)
        records = list(log.records)
        records[1], records[2] = records[2], records[1]
        reordered = EvidenceLog(log.log_id, log.log_version, tuple(records))
        self.assertFalse(replay_log(reordered, expected_root=tree.root()).ok)

    def test_log_version_is_bound(self):
        log_a = make_log(5, VERSION_A)
        log_b = make_log(5, VERSION_B)
        self.assertNotEqual(replay_log(log_a).root, replay_log(log_b).root)


class TestCrossVersionSplice(unittest.TestCase):
    """Proofs from different log versions must not be spliced into a
    false acceptance."""

    def setUp(self):
        self.n = 7
        self.digests = make_digests(self.n)
        self.tree_a = MerkleTree(self.digests, VERSION_A)
        self.tree_b = MerkleTree(self.digests, VERSION_B)
        self.ckpt_a = Checkpoint(VERSION_A, self.n, self.tree_a.root())
        self.ckpt_b = Checkpoint(VERSION_B, self.n, self.tree_b.root())

    def test_membership_proof_not_portable_across_versions(self):
        for i in range(self.n):
            proof_a = MembershipProof(VERSION_A, self.n, i, self.digests[i],
                                      tuple(self.tree_a.audit_path(i)))
            self.assertFalse(verify_membership(proof_a, self.ckpt_b))
            # Re-labelled splice: claim version B, carry version A hashes.
            spliced = MembershipProof(VERSION_B, self.n, i, self.digests[i],
                                      tuple(self.tree_a.audit_path(i)))
            self.assertFalse(verify_membership(spliced, self.ckpt_b))

    def test_multi_proof_not_portable_across_versions(self):
        idxs = (1, 2, 5)
        hashes_a = tuple(self.tree_a.multi_proof_hashes(idxs))
        spliced = MultiProof(VERSION_B, self.n, idxs,
                             tuple(self.digests[i] for i in idxs), hashes_a)
        self.assertFalse(verify_multi(spliced, self.ckpt_b))
        # Mixing hashes from both versions' proofs also fails.
        hashes_b = tuple(self.tree_b.multi_proof_hashes(idxs))
        mixed = MultiProof(VERSION_B, self.n, idxs,
                           tuple(self.digests[i] for i in idxs),
                           hashes_b[:1] + hashes_a[1:])
        self.assertFalse(verify_multi(mixed, self.ckpt_b))

    def test_checkpoint_sizes_must_match(self):
        bigger = MerkleTree(self.digests + make_digests(1, "extra"),
                            VERSION_A)
        ckpt_bigger = Checkpoint(VERSION_A, self.n + 1, bigger.root())
        proof = MembershipProof(VERSION_A, self.n, 3, self.digests[3],
                                tuple(self.tree_a.audit_path(3)))
        self.assertFalse(verify_membership(proof, ckpt_bigger))


if __name__ == "__main__":
    unittest.main()
