import type { CrdtStore } from "./store.ts";
import type { ClockMatrix } from "./clockMatrix.ts";
import type { GcStats } from "./types.ts";

export class TombstoneGcCoordinator {
  private readonly nodeId: string;
  private readonly matrix: ClockMatrix;
  private readonly store: CrdtStore;

  constructor(nodeId: string, matrix: ClockMatrix, store: CrdtStore) {
    this.nodeId = nodeId;
    this.matrix = matrix;
    this.store = store;
  }

  causalStabilityFrontier(): Map<string, number> {
    return this.matrix.frontier();
  }

  isCausallyStable(nodeId: string, clock: number): boolean {
    const frontier = this.causalStabilityFrontier();
    return (frontier.get(nodeId) ?? 0) >= clock;
  }

  collect(): GcStats {
    const frontier = this.causalStabilityFrontier();
    const tombstones = this.store.tombstones();
    let reclaimed = 0;
    let reclaimedBytes = 0;
    for (const entry of tombstones) {
      const dot = entry.deletedAt;
      if (!dot) continue;
      const stable = (frontier.get(dot.nodeId) ?? 0) >= dot.clock;
      if (stable) {
        reclaimedBytes += entry.sizeBytes;
        this.store.purge(entry.key);
        reclaimed += 1;
      }
    }
    const frontierObj: Record<string, number> = {};
    for (const [id, clock] of frontier) frontierObj[id] = clock;
    return {
      frontier: frontierObj,
      scanned: tombstones.length,
      reclaimed,
      reclaimedBytes,
      remainingBytes: this.store.bytes,
    };
  }
}
