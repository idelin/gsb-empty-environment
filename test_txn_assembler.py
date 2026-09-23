import unittest

from txn_assembler import (
    ChangeEvent,
    Manifest,
    RejectReason,
    TransactionAssembler,
    TxnState,
    apply_to_store,
    manifest_digest,
)

DIGEST_AB = manifest_digest(["A", "B"])


def make_event(txn_id="txn-1", table="A", pk="k1", before=None, after=None,
               seq=1, digest=DIGEST_AB):
    return ChangeEvent(
        txn_id=txn_id,
        table=table,
        primary_key=pk,
        before=before,
        after=after,
        commit_seq=seq,
        manifest_digest=digest,
    )


def make_assembler(required=("A", "B"), deadline=100.0, txn_id="txn-1"):
    return TransactionAssembler(
        [Manifest(txn_id=txn_id, required_tables=tuple(required), deadline=deadline)]
    )


class CompletenessTests(unittest.TestCase):
    def test_publish_when_manifest_complete(self):
        assembler = make_assembler()
        self.assertIs(assembler.receive(make_event(table="A", seq=2)), TxnState.COLLECTING)
        self.assertIs(assembler.receive(make_event(table="B", seq=1)), TxnState.PUBLISHED)
        self.assertEqual(len(assembler.published), 1)

    def test_incomplete_manifest_not_published(self):
        assembler = make_assembler()
        assembler.receive(make_event(table="A"))
        self.assertIs(assembler.state_of("txn-1"), TxnState.COLLECTING)
        self.assertEqual(assembler.published, [])

    def test_duplicate_shard_does_not_satisfy_missing_table(self):
        assembler = make_assembler()
        assembler.receive(make_event(table="A", seq=1))
        state = assembler.receive(make_event(table="A", seq=2))
        self.assertIs(state, TxnState.COLLECTING)
        self.assertEqual(assembler.buffered_tables("txn-1"), {"A"})
        self.assertEqual(assembler.published, [])

    def test_published_in_commit_seq_order(self):
        assembler = make_assembler(required=("A", "B", "C"))
        digest = manifest_digest(["A", "B", "C"])
        assembler.receive(make_event(table="C", seq=30, digest=digest))
        assembler.receive(make_event(table="A", seq=10, digest=digest))
        assembler.receive(make_event(table="B", seq=20, digest=digest))
        seqs = [e.commit_seq for e in assembler.published[0]]
        self.assertEqual(seqs, [10, 20, 30])


class DigestConflictTests(unittest.TestCase):
    def test_conflicting_digest_rejects_transaction(self):
        assembler = make_assembler()
        assembler.receive(make_event(table="A", digest=DIGEST_AB))
        state = assembler.receive(make_event(table="B", digest="deadbeefdeadbeef"))
        self.assertIs(state, TxnState.REJECTED)
        self.assertIs(
            assembler.reject_reason_of("txn-1"), RejectReason.DIGEST_CONFLICT
        )
        self.assertEqual(assembler.published, [])

    def test_rejected_transaction_ignores_late_shards(self):
        assembler = make_assembler()
        assembler.receive(make_event(table="A", digest="deadbeefdeadbeef"))
        state = assembler.receive(make_event(table="B", digest=DIGEST_AB))
        self.assertIs(state, TxnState.REJECTED)
        self.assertEqual(assembler.published, [])

    def test_table_outside_manifest_rejected(self):
        assembler = make_assembler()
        state = assembler.receive(make_event(table="Z"))
        self.assertIs(state, TxnState.REJECTED)
        self.assertIs(
            assembler.reject_reason_of("txn-1"), RejectReason.TABLE_NOT_IN_MANIFEST
        )


class TimeoutTests(unittest.TestCase):
    def test_timeout_marks_pending_without_partial_commit(self):
        assembler = make_assembler(deadline=100.0)
        assembler.receive(make_event(table="A", after={"qty": 5}))
        marked = assembler.expire_timeouts(now=100.0)
        self.assertEqual(marked, ["txn-1"])
        self.assertIs(assembler.state_of("txn-1"), TxnState.PENDING_TIMEOUT)
        self.assertEqual(assembler.published, [])

    def test_no_timeout_before_deadline(self):
        assembler = make_assembler(deadline=100.0)
        assembler.receive(make_event(table="A"))
        self.assertEqual(assembler.expire_timeouts(now=99.9), [])
        self.assertIs(assembler.state_of("txn-1"), TxnState.COLLECTING)

    def test_published_transaction_not_affected_by_timeout(self):
        assembler = make_assembler(deadline=100.0)
        assembler.receive(make_event(table="A", seq=1))
        assembler.receive(make_event(table="B", seq=2))
        self.assertEqual(assembler.expire_timeouts(now=1000.0), [])
        self.assertIs(assembler.state_of("txn-1"), TxnState.PUBLISHED)

    def test_timed_out_transaction_never_publishes(self):
        assembler = make_assembler(deadline=100.0)
        assembler.receive(make_event(table="A"))
        assembler.expire_timeouts(now=100.0)
        state = assembler.receive(make_event(table="B"))
        self.assertIs(state, TxnState.PENDING_TIMEOUT)
        self.assertEqual(assembler.published, [])


class StoreTests(unittest.TestCase):
    def test_apply_to_store_upsert_and_delete(self):
        store: dict[str, dict[str, object]] = {}
        apply_to_store(store, [make_event(table="A", pk="k1", after={"v": 1})])
        apply_to_store(store, [make_event(table="A", pk="k1", before={"v": 1}, after=None)])
        self.assertEqual(store, {"A": {}})


if __name__ == "__main__":
    unittest.main()
