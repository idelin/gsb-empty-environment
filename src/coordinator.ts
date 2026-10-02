import { VectorClock, type ClockSnapshot } from "./vectorClock.ts";
import { TombstoneStore, type PruneResult } from "./tombstoneStore.ts";

export interface GcEvent {
  frontier: ClockSnapshot;
  removed: number;
  bytesFreed: number;
}

/**
 * Tombstone GC coordinator.
 *
 * Tracks the latest vector clock advertised by every node in the cluster
 * (including the local one). The causal stability frontier is the
 * component-wise minimum across all known clocks: any event whose causal
 * time is dominated by the frontier is guaranteed to have been observed by
 * every node, so tombstones at or below the frontier can be physically
 * reclaimed. GC runs automatically whenever a fresh clock observation
 * advances the frontier.
 */
export class TombstoneGcCoordinator {
  private readonly knownClocks = new Map<string, VectorClock>();
  private frontierClock: VectorClock | null = null;
  private readonly listeners: Array<(event: GcEvent) => void> = [];
  private gcPasses = 0;
  private totalRemoved = 0;
  private totalBytesFreed = 0;
  readonly nodeId: string;
  private readonly cluster: readonly string[];
  private readonly store: TombstoneStore;

  constructor(
    nodeId: string,
    cluster: readonly string[],
    store: TombstoneStore,
  ) {
    this.nodeId = nodeId;
    this.cluster = cluster;
    this.store = store;
  }

  onGc(listener: (event: GcEvent) => void): void {
    this.listeners.push(listener);
  }

  /** Record a clock observation (self tick or gossip) and auto-GC. */
  observe(nodeId: string, snapshot: ClockSnapshot): void {
    if (!this.cluster.includes(nodeId)) {
      throw new Error(`unknown node in cluster: ${nodeId}`);
    }
    const incoming = VectorClock.from(snapshot);
    const known = this.knownClocks.get(nodeId);
    if (known) {
      known.merge(incoming);
    } else {
      this.knownClocks.set(nodeId, incoming);
    }
    this.recomputeFrontier();
    this.collect();
  }

  /**
   * Causal stability frontier, or null until every cluster member has
   * advertised at least one clock (no safe watermark exists before that).
   */
  frontier(): VectorClock | null {
    return this.frontierClock;
  }

  /** True when the given causal time is known to be observed cluster-wide. */
  isStable(clock: ClockSnapshot): boolean {
    if (!this.frontierClock) return false;
    return this.frontierClock.dominates(VectorClock.from(clock));
  }

  gcStats(): { passes: number; removed: number; bytesFreed: number } {
    return {
      passes: this.gcPasses,
      removed: this.totalRemoved,
      bytesFreed: this.totalBytesFreed,
    };
  }

  private recomputeFrontier(): void {
    if (this.knownClocks.size < this.cluster.length) {
      this.frontierClock = null;
      return;
    }
    this.frontierClock = VectorClock.entrywiseMin(this.knownClocks.values());
  }

  private collect(): void {
    if (!this.frontierClock) return;
    const frontier = this.frontierClock;
    const result: PruneResult = this.store.prune((tombstone) =>
      frontier.dominates(VectorClock.from(tombstone.clock)),
    );
    if (result.removed === 0) return;
    this.gcPasses += 1;
    this.totalRemoved += result.removed;
    this.totalBytesFreed += result.bytesFreed;
    const event: GcEvent = {
      frontier: frontier.snapshot(),
      removed: result.removed,
      bytesFreed: result.bytesFreed,
    };
    for (const listener of this.listeners) listener(event);
  }
}
