import unittest

from shard_tx import (
    Change,
    Manifest,
    Shard,
    ShardCoordinator,
    SubmitStatus,
    TableStore,
    TxState,
    ManifestConflictError,
    UnknownTransactionError,
)
from shard_tx.fixtures import load_fixture


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


def make_coordinator():
    clock = FakeClock()
    return ShardCoordinator(clock=clock), clock


def shard_for(manifest, table, changes):
    return Shard(
        tx_id=manifest.tx_id,
        table=table,
        changes=tuple(changes),
        manifest_digest=manifest.digest,
    )


def change(manifest, table, pk, after, seq, before=None):
    return Change(tx_id=manifest.tx_id, table=table, primary_key=pk,
                  before=before, after=after, commit_seq=seq)


class PublishCompletenessTest(unittest.TestCase):
    def setUp(self):
        self.coordinator, self.clock = make_coordinator()
        self.manifest = Manifest(tx_id="tx-1", tables=frozenset({"A", "B"}),
                                 ttl_seconds=30)
        self.coordinator.register_manifest(self.manifest)

    def test_full_manifest_publishes_atomically_in_commit_seq_order(self):
        shard_a = shard_for(self.manifest, "A", [
            change(self.manifest, "A", "a-1", {"v": "created"}, seq=1),
            change(self.manifest, "A", "a-1", {"v": "updated"}, seq=3,
                   before={"v": "created"}),
        ])
        shard_b = shard_for(self.manifest, "B", [
            change(self.manifest, "B", "b-1", {"ref": "a-1"}, seq=2),
        ])
        result_a = self.coordinator.submit_shard(shard_a)
        self.assertEqual(result_a.status, SubmitStatus.ACCEPTED)
        self.assertEqual(self.coordinator.store.get("A", "a-1"), None)

        result_b = self.coordinator.submit_shard(shard_b)
        self.assertEqual(result_b.status, SubmitStatus.PUBLISHED)
        self.assertEqual(self.coordinator.state_of("tx-1"), TxState.COMMITTED)
        self.assertEqual(self.coordinator.store.get("A", "a-1"), {"v": "updated"})
        self.assertEqual(self.coordinator.store.get("B", "b-1"), {"ref": "a-1"})

    def test_only_a_arrives_nothing_published(self):
        shard_a = shard_for(self.manifest, "A", [
            change(self.manifest, "A", "a-1", {"v": 1}, seq=1),
        ])
        result = self.coordinator.submit_shard(shard_a)
        self.assertEqual(result.status, SubmitStatus.ACCEPTED)
        self.assertEqual(result.missing, frozenset({"B"}))
        self.assertEqual(self.coordinator.state_of("tx-1"), TxState.COLLECTING)
        self.assertEqual(self.coordinator.store.tables(), {})

    def test_duplicate_shard_does_not_satisfy_missing_b(self):
        shard_a = shard_for(self.manifest, "A", [
            change(self.manifest, "A", "a-1", {"v": 1}, seq=1),
        ])
        shard_a_again = shard_for(self.manifest, "A", [
            change(self.manifest, "A", "a-2", {"v": 2}, seq=2),
        ])
        self.coordinator.submit_shard(shard_a)
        result = self.coordinator.submit_shard(shard_a_again)
        self.assertEqual(result.status, SubmitStatus.DUPLICATE)
        self.assertEqual(result.missing, frozenset({"B"}))
        self.assertEqual(self.coordinator.state_of("tx-1"), TxState.COLLECTING)
        self.assertEqual(self.coordinator.store.tables(), {})

    def test_manifest_digest_conflict_rejected(self):
        forged = Shard(
            tx_id="tx-1", table="A",
            changes=(change(self.manifest, "A", "a-1", {"v": 1}, seq=1),),
            manifest_digest="0" * 64,
        )
        with self.assertRaises(ManifestConflictError):
            self.coordinator.submit_shard(forged)
        self.assertEqual(self.coordinator.store.tables(), {})

    def test_unknown_transaction_rejected(self):
        other = Manifest(tx_id="ghost", tables=frozenset({"A"}), ttl_seconds=1)
        with self.assertRaises(UnknownTransactionError):
            self.coordinator.submit_shard(shard_for(other, "A", []))

    def test_unexpected_table_rejected(self):
        shard_c = shard_for(self.manifest, "C", [
            change(self.manifest, "C", "c-1", {"v": 1}, seq=1),
        ])
        result = self.coordinator.submit_shard(shard_c)
        self.assertEqual(result.status, SubmitStatus.UNEXPECTED_TABLE)
        self.assertEqual(self.coordinator.store.tables(), {})


