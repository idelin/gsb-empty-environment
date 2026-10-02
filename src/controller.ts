/**
 * Dual-ring hot-standby routing controller.
 *
 * - The ACTIVE ring serves 100% of traffic.
 * - The STANDBY ring receives topology changes in the background and
 *   pre-computes the key-migration impact (prewarm) before going live.
 * - Switchover is an atomic pointer flip: `route()` always reads a single
 *   fully-built ring reference, so swapping the reference is a wait-free,
 *   sub-millisecond, globally consistent cutover with zero transient
 *   misrouting.
 */

import { HashRing, type RingNode } from "./hash-ring.ts";

export type TopologyEvent =
  | { type: "add"; node: RingNode }
  | { type: "remove"; nodeId: string };

export interface MigrationPlan {
  /** Vnode ranges whose owner changes after switchover. */
  movedVnodes: number;
  totalVnodes: number;
  vnodeMoveRatio: number;
  /** Exact key movement measured on the provided traffic sample. */
  sampledKeys: number;
  movedKeys: number;
  keyMoveRatio: number;
  addedNodes: string[];
  removedNodes: string[];
  warmedAt: number;
}

export interface FlipResult {
  durationMs: number;
  fromVersion: number;
  toVersion: number;
  flipCount: number;
}

export class DualRingController {
  #active: HashRing;
  #standby: HashRing;
  #warmed = false;
  #plan: MigrationPlan | null = null;
  #flipCount = 0;
  #lastFlipDurationMs: number | null = null;

  constructor(nodes: RingNode[]) {
    if (nodes.length === 0) throw new Error("controller needs at least one node");
    this.#active = new HashRing(nodes);
    this.#standby = this.#active.clone();
    this.#warmed = true;
  }

  /** Hot path: read the current ring reference and route. Always consistent. */
  route(key: string): string {
    return this.#active.lookup(key);
  }

  routeDistinct(key: string, count: number): string[] {
    return this.#active.lookupDistinct(key, count);
  }

  get activeVersion(): number {
    return this.#active.version;
  }

  get standbyVersion(): number {
    return this.#standby.version;
  }

  get standbyReady(): boolean {
    return this.#warmed;
  }

  get migrationPlan(): MigrationPlan | null {
    return this.#plan;
  }

  get flipCount(): number {
    return this.#flipCount;
  }

  get lastFlipDurationMs(): number | null {
    return this.#lastFlipDurationMs;
  }

  activeNodeIds(): string[] {
    return this.#active.nodeIds();
  }

  /**
   * Apply topology changes to the STANDBY ring only. The active ring keeps
   * serving traffic untouched; the standby is marked dirty until prewarmed.
   */
  syncStandby(events: TopologyEvent[]): void {
    for (const event of events) {
      if (event.type === "add") this.#standby.addNode(event.node);
      else this.#standby.removeNode(event.nodeId);
    }
    if (this.#standby.size === 0) {
      throw new Error("topology events would empty the standby ring");
    }
    this.#warmed = false;
    this.#plan = null;
  }

  /**
   * Prewarm: diff standby vs active at vnode granularity and, given a sample
   * of live keys, compute the exact set of keys that will migrate on flip.
   */
  prewarm(sampleKeys: string[] = []): MigrationPlan {
    const changes = this.#active.diffOwnership(this.#standby);
    const activeIds = new Set(this.#active.nodeIds());
    const standbyIds = new Set(this.#standby.nodeIds());

    let movedKeys = 0;
    for (const key of sampleKeys) {
      if (this.#active.lookup(key) !== this.#standby.lookup(key)) movedKeys++;
    }

    this.#plan = {
      movedVnodes: changes.length,
      totalVnodes: this.#active.vnodeCount,
      vnodeMoveRatio: changes.length / this.#active.vnodeCount,
      sampledKeys: sampleKeys.length,
      movedKeys,
      keyMoveRatio: sampleKeys.length === 0 ? 0 : movedKeys / sampleKeys.length,
      addedNodes: [...standbyIds].filter((id) => !activeIds.has(id)),
      removedNodes: [...activeIds].filter((id) => !standbyIds.has(id)),
      warmedAt: Date.now(),
    };
    this.#warmed = true;
    return this.#plan;
  }

  /**
   * Atomic pointer flip: the warmed standby becomes active in a single
   * reference swap. The old active demotes to standby and must be
   * re-synced + prewarmed before the next flip.
   */
  flip(): FlipResult {
    if (!this.#warmed) {
      throw new Error("standby ring is not warmed; refusing to flip");
    }
    const fromVersion = this.#active.version;
    const start = performance.now();
    const previous = this.#active;
    this.#active = this.#standby;
    this.#standby = previous;
    const durationMs = performance.now() - start;

    this.#flipCount++;
    this.#lastFlipDurationMs = durationMs;
    this.#warmed = false;
    this.#plan = null;
    return {
      durationMs,
      fromVersion,
      toVersion: this.#active.version,
      flipCount: this.#flipCount,
    };
  }

  /** One-click global failover when the active ring is deemed unhealthy. */
  failover(): FlipResult {
    return this.flip();
  }
}
