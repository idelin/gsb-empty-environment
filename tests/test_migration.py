import unittest

from keymigrate.events import ChangeEvent
from keymigrate.migration import Migrator
from keymigrate.partition import PartitionFunction
from keymigrate.replay import build_system, replay
from keymigrate.store import FencingError

PF_V1 = PartitionFunction(version=1, num_partitions=2)
PF_V2 = PartitionFunction(version=2, num_partitions=4)
NUM_STORES = 4


def ev(seq, table="users", pk="u1", after=None, before=None):
    return ChangeEvent(tx_id=f"tx-{seq}", table=table, pk=pk,
                       before=before, after=after, commit_seq=seq)


def all_data(stores):
    merged = {}
    for store in stores.values():
        merged.update(store.data)
    return merged


class MigrationTestCase(unittest.TestCase):
    def setUp(self):
        self.registry, self.stores, self.router = build_system(PF_V1, NUM_STORES)

    def migrate(self, pf=PF_V2, **kwargs):
        return Migrator(self.registry, self.stores, self.router, **kwargs).migrate(pf)

    def test_routing_assigns_owner_from_partition_function(self):
        event = ev(1, after={"name": "Ada"})
        partition = self.router.route(event)
        self.assertEqual(partition, PF_V1.partition_for("users", "u1"))
        owner = self.registry.effective_owner(event.key)
        self.assertEqual((owner.partition, owner.version), (partition, 1))
        self.assertEqual(self.stores[partition].data[event.key], {"name": "Ada"})

    def test_single_effective_owner_fencing(self):
        event = ev(1, after={"name": "Ada"})
        self.router.route(event)
        owner = self.registry.effective_owner(event.key)
        # A stale epoch (old owner generation) must be rejected everywhere.
        for store in self.stores.values():
            with self.assertRaises(FencingError):
                store.apply(ev(2, after={"name": "X"}), owner.epoch + 100)
        # During a transfer the old owner can no longer accept writes.
        self.registry.begin_transfer(event.key, 2, 2, cutover_seq=1)
        with self.assertRaises(FencingError):
            self.stores[owner.partition].apply(ev(2, after={"name": "Y"}), owner.epoch)
        self.registry.rollback_transfer(event.key)

    def test_migration_moves_state_and_ownership(self):
        self.router.route(ev(1, after={"name": "Ada"}))
        key = ("users", "u1")
        old_owner = self.registry.effective_owner(key)
        report = self.migrate()
        self.assertIn(key, report.migrated)
        new_owner = self.registry.effective_owner(key)
        self.assertEqual(new_owner.version, 2)
        self.assertEqual(new_owner.partition, PF_V2.partition_for(*key))
        self.assertGreater(new_owner.epoch, old_owner.epoch)
        if new_owner.partition != old_owner.partition:
            self.assertNotIn(key, self.stores[old_owner.partition].data)
        self.assertEqual(all_data(self.stores)[key], {"name": "Ada"})
        # Exactly one store holds the key.
        holders = [s for s in self.stores.values() if key in s.data]
        self.assertEqual(len(holders), 1)

    def test_pre_switchover_events_not_lost(self):
        for seq in range(1, 5):
            self.router.route(ev(seq, pk=f"u{seq}", after={"seq": seq}))
        before = all_data(self.stores)
        self.migrate()
        self.assertEqual(all_data(self.stores), before)
        self.assertEqual(len(self.router.journal), 4)

    def test_in_flight_event_buffered_then_delivered_to_new_owner(self):
        self.router.route(ev(1, after={"name": "Ada"}))
        key = ("users", "u1")
        in_flight = ev(2, after={"name": "Ada2"}, before={"name": "Ada"})

        def while_transferring(k):
            if k == key:
                # Mid-transfer: router must buffer, not deliver.
                self.assertIsNone(self.router.route(in_flight))

        self.migrate(while_transferring=while_transferring)
        owner = self.registry.effective_owner(key)
        store = self.stores[owner.partition]
        self.assertEqual(store.data[key], {"name": "Ada2"})
        self.assertEqual(store.watermarks[key], 2)
        self.assertEqual(self.router.peek_buffer(key), [])

    def test_duplicate_migration_does_not_duplicate_state(self):
        for seq in range(1, 4):
            self.router.route(ev(seq, pk=f"u{seq}", after={"seq": seq}))
        first = self.migrate()
        snapshot = all_data(self.stores)
        watermarks = {s.partition_id: dict(s.watermarks) for s in self.stores.values()}
        second = self.migrate()
        self.assertEqual(second.migrated, [])
        self.assertEqual(len(second.skipped), 3)
        self.assertEqual(all_data(self.stores), snapshot)
        self.assertEqual(
            watermarks,
            {s.partition_id: dict(s.watermarks) for s in self.stores.values()},
        )
        self.assertEqual(dict(self.router._buffers), {})

    def test_duplicate_event_delivery_is_idempotent(self):
        event = ev(1, after={"name": "Ada"})
        self.router.route(event)
        self.router.route(event)
        self.router.route(event)
        self.assertEqual(len(self.router.journal), 1)
        self.assertEqual(all_data(self.stores), {("users", "u1"): {"name": "Ada"}})

    def test_route_failure_rolls_back_ownership(self):
        self.router.route(ev(1, after={"name": "Ada"}))
        key = ("users", "u1")
        old_owner = self.registry.effective_owner(key)
        in_flight = ev(2, after={"name": "Ada2"})

        def while_transferring(k):
            if k == key:
                self.router.route(in_flight)

        report = self.migrate(fail_on=lambda k: True,
                              while_transferring=while_transferring)
        self.assertTrue(report.aborted)
        self.assertEqual(report.rolled_back, [key])
        # Ownership unchanged: same partition, version and epoch.
        self.assertEqual(self.registry.effective_owner(key), old_owner)
        # Staged copy discarded; old store keeps state and gets the
        # buffered event back.
        old_store = self.stores[old_owner.partition]
        self.assertEqual(old_store.data[key], {"name": "Ada2"})
        self.assertEqual(old_store.watermarks[key], 2)
        new_partition = PF_V2.partition_for(*key)
        if new_partition != old_owner.partition:
            self.assertNotIn(key, self.stores[new_partition].data)
        # System stays on v1; a later retry can succeed.
        report = self.migrate()
        self.assertFalse(report.aborted)
        self.assertEqual(self.registry.effective_owner(key).version, 2)

    def test_delete_event_removes_state(self):
        self.router.route(ev(1, after={"name": "Ada"}))
        self.router.route(ev(2, after=None, before={"name": "Ada"}))
        self.assertEqual(all_data(self.stores), {})
        self.migrate()
        self.assertEqual(all_data(self.stores), {})

    def test_replay_recovers_state(self):
        events = [
            ev(1, pk="u1", after={"name": "Ada"}),
            ev(2, pk="u2", after={"name": "Ben"}),
            ev(3, pk="u1", after={"name": "Ada2"}, before={"name": "Ada"}),
            ev(4, pk="u2", after=None, before={"name": "Ben"}),
        ]
        for event in events:
            self.router.route(event)
        self.migrate()
        self.router.route(ev(5, pk="u3", after={"name": "Cy"}))
        live = all_data(self.stores)

        _, replayed_stores, _ = replay(events + [ev(5, pk="u3", after={"name": "Cy"})],
                                       PF_V2, NUM_STORES)
        self.assertEqual(all_data(replayed_stores), live)
        # Replaying again (recovery restart) changes nothing.
        _, replayed_again, _ = replay(events + [ev(5, pk="u3", after={"name": "Cy"})],
                                      PF_V2, NUM_STORES)
        self.assertEqual(all_data(replayed_again), live)

    def test_replay_from_seq_resumes_without_duplicates(self):
        events = [ev(seq, pk=f"u{seq}", after={"seq": seq}) for seq in range(1, 6)]
        _, stores_a, _ = replay(events, PF_V2, NUM_STORES)
        # Crash-after-3 recovery: replay only the tail onto a system that
        # already applied the prefix.
        _, stores_b, router_b = build_system(PF_V2, NUM_STORES)
        for event in events[:3]:
            router_b.route(event)
        for event in events:
            if event.commit_seq > 3:
                router_b.route(event)
        self.assertEqual(all_data(stores_a), all_data(stores_b))


if __name__ == "__main__":
    unittest.main()