class TimeoutTest(unittest.TestCase):
    def test_timeout_marks_pending_and_never_partially_commits(self):
        coordinator, clock = make_coordinator()
        manifest = Manifest(tx_id="tx-t", tables=frozenset({"A", "B"}),
                            ttl_seconds=10)
        coordinator.register_manifest(manifest)
        coordinator.submit_shard(shard_for(manifest, "A", [
            change(manifest, "A", "a-1", {"v": 1}, seq=1),
        ]))

        clock.advance(11)
        expired = coordinator.sweep_timeouts()

        self.assertEqual(expired, ["tx-t"])
        self.assertEqual(coordinator.state_of("tx-t"), TxState.PENDING)
        self.assertEqual(coordinator.store.tables(), {})

    def test_late_shard_after_timeout_still_completes_and_publishes(self):
        coordinator, clock = make_coordinator()
        manifest = Manifest(tx_id="tx-late", tables=frozenset({"A", "B"}),
                            ttl_seconds=10)
        coordinator.register_manifest(manifest)
        coordinator.submit_shard(shard_for(manifest, "A", [
            change(manifest, "A", "a-1", {"v": 1}, seq=1),
        ]))
        clock.advance(11)
        coordinator.sweep_timeouts()
        self.assertEqual(coordinator.state_of("tx-late"), TxState.PENDING)

        result = coordinator.submit_shard(shard_for(manifest, "B", [
            change(manifest, "B", "b-1", {"v": 2}, seq=2),
        ]))
        self.assertEqual(result.status, SubmitStatus.PUBLISHED)
        self.assertEqual(coordinator.state_of("tx-late"), TxState.COMMITTED)
        self.assertEqual(coordinator.store.get("A", "a-1"), {"v": 1})
        self.assertEqual(coordinator.store.get("B", "b-1"), {"v": 2})

    def test_complete_before_deadline_not_marked_pending(self):
        coordinator, clock = make_coordinator()
        manifest = Manifest(tx_id="tx-fast", tables=frozenset({"A"}),
                            ttl_seconds=10)
        coordinator.register_manifest(manifest)
        coordinator.submit_shard(shard_for(manifest, "A", []))
        clock.advance(60)
        self.assertEqual(coordinator.sweep_timeouts(), [])
        self.assertEqual(coordinator.state_of("tx-fast"), TxState.COMMITTED)


class AtomicityTest(unittest.TestCase):
    def test_failed_optimistic_check_leaves_store_untouched(self):
        store = TableStore()
        store.apply_all([
            Change(tx_id="seed", table="A", primary_key="a-1",
                   before=None, after={"v": "original"}, commit_seq=1),
        ])
        with self.assertRaises(ValueError):
            store.apply_all([
                Change(tx_id="tx-x", table="A", primary_key="a-1",
                       before={"v": "stale"}, after={"v": "new"}, commit_seq=1),
                Change(tx_id="tx-x", table="B", primary_key="b-1",
                       before=None, after={"v": "other"}, commit_seq=2),
            ])
        self.assertEqual(store.get("A", "a-1"), {"v": "original"})
        self.assertEqual(store.get("B", "b-1"), None)


class FixtureTest(unittest.TestCase):
    def test_fixture_round_trip(self):
        manifest, shards = load_fixture("fixtures/tx_1001.json")
        self.assertEqual(manifest.tx_id, "tx-1001")
        self.assertEqual(manifest.tables, frozenset({"A", "B"}))
        self.assertEqual({s.table for s in shards}, {"A", "B"})
        for shard in shards:
            self.assertEqual(shard.manifest_digest, manifest.digest)

        coordinator, _ = make_coordinator()
        coordinator.register_manifest(manifest)
        results = [coordinator.submit_shard(s).status for s in shards]
        self.assertEqual(results, [SubmitStatus.ACCEPTED, SubmitStatus.PUBLISHED])
        self.assertEqual(
            coordinator.store.get("A", "a-1"),
            {"id": "a-1", "amount": 100, "status": "confirmed"},
        )
        self.assertEqual(
            coordinator.store.get("B", "b-1"),
            {"id": "b-1", "ref": "a-1", "qty": 2},
        )


if __name__ == "__main__":
    unittest.main()
