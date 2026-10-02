import { eventKey, type MemberEvent } from './types.ts';

export function retransmitLimit(clusterSize: number, lambda = 3): number {
  return Math.max(1, Math.ceil(lambda * Math.log2(clusterSize + 1)));
}

export class Disseminator {
  private queue: MemberEvent[] = [];
  private counts: Map<string, number> = new Map();
  private readonly limit: number;

  constructor(clusterSize: number, lambda = 3) {
    this.limit = retransmitLimit(clusterSize, lambda);
  }

  get maxRounds(): number {
    return this.limit;
  }

  get pending(): number {
    return this.queue.length;
  }

  add(event: MemberEvent): boolean {
    const key = eventKey(event);
    const index = this.queue.findIndex((e) => eventKey(e) === key);
    if (index >= 0) {
      if (this.queue[index].incarnation >= event.incarnation) return false;
      this.queue[index] = event;
      return true;
    }
    this.queue.push(event);
    this.counts.set(key, 0);
    return true;
  }

  getPiggyback(maxEvents: number): MemberEvent[] {
    this.queue.sort(
      (a, b) => this.counts.get(eventKey(a))! - this.counts.get(eventKey(b))!,
    );
    const picked: MemberEvent[] = [];
    const kept: MemberEvent[] = [];
    for (const event of this.queue) {
      const key = eventKey(event);
      const count = this.counts.get(key)!;
      if (count >= this.limit) {
        this.counts.delete(key);
        continue;
      }
      if (picked.length < maxEvents) {
        picked.push(event);
        this.counts.set(key, count + 1);
      }
      kept.push(event);
    }
    this.queue = kept;
    return picked;
  }
}
