import { EVENT_WIRE_BYTES, type ChangeEvent } from "./events.ts";

interface QueuedEvent {
  event: ChangeEvent;
  transmitCount: number;
}

export class PiggybackDisseminator {
  private readonly queue = new Map<string, QueuedEvent>();
  private maxRounds: number;
  private readonly lambda: number;

  constructor(clusterSize: number, lambda = 3) {
    this.lambda = lambda;
    this.maxRounds = PiggybackDisseminator.roundsFor(clusterSize, lambda);
  }

  static roundsFor(clusterSize: number, lambda = 3): number {
    const size = Math.max(2, clusterSize);
    return Math.max(1, Math.ceil(Math.log2(size)) * lambda);
  }

  setClusterSize(size: number): void {
    this.maxRounds = PiggybackDisseminator.roundsFor(size, this.lambda);
  }

  get pendingCount(): number {
    return this.queue.size;
  }

  get roundsLimit(): number {
    return this.maxRounds;
  }

  transmitCountOf(nodeId: string): number {
    return this.queue.get(nodeId)?.transmitCount ?? -1;
  }

  queueEvent(event: ChangeEvent): void {
    const existing = this.queue.get(event.nodeId);
    if (existing) {
      if (existing.event.incarnation > event.incarnation) return;
      if (
        existing.event.incarnation === event.incarnation &&
        existing.event.type === event.type
      ) {
        return;
      }
    }
    this.queue.set(event.nodeId, { event, transmitCount: 0 });
  }

  getPiggyback(budgetBytes: number): ChangeEvent[] {
    const capacity = Math.floor(budgetBytes / EVENT_WIRE_BYTES);
    if (capacity <= 0 || this.queue.size === 0) return [];
    const entries = [...this.queue.values()].sort(
      (a, b) => a.transmitCount - b.transmitCount,
    );
    const picked: ChangeEvent[] = [];
    for (const entry of entries) {
      if (picked.length >= capacity) break;
      picked.push(entry.event);
      entry.transmitCount += 1;
      if (entry.transmitCount >= this.maxRounds) {
        this.queue.delete(entry.event.nodeId);
      }
    }
    return picked;
  }
}
