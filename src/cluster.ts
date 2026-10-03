/**
 * Cluster: a deterministic, single-threaded, clock-driven network simulator.
 *
 * - Every event (message delivery or timer) is stamped with (time, seq) and
 *   processed strictly in that order, so a scenario is fully deterministic:
 *   same script => same event trace => same final state.
 * - The logical clock only advances when an event is processed; nodes never
 *   read any wall clock. `wait(d)` schedules a no-op event to move time.
 * - Partitions are modeled as blocked directed links; messages sent across a
 *   blocked link are dropped (counted in `stats.dropped`).
 * - Optional `delayFn` jitters delivery latency (seeded by tests) to explore
 *   adversarial interleavings while staying deterministic.
 */
import { PaxosNode } from './node.ts';
import type { Message } from './types.ts';

interface Event {
  time: number;
  seq: number;
  run: () => void;
}

export class Cluster {
  readonly size: number;
  readonly nodes: PaxosNode[] = [];

  /** Logical clock (ticks). The only time source any node can observe. */
  now = 0;
  /** Optional latency jitter, e.g. a seeded PRNG-driven function. */
  delayFn: (() => number) | null = null;

  readonly stats: Record<string, number> = {
    Prepare: 0,
    Promise: 0,
    Accept: 0,
    Accepted: 0,
    Commit: 0,
    dropped: 0,
  };
  /** Every delivered message, in delivery order (deterministic trace). */
  readonly delivered: Message[] = [];

  private queue: Event[] = [];
  private seq = 0;
  private readonly blockedLinks: Set<string> = new Set();

  constructor(size: number) {
    this.size = size;
    for (let i = 0; i < size; i++) this.nodes.push(new PaxosNode(i, this));
  }

  majority(): number {
    return Math.floor(this.size / 2) + 1;
  }

  peerIds(id: number): number[] {
    const out: number[] = [];
    for (let i = 0; i < this.size; i++) if (i !== id) out.push(i);
    return out;
  }

  leaderIds(): number[] {
    return this.nodes.filter((n) => n.role === 'leader').map((n) => n.id);
  }

  // ------------------------------------------------------------- topology

  private linkKey(from: number, to: number): string {
    return `${from}->${to}`;
  }

  blockLink(a: number, b: number): void {
    this.blockedLinks.add(this.linkKey(a, b));
  }

  /** Cut every link between the two groups (both directions). */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) {
      for (const b of groupB) {
        this.blockLink(a, b);
        this.blockLink(b, a);
      }
    }
  }

  isolate(id: number): void {
    for (const p of this.peerIds(id)) {
      this.blockLink(id, p);
      this.blockLink(p, id);
    }
  }

  heal(): void {
    this.blockedLinks.clear();
  }

  // ------------------------------------------------------------- events

  send(msg: Message, delay?: number): void {
    this.stats[msg.type] += 1;
    if (this.blockedLinks.has(this.linkKey(msg.from, msg.to))) {
      this.stats.dropped += 1;
      return;
    }
    const d = delay ?? (this.delayFn ? this.delayFn() : 1);
    const at = this.now + d;
    this.enqueue(at, () => {
      this.delivered.push(msg);
      this.nodes[msg.to].handle(msg);
    });
  }

  /** Advance the logical clock by scheduling a no-op event `duration` ticks out. */
  wait(duration: number): void {
    this.enqueue(this.now + duration, () => {});
  }

  private enqueue(time: number, run: () => void): void {
    const ev: Event = { time, seq: this.seq++, run };
    let i = this.queue.length;
    while (i > 0) {
      const prev = this.queue[i - 1];
      if (prev.time < ev.time || (prev.time === ev.time && prev.seq < ev.seq)) break;
      i--;
    }
    this.queue.splice(i, 0, ev);
  }

  /** Process the next event. Returns false when the queue is empty. */
  tick(): boolean {
    const ev = this.queue.shift();
    if (!ev) return false;
    if (ev.time > this.now) this.now = ev.time;
    ev.run();
    return true;
  }

  /** Drain the event queue. Throws if a pathological livelock burns the budget. */
  run(maxTicks = 100000): void {
    let n = 0;
    while (this.tick()) {
      if (++n > maxTicks) throw new Error('cluster.run: tick budget exceeded (livelock?)');
    }
  }

  get pendingEvents(): number {
    return this.queue.length;
  }

  // ------------------------------------------------------------- drivers

  elect(nodeId: number): void {
    this.nodes[nodeId].startElection();
  }

  propose(nodeId: number, value: string) {
    return this.nodes[nodeId].propose(value);
  }

  // ------------------------------------------------------------- invariants

  /**
   * Safety: no two nodes may commit different values for the same slot.
   * Throws on violation; call after every tick in adversarial scenarios.
   */
  checkSafety(): void {
    for (let i = 0; i < this.size; i++) {
      for (let j = i + 1; j < this.size; j++) {
        const a = this.nodes[i];
        const b = this.nodes[j];
        const n = Math.min(a.commitIndex, b.commitIndex);
        for (let s = 1; s <= n; s++) {
          const va = a.log.get(s)?.value;
          const vb = b.log.get(s)?.value;
          if (va !== vb) {
            throw new Error(
              `SAFETY VIOLATION: slot ${s} committed as '${va}' on node ${i} but '${vb}' on node ${j}`,
            );
          }
        }
      }
    }
  }
}
