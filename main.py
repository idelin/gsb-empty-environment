"""Demo: partition-function version upgrade with zero-loss key migration.

Scenario:
  v1 (2 partitions) -> route pre-switchover events -> migrate to v2
  (4 partitions) while an in-flight event arrives mid-transfer -> route the
  rest -> re-run the same migration (no-op) -> attempt v3 with an injected
  route failure (ownership rolls back) -> recover by replaying the log.
"""
from __future__ import annotations

from keymigrate.events import load_events
from keymigrate.migration import Migrator
from keymigrate.partition import PartitionFunction
from keymigrate.replay import build_system, replay

FIXTURE = "fixtures/changes.json"
MAX_PARTITIONS = 4
CUTOVER_SEQ = 6  # events 1..6 use the old route; migration happens after them


def merged_state(stores):
    merged = {}
    for partition_id in sorted(stores):
        for key, value in sorted(stores[partition_id].data.items()):
            merged[key] = (partition_id, value)
    return merged


def show_state(title, registry, stores):
    print(f"\n--- {title} ---")
    for key, (partition_id, value) in merged_state(stores).items():
        owner = registry.effective_owner(key)
        print(f"  {key[0]}/{key[1]} -> partition {partition_id} "
              f"(owner v{owner.version} e{owner.epoch}) {value}")


def main() -> None:
    events = load_events(FIXTURE)
    pf_v1 = PartitionFunction(version=1, num_partitions=2)
    pf_v2 = PartitionFunction(version=2, num_partitions=4)
    pf_v3 = PartitionFunction(version=3, num_partitions=3)

    registry, stores, router = build_system(pf_v1, MAX_PARTITIONS)

    print("=== Phase 1: route pre-switchover events on v1 (2 partitions) ===")
    for event in events:
        if event.commit_seq <= CUTOVER_SEQ:
            router.route(event)
    # Duplicate delivery of the same events must not duplicate state.
    for event in events:
        if event.commit_seq <= CUTOVER_SEQ:
            router.route(event)
    print(f"  routed {len(router.journal)} unique events "
          f"(duplicates were dropped by watermark dedupe)")
    show_state("state on v1", registry, stores)

    print("\n=== Phase 2: migrate v1 -> v2; an in-flight event arrives mid-transfer ===")
    in_flight = [e for e in events if e.commit_seq == 7]

    def while_transferring(key):
        for event in in_flight:
            if event.key == key:
                router.route(event)  # buffered until the transfer commits

    migrator = Migrator(registry, stores, router,
                        while_transferring=while_transferring)
    report = migrator.migrate(pf_v2)
    print(f"  migrated={len(report.migrated)} skipped={len(report.skipped)} "
          f"rolled_back={len(report.rolled_back)}")
    for event in events:
        if event.commit_seq >= 7:
            router.route(event)  # seq 7 dedupes if already delivered
    show_state("state after migration to v2 (4 partitions)", registry, stores)

    print("\n=== Phase 3: re-run the same migration (must be a no-op) ===")
    report = Migrator(registry, stores, router).migrate(pf_v2)
    print(f"  migrated={len(report.migrated)} skipped={len(report.skipped)} "
          f"rolled_back={len(report.rolled_back)}")

    print("\n=== Phase 4: migrate v2 -> v3 with injected route failure ===")
    failing = Migrator(registry, stores, router, fail_on=lambda key: True)
    report = failing.migrate(pf_v3)
    print(f"  aborted={report.aborted} rolled_back={report.rolled_back}")
    owners = {key: registry.effective_owner(key) for key in registry.known_keys()}
    print(f"  all owners still on v2: {all(o.version == 2 for o in owners.values())}")
    show_state("state intact after rollback", registry, stores)

    print("\n=== Phase 5: recover by replaying the commit log at v2 ===")
    _, replayed_stores, replayed_router = replay(events, pf_v2, MAX_PARTITIONS)
    live = merged_state(stores)
    recovered = merged_state(replayed_stores)
    print(f"  live keys={len(live)} recovered keys={len(recovered)}")
    print(f"  recovered state matches live state: "
          f"{ {k: v for k, v in live.items()} == {k: v for k, v in recovered.items()} }")
    print(f"  replay journal entries: {len(replayed_router.journal)}")

    print("\n=== Route journal (commit_seq, key, partition, owner_version) ===")
    for entry in router.journal:
        print(f"  seq={entry[0]:>2} {entry[1][0]}/{entry[1][1]} "
              f"-> partition {entry[2]} (v{entry[3]})")


if __name__ == "__main__":
    main()
