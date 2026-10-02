import { VectorClock, type ClockSnapshot } from "./vectorClock.ts";
import { TombstoneStore } from "./tombstoneStore.ts";
import { TombstoneGcCoordinator } from "./coordinator.ts";

/**
 * A simulated cluster node: local CRDT store + logical clock + GC
 * coordinator. Nodes exchange causal context by gossiping clock snapshots.
 */
export class ClusterNode {
  readonly clock = new VectorClock();
  readonly store: TombstoneStore;
  readonly coordinator: TombstoneGcCoordinator;
  readonly id: string;
  private inTransaction = false;

  constructor(
    id: string,
    cluster: readonly string[],
    tombstonePayloadSize = 256,
  ) {
    this.id = id;
    this.store = new TombstoneStore(tombstonePayloadSize);
    this.coordinator = new TombstoneGcCoordinator(id, cluster, this.store);
    this.coordinator.observe(this.id, this.clock.snapshot());
  }

  /** Local logical time = own vector component. */
  localTime(): number {
    return this.clock.get(this.id);
  }

  put(key: string, value: string): void {
    this.tick();
    this.store.set(key, value);
  }

  delete(key: string): boolean {
    this.tick();
    const removed = this.store.remove(key, this.id, this.clock.snapshot());
    return removed;
  }

  /**
   * Run several ops under a single causal commit: the clock advances once
   * and every tombstone created inside shares the same delete timestamp.
   */
  transact(apply: () => void): void {
    this.clock.tick(this.id);
    this.inTransaction = true;
    try {
      apply();
    } finally {
      this.inTransaction = false;
    }
    this.coordinator.observe(this.id, this.clock.snapshot());
  }

  private tick(): void {
    if (this.inTransaction) return;
    this.clock.tick(this.id);
    this.coordinator.observe(this.id, this.clock.snapshot());
  }

  gossipTo(peer: ClusterNode): void {
    peer.receive(this.id, this.clock.snapshot());
  }

  receive(from: string, snapshot: ClockSnapshot): void {
    this.clock.merge(VectorClock.from(snapshot));
    this.coordinator.observe(from, snapshot);
    // Keep the coordinator's view of our own clock fresh after merging.
    this.coordinator.observe(this.id, this.clock.snapshot());
  }
}

/** Deterministic full-mesh gossip rounds until all clocks quiesce. */
export function gossipUntilStable(
  nodes: readonly ClusterNode[],
  maxRounds = 10,
): number {
  for (let round = 1; round <= maxRounds; round += 1) {
    const before = nodes.map((n) => n.clock.toString()).join("|");
    for (const from of nodes) {
      for (const to of nodes) {
        if (from !== to) from.gossipTo(to);
      }
    }
    const after = nodes.map((n) => n.clock.toString()).join("|");
    if (before === after) return round;
  }
  return maxRounds;
}
