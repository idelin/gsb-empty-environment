import { ClockMatrix } from "./clockMatrix.ts";
import { TombstoneGcCoordinator } from "./coordinator.ts";
import { CrdtStore } from "./store.ts";
import type { Dot, GcStats, KnowledgeVector } from "./types.ts";

export class ClusterNode<V = unknown> {
  readonly store: CrdtStore<V>;
  private readonly matrix: ClockMatrix;
  private readonly gc: TombstoneGcCoordinator;
  private readonly knowledge = new Map<string, number>();
  private localClock = 0;
  readonly id: string;

  constructor(id: string) {
    this.id = id;
    this.store = new CrdtStore<V>();
    this.matrix = new ClockMatrix();
    this.gc = new TombstoneGcCoordinator(id, this.matrix, this.store);
    this.knowledge.set(id, 0);
    this.publishSelf();
  }

  private publishSelf(): void {
    this.matrix.observe(this.id, this.knowledgeVector());
  }

  tick(): number {
    this.localClock += 1;
    this.knowledge.set(this.id, this.localClock);
    this.publishSelf();
    return this.localClock;
  }

  advanceTo(clock: number): void {
    while (this.localClock < clock) this.tick();
  }

  get clock(): number {
    return this.localClock;
  }

  knowledgeVector(): KnowledgeVector {
    const vector: KnowledgeVector = {};
    for (const [id, clock] of this.knowledge) vector[id] = clock;
    return vector;
  }

  set(key: string, value: V): void {
    this.tick();
    this.store.set(key, value);
  }

  delete(key: string): Dot | null {
    const clock = this.tick();
    const dot: Dot = { nodeId: this.id, clock };
    return this.store.delete(key, dot) ? dot : null;
  }

  receiveGossip(fromNodeId: string, vector: KnowledgeVector): void {
    for (const [id, clock] of Object.entries(vector)) {
      const known = this.knowledge.get(id) ?? 0;
      if (clock > known) this.knowledge.set(id, clock);
    }
    this.matrix.observe(fromNodeId, vector);
    this.publishSelf();
  }

  frontier(): Map<string, number> {
    return this.gc.causalStabilityFrontier();
  }

  runGc(): GcStats {
    return this.gc.collect();
  }
}
