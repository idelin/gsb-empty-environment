"""Runnable sample: migrate key state from partition v1 (4 partitions) to
v2 (8 partitions) while a local fixture feed keeps producing change events.

Run:  python3 demo.py
"""
from __future__ import annotations

import tempfile
from pathlib import Path

from keymigrate import (
    EventLog,
    Migration,
    MigrationCoordinator,
    OwnershipRegistry,
    PartitionFunction,
    Router,
    KeyStateStore,
    load_fixture,
    replay,
)

FIXTURE = Path(__file__).parent / "fixtures" / "changes.json"


def build_system(log_path=None):
    v1 = PartitionFunction(version=1, num_partitions=4)
    v2 = PartitionFunction(version=2, num_partitions=8)
    owners = sorted(set(v1.all_owners()) | set(v2.all_owners()))
    stores = {owner: KeyStateStore(owner) for owner in owners}
    registry = OwnershipRegistry()
    log = EventLog(log_path)
    router = Router(registry, stores, log)
    coordinator = MigrationCoordinator(registry, stores, log)
    return v1, v2, stores, registry, log, router, coordinator


def merged_state(stores):
    merged = {}
    for store in stores.values():
        for key, value in store.state.items():
            assert key not in merged, f"key {key} present in two owners!"
            merged[key] = value
    return merged


def main() -> None:
    events = load_fixture(FIXTURE)
    print(f"loaded {len(events)} change events from {FIXTURE.name}")

    log_path = Path(tempfile.gettempdir()) / "keymigrate_demo.jsonl"
    v1, v2, stores, registry, log, router, coordinator = build_system(log_path)

    # Phase 1: steady state on partition version 1.
    for event in events[:6]:
        router.route(event, v1)
    print(f"phase 1: routed 6 events on v{ v1.version } route")

    # Phase 2: partition function version changes -> begin migration.
    migration = Migration("m-001", old_fn=v1, new_fn=v2)
    moved = coordinator.begin(migration)
    print(f"phase 2: migration m-001 began, {len(moved)} keys moving: {sorted(moved)}")

    # Phase 3: events keep arriving on the OLD route during the switch.
    for event in events[6:9]:
        router.route(event, v1)
    print("phase 3: routed 3 events on the old route during the switch")

    # Phase 4: commit -> inflight old-route events forwarded, ownership flips.
    coordinator.commit(migration.migration_id)
    print("phase 4: migration committed, ownership flipped to v2 owners")

    # Phase 5: new events use the new route.
    for event in events[9:]:
        router.route(event, v2)
    print("phase 5: routed 3 events on v2 route")

    # Duplicate migration request must be a no-op (no state copied twice).
    again = coordinator.begin(Migration("m-001", old_fn=v1, new_fn=v2))
    print(f"duplicate migration m-001 re-submitted -> moved {len(again)} keys (no-op)")

    # Verify: final state equals a plain in-order fold of the fixture feed.
    expected = {}
    for event in events:
        if event.after is None:
            expected.pop(event.key, None)
        else:
            expected[event.key] = event.after
    actual = merged_state(stores)
    assert actual == expected, f"state mismatch:\n{actual}\n!=\n{expected}"
    print("check: merged state equals in-order fold of all 12 events  [OK]")
    counts = {k: sum(1 for s in stores.values() if k in s.state) for k in actual}
    assert all(n == 1 for n in counts.values()), "key owned by more than one store!"
    print("check: every live key has exactly one valid owner         [OK]")

    # Replay recovery: rebuild a fresh system purely from the log.
    registry2 = OwnershipRegistry()
    owners = sorted(set(v1.all_owners()) | set(v2.all_owners()))
    stores2 = {owner: KeyStateStore(owner) for owner in owners}
    replay(EventLog.read(log_path), registry2, stores2, {1: v1, 2: v2})
    assert merged_state(stores2) == actual
    assert {k: (r.owner, r.epoch) for k, r in registry2.records.items()} == {
        k: (r.owner, r.epoch) for k, r in registry.records.items()
    }
    print(f"check: replay of {log_path} rebuilt identical state       [OK]")

    print("\nfinal state per owner:")
    for owner in sorted(stores):
        if stores[owner].state:
            print(f"  {owner}: {stores[owner].state}")
    print("\nALL CHECKS PASSED")


if __name__ == "__main__":
    main()
