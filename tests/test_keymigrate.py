import tempfile
import unittest
from pathlib import Path

from keymigrate import (
    ChangeEvent,
    EventLog,
    KeyStateStore,
    Migration,
    MigrationCoordinator,
    OwnershipRegistry,
    PartitionFunction,
    Router,
    RoutingError,
    StaleOwnerError,
    replay,
)

V1 = PartitionFunction(version=1, num_partitions=4)
V2 = PartitionFunction(version=2, num_partitions=8)
OWNERS = sorted(set(V1.all_owners()) | set(V2.all_owners()))


def ev(txid, pk, lsn, after=None, table="t"):
    if after is None:
        after = {"v": lsn}
    return ChangeEvent(txid=txid, table=table, pk=pk, before=None, after=after, lsn=lsn)


def moving_key():
    """Find a fixture key whose owner differs between v1 and v2."""
    for i in range(1000):
        key = ("t", f"k{i}")
        if V1.owner_for(key) != V2.owner_for(key):
            return key
    raise AssertionError("no moving key found")


def stable_key():
    for i in range(1000):
        key = ("t", f"k{i}")
        if V1.owner_for(key) == V2.owner_for(key):
            return key
    raise AssertionError("no stable key found")


def make_system(log_path=None):
    stores = {o: KeyStateStore(o) for o in OWNERS}
    registry = OwnershipRegistry()
    log = EventLog(log_path)
    router = Router(registry, stores, log)
    coordinator = MigrationCoordinator(registry, stores, log)
    return stores, registry, log, router, coordinator


class PartitionFunctionTest(unittest.TestCase):
    def test_deterministic_and_versioned(self):
        key = ("users", "u1")
        self.assertEqual(V1.owner_for(key), V1.owner_for(key))
        owners_v1 = {V1.owner_for(("t", f"k{i}")) for i in range(200)}
        owners_v2 = {V2.owner_for(("t", f"k{i}")) for i in range(200)}
        self.assertTrue(owners_v1 <= set(V1.all_owners()))
        self.assertTrue(owners_v2 <= set(V2.all_owners()))
        self.assertTrue(owners_v1 != owners_v2 or V1.num_partitions != V2.num_partitions)


class StoreTest(unittest.TestCase):
    def test_idempotent_apply_drops_duplicates(self):
        store = KeyStateStore("P0")
        e = ev("tx1", "a", 1, after={"n": 1})
        self.assertTrue(store.apply(e, epoch=0))
        self.assertFalse(store.apply(e, epoch=0))  # same txid+lsn again
        self.assertFalse(store.apply(ev("tx0", "a", 1), epoch=0))  # stale lsn
        self.assertEqual(store.state[("t", "a")], {"n": 1})

    def test_out_of_order_and_delete(self):
        store = KeyStateStore("P0")
        store.apply(ev("tx2", "a", 2, after={"n": 2}), epoch=0)
        self.assertFalse(store.apply(ev("tx1", "a", 1, after={"n": 1}), epoch=0))
        self.assertEqual(store.state[("t", "a")], {"n": 2})
        store.apply(ChangeEvent("tx3", "t", "a", {"n": 2}, None, 3), epoch=0)
        self.assertNotIn(("t", "a"), store.state)

    def test_fencing_rejects_stale_epoch(self):
        store = KeyStateStore("P0")
        store.apply(ev("tx1", "a", 1), epoch=3)
        with self.assertRaises(StaleOwnerError):
            store.apply(ev("tx2", "a", 2), epoch=2)


class OwnershipTest(unittest.TestCase):
    def test_single_valid_owner_across_commit_and_rollback(self):
        reg = OwnershipRegistry()
        key = ("t", "a")
        reg.ensure_key(key, "P0", version=1)
        reg.begin_transfer("m1", key, "P1", new_version=2)
        # pending transfer does not change the effective owner
        self.assertEqual(reg.owner_of(key), ("P0", 0))
        reg.rollback_transfer("m1", key)
        self.assertEqual(reg.owner_of(key), ("P0", 0))
        reg.begin_transfer("m2", key, "P1", new_version=2)
        reg.commit_transfer("m2", key)
        self.assertEqual(reg.owner_of(key), ("P1", 1))
        self.assertEqual(len(reg.records), 1)  # exactly one owner record


