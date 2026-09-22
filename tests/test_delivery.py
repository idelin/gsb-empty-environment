import tempfile
import unittest
import shutil
from pathlib import Path

from rdl import DeliveryManager, Event, PartConfig
from rdl.errors import (
    CapacityExceededError,
    PartitionFrozenError,
    ProofRejectedError,
)
from rdl.storage import CommitCrash, FailureController


class DeliveryTestBase(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.fc = FailureController()
        self.mgr = DeliveryManager(self.root, failures=self.fc)
        self.cfg = PartConfig(
            partition="p", modulus=16, initial_seq=1, initial_abs=0,
            capacity_live=64, memory_slots=4, inflight_max=8,
        )
        self.mgr.create_partition(self.cfg)

    def tearDown(self):
        shutil.rmtree(self.root)

    def feed(self, *seqs, epoch=1):
        for seq in seqs:
            res = self.mgr.ingest(Event("p", epoch, seq, {"v": seq}))
            self.assertTrue(res.accepted, seq)

    def delivered_ids(self, result):
        return [i.delivery_id for i in result.delivered]


class GapTests(DeliveryTestBase):
    def test_gap_blocks_head(self):
        self.feed(1, 4, 3)
        res = self.mgr.pump("p")
        self.assertEqual(self.delivered_ids(res), ["dlv:p:0"])
        self.assertEqual(res.blocked, "gap")

    def test_proof_with_wrong_epoch_rejected(self):
        self.feed(1)
        self.mgr.pump("p")
        with self.assertRaises(ProofRejectedError):
            self.mgr.grant_skip("p", epoch=9, start_seq=2, end_seq=2)

    def test_proof_unblocks_in_order(self):
        self.feed(1, 4, 3)
        self.mgr.pump("p")
        g = self.mgr.grant_skip("p", 1, 2, 2)
        self.assertEqual((g.abs_start, g.abs_end), (1, 1))
        res = self.mgr.pump("p")
        self.assertEqual(self.delivered_ids(res), ["dlv:p:2", "dlv:p:3"])
        proof = self.mgr._get("p").proofs[g.proof_id]
        self.assertTrue(proof.crossed)

    def test_revoke_before_cross(self):
        self.feed(1)
        self.mgr.pump("p")
        g = self.mgr.grant_skip("p", 1, 2, 2)
        rv = self.mgr.revoke_skip("p", g.proof_id)
        self.assertTrue(rv.revoked)
        self.assertEqual(self.mgr.pump("p").blocked, "gap")
        # filling the gap now delivers normally - no conflict
        self.feed(2)
        res = self.mgr.pump("p")
        self.assertEqual(self.delivered_ids(res), ["dlv:p:1"])

    def test_revoke_after_cross_is_conflict(self):
        self.feed(1, 3)
        self.mgr.pump("p")
        g = self.mgr.grant_skip("p", 1, 2, 2)
        self.mgr.pump("p")
        rv = self.mgr.revoke_skip("p", g.proof_id)
        self.assertFalse(rv.revoked)
        self.assertIsNotNone(rv.conflict_id)
        self.assertEqual(len(self.mgr.open_conflicts("p")), 1)

    def test_late_event_and_epoch_divergence(self):
        self.feed(1, 3)
        self.mgr.pump("p")
        self.mgr.grant_skip("p", 1, 2, 2)
        self.mgr.pump("p")
        # same epoch late arrival
        r = self.mgr.ingest(Event("p", 1, 2, {"v": 2}))
        self.assertEqual(r.kind, "conflict")
        c1 = self.mgr.open_conflicts("p")[0]
        self.assertEqual(c1.kind, "late_event_after_skip")
        self.mgr.resolve_conflict("p", c1.conflict_id, "confirm_skip")
        # newer epoch at the skipped position: divergence, must reinstate
        r = self.mgr.ingest(Event("p", 2, 2, {"v": "new"}))
        self.assertEqual(r.kind, "conflict")
        c2 = self.mgr.open_conflicts("p")[0]
        self.assertEqual(c2.kind, "epoch_divergence_at_gap")
        self.mgr.resolve_conflict("p", c2.conflict_id, "reinstate")
        res = self.mgr.pump("p")
        kinds = [(c.kind, c.payload) for c in res.corrections]
        self.assertIn(("reinstate", {"v": "new"}), kinds)

    def test_duplicate_ingest_is_idempotent(self):
        self.feed(1)
        r = self.mgr.ingest(Event("p", 1, 1, {"v": 1}))
        self.assertEqual(r.kind, "duplicate")
        self.assertEqual(r.duplicate_of, "dlv:p:0")

    def test_duplicate_ack(self):
        self.feed(1)
        self.mgr.pump("p")
        self.assertTrue(self.mgr.ack("dlv:p:0").accepted)
        r = self.mgr.ack("dlv:p:0")
        self.assertTrue(r.duplicate)


class DurabilityTests(DeliveryTestBase):
    def test_crash_before_ack_commit_redelivers_same_id(self):
        self.feed(1, 4, 3)
        self.mgr.pump("p")
        self.mgr.grant_skip("p", 1, 2, 2)
        self.mgr.pump("p")
        self.fc.arm_commit_crash(1)
        with self.assertRaises(CommitCrash):
            self.mgr.ack("dlv:p:2")
        self.mgr.crash()
        self.mgr.recover()
        res = self.mgr.pump("p")
        ids = {i.delivery_id for i in res.redelivered}
        self.assertEqual(ids, {"dlv:p:0", "dlv:p:2", "dlv:p:3"})

    def test_durable_acks_never_redeliver(self):
        self.feed(1, 2, 3)
        self.mgr.pump("p")
        for did in ("dlv:p:0", "dlv:p:1", "dlv:p:2"):
            self.mgr.ack(did)
        self.mgr.crash()
        self.mgr.recover()
        res = self.mgr.pump("p")
        self.assertEqual(res.redelivered, [])

    def test_spill_failure_rejects_and_retries(self):
        cfg = PartConfig("q", modulus=16, capacity_live=8, memory_slots=1,
                         inflight_max=8)
        self.mgr.create_partition(cfg)
        self.mgr.ingest(Event("q", 1, 1, 1))
        self.fc.arm_spill_failure("q", 1)
        r = self.mgr.ingest(Event("q", 1, 2, 2))
        self.assertFalse(r.accepted)
        self.assertTrue(r.spill_failed)
        # retry accepted with stable abs
        r2 = self.mgr.ingest(Event("q", 1, 2, 2))
        self.assertTrue(r2.accepted)
        self.assertEqual(r2.abs_index, 1)

    def test_capacity_hard_cap(self):
        cfg = PartConfig("q", modulus=32, capacity_live=3, memory_slots=2,
                         inflight_max=2)
        self.mgr.create_partition(cfg)
        for seq in (1, 2, 3):
            self.mgr.ingest(Event("q", 1, seq, seq))
        with self.assertRaises(CapacityExceededError):
            self.mgr.ingest(Event("q", 1, 4, 4))


class WraparoundTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.mgr = DeliveryManager(self.root)
        self.mgr.create_partition(
            PartConfig("w", modulus=8, initial_seq=6, initial_abs=5,
                       capacity_live=8, memory_slots=2, inflight_max=8)
        )

    def tearDown(self):
        shutil.rmtree(self.root)

    def test_deliveries_across_wrap(self):
        for seq in (6, 7, 0, 1):
            self.mgr.ingest(Event("w", 1, seq, seq))
        res = self.mgr.pump("w")
        self.assertEqual(
            [i.seq for i in res.delivered], [6, 7, 0, 1]
        )
        self.assertEqual(
            [i.delivery_id for i in res.delivered],
            [f"dlv:w:{a}" for a in range(5, 9)],
        )


class MigrationTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp())
        self.bundle = Path(tempfile.mkdtemp())
        self.mgr = DeliveryManager(self.root, owner="worker-A")
        self.mgr.create_partition(
            PartConfig("m", modulus=8, initial_seq=6, initial_abs=5,
                       capacity_live=8, memory_slots=2, inflight_max=8)
        )

    def tearDown(self):
        shutil.rmtree(self.root)
        shutil.rmtree(self.bundle)

    def test_migration_carries_gap_proof_acks(self):
        for seq in (6, 7, 0):
            self.mgr.ingest(Event("m", 1, seq, seq))
        self.mgr.pump("m")
        self.mgr.ack("dlv:m:5")
        self.mgr.grant_skip("m", 1, 1, 1, proof_id="proof:m:g")
        before = self.mgr.partition_state("m")
        info = self.mgr.migrate_partition("m", self.bundle / "m", "worker-B")
        after = self.mgr.partition_state("m")
        self.assertEqual(after["owner"], "worker-B")
        for key in ("low_abs", "head_abs", "gap_abs", "unacked"):
            self.assertEqual(before[key], after[key], key)
        self.assertIn("proof:m:g", after["proofs"])
        # proof remains revocable on the new owner
        rv = self.mgr.revoke_skip("m", "proof:m:g")
        self.assertTrue(rv.revoked)
        # unacked redelivered with the same ids after migration
        res = self.mgr.pump("m")
        self.assertEqual(
            {i.delivery_id for i in res.redelivered}, {"dlv:m:6", "dlv:m:7"}
        )
        # traffic resumes
        self.mgr.ingest(Event("m", 1, 1, 1))
        res = self.mgr.pump("m")
        self.assertEqual([i.seq for i in res.delivered], [1])


if __name__ == "__main__":
    unittest.main()
