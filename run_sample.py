"""End-to-end sample: fixture-driven shards, atomic publish, timeout, conflicts."""

from shard_tx import (
    ManifestConflictError,
    Shard,
    ShardCoordinator,
    SubmitStatus,
    TxState,
)
from shard_tx.fixtures import load_fixture


class FakeClock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


def show_store(coordinator, label):
    print(f"  store after {label}: {coordinator.store.tables() or '(empty)'}")


def main():
    clock = FakeClock()
    coordinator = ShardCoordinator(clock=clock)

    print("== Scenario 1: manifest {A,B}, shards arrive and publish together ==")
    manifest, shards = load_fixture("fixtures/tx_1001.json")
    digest = coordinator.register_manifest(manifest)
    print(f"  registered {manifest.tx_id}, digest={digest[:12]}..., "
          f"expecting {sorted(manifest.tables)}")
    shard_a, shard_b = shards

    result = coordinator.submit_shard(shard_a)
    print(f"  shard A -> {result.status.value}, missing={sorted(result.missing)}")
    show_store(coordinator, "A only")

    duplicate = Shard(tx_id=manifest.tx_id, table="A", changes=shard_a.changes,
                      manifest_digest=manifest.digest)
    result = coordinator.submit_shard(duplicate)
    print(f"  duplicate shard A -> {result.status.value}, "
          f"missing={sorted(result.missing)} (duplicate does not satisfy B)")
    show_store(coordinator, "duplicate A")

    result = coordinator.submit_shard(shard_b)
    print(f"  shard B -> {result.status.value}, state={coordinator.state_of(manifest.tx_id).value}")
    show_store(coordinator, "A+B complete")

    print("\n== Scenario 2: only A arrives before the deadline ==")
    manifest2, shards2 = load_fixture("fixtures/tx_1002_only_a.json")
    coordinator.register_manifest(manifest2)
    result = coordinator.submit_shard(shards2[0])
    print(f"  shard A -> {result.status.value}, missing={sorted(result.missing)}")
    clock.advance(6)  # ttl_seconds is 5
    expired = coordinator.sweep_timeouts()
    print(f"  after timeout sweep: expired={expired}, "
          f"state={coordinator.state_of(manifest2.tx_id).value}")
    assert coordinator.state_of(manifest2.tx_id) is TxState.PENDING
    print("  tx-1002 marked PENDING only; its shard A was never committed:")
    print(f"  store row a-9: {coordinator.store.get('A', 'a-9')}")

    print("\n== Scenario 3: manifest digest conflict is rejected ==")
    forged = Shard(tx_id=manifest.tx_id, table="A", changes=(),
                   manifest_digest="f" * 64)
    try:
        coordinator.submit_shard(forged)
    except ManifestConflictError as exc:
        print(f"  rejected: {exc}")

    print("\nDone.")


if __name__ == "__main__":
    main()