class MigrationTest(unittest.TestCase):
    def test_old_route_events_during_switch_are_not_lost(self):
        stores, registry, _, router, coord = make_system()
        key = moving_key()
        pk = key[1]
        router.route(ev("tx1", pk, 1, after={"n": 1}), V1)
        old_owner = V1.owner_for(key)
        new_owner = V2.owner_for(key)

        coord.begin(Migration("m1", V1, V2))
        # events issued on the OLD route while the switch is in progress
        router.route(ev("tx2", pk, 2, after={"n": 2}), V1)
        router.route(ev("tx3", pk, 3, after={"n": 3}), V1)
        coord.commit("m1")

        self.assertEqual(registry.owner_of(key), (new_owner, 1))
        self.assertEqual(stores[new_owner].state[key], {"n": 3})
        self.assertNotIn(key, stores[old_owner].state)
        # post-switch events go straight to the new owner
        router.route(ev("tx4", pk, 4, after={"n": 4}), V2)
        self.assertEqual(stores[new_owner].state[key], {"n": 4})

    def test_duplicate_migration_does_not_copy_state_twice(self):
        stores, registry, _, router, coord = make_system()
        key = moving_key()
        router.route(ev("tx1", key[1], 1), V1)
        migration = Migration("m-dup", V1, V2)
        moved = coord.begin(migration)
        coord.commit("m-dup")
        snapshot = {o: dict(s.state) for o, s in stores.items()}

        again = coord.begin(Migration("m-dup", V1, V2))  # same id: no-op
        self.assertEqual(again, [])
        self.assertTrue(moved)
        self.assertEqual(snapshot, {o: dict(s.state) for o, s in stores.items()})
        self.assertIn("m-dup", registry.completed_migrations)

    def test_stable_keys_are_not_migrated(self):
        stores, registry, _, router, coord = make_system()
        key = stable_key()
        router.route(ev("tx1", key[1], 1), V1)
        moved = coord.begin(Migration("m2", V1, V2))
        self.assertNotIn(key, moved)
        self.assertEqual(registry.owner_of(key), (V1.owner_for(key), 0))

    def test_routing_failure_rolls_back_ownership(self):
        class FailingStore(KeyStateStore):
            def apply(self, event, epoch):
                raise RuntimeError("simulated routing failure")

        stores, registry, _, router, coord = make_system()
        key = moving_key()
        pk = key[1]
        router.route(ev("tx1", pk, 1, after={"n": 1}), V1)
        old_owner = V1.owner_for(key)
        new_owner = V2.owner_for(key)

        coord.begin(Migration("m3", V1, V2))
        router.route(ev("tx2", pk, 2, after={"n": 2}), V1)  # inflight old-route event
        stores[new_owner] = FailingStore(new_owner)  # new owner cannot accept
        coord.stores = stores

        with self.assertRaises(RoutingError):
            coord.commit("m3")

        # ownership rolled back: old owner is still the only valid owner
        self.assertEqual(registry.owner_of(key), (old_owner, 0))
        self.assertEqual(registry.pending, {})
        # nothing lost at the old owner; nothing left at the new owner
        self.assertEqual(stores[old_owner].state[key], {"n": 2})
        self.assertNotIn(key, stores[new_owner].state)
        self.assertNotIn("m3", registry.completed_migrations)
        # system keeps accepting events on the old route
        router.route(ev("tx3", pk, 3, after={"n": 3}), V1)
        self.assertEqual(stores[old_owner].state[key], {"n": 3})


class ReplayTest(unittest.TestCase):
    def test_replay_recovers_identical_state(self):
        with tempfile.TemporaryDirectory() as tmp:
            log_path = Path(tmp) / "log.jsonl"
            stores, registry, log, router, coord = make_system(log_path)
            keys = [moving_key(), stable_key(), ("t", moving_key()[1] + "x")]

            lsn = 0
            for key in keys:
                lsn += 1
                router.route(ev(f"tx{lsn}", key[1], lsn, after={"n": lsn}), V1)
            coord.begin(Migration("m1", V1, V2))
            for key in keys:  # old-route events during the switch
                lsn += 1
                router.route(ev(f"tx{lsn}", key[1], lsn, after={"n": lsn}), V1)
            coord.commit("m1")
            for key in keys:  # new-route events after the switch
                lsn += 1
                router.route(ev(f"tx{lsn}", key[1], lsn, after={"n": lsn}), V2)

            registry2 = OwnershipRegistry()
            stores2 = {o: KeyStateStore(o) for o in OWNERS}
            replay(EventLog.read(log_path), registry2, stores2, {1: V1, 2: V2})

            self.assertEqual(
                {o: s.state for o, s in stores.items()},
                {o: s.state for o, s in stores2.items()},
            )
            self.assertEqual(
                {k: (r.owner, r.epoch, r.version) for k, r in registry.records.items()},
                {k: (r.owner, r.epoch, r.version) for k, r in registry2.records.items()},
            )
            self.assertEqual(registry.completed_migrations, registry2.completed_migrations)


if __name__ == "__main__":
    unittest.main()
