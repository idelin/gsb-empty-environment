import type { Message } from './types.ts';

/** Deterministic PRNG (mulberry32) so every fuzz run is reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface Envelope {
  msg: Message;
  deliverAt: number;
}

/**
 * Deterministic in-memory message transport.
 *
 * - Discrete logical clock (`now`); messages are delivered in batches by
 *   `tick()`, one handler at a time, each running to completion. Handlers
 *   never re-enter: `send()` only enqueues. This is the actor-model
 *   serialization that makes every node's state transitions atomic.
 * - Supports partitions, message loss and bounded random delays, all driven
 *   by a seeded PRNG so tests are reproducible.
 * - No timers, sockets or promises: nothing can leak.
 */
export class Network {
  now = 0;
  dropRate = 0;
  minDelay = 1;
  maxDelay = 1;
  /** Optional hook fired after every tick (used for invariant checking). */
  afterTick: (() => void) | null = null;

  private queue: Envelope[] = [];
  private readonly blocked = new Set<string>();
  private readonly handlers = new Map<number, (msg: Message) => void>();
  private readonly rand: () => number;

  constructor(seed = 1) {
    this.rand = mulberry32(seed);
  }

  register(id: number, handler: (msg: Message) => void): void {
    this.handlers.set(id, handler);
  }

  /** Block all traffic between the two groups (both directions). */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) {
      for (const b of groupB) {
        this.blocked.add(`${a}->${b}`);
        this.blocked.add(`${b}->${a}`);
      }
    }
  }

  heal(): void {
    this.blocked.clear();
  }

  send(msg: Message): void {
    if (this.blocked.has(`${msg.from}->${msg.to}`)) return;
    if (this.dropRate > 0 && this.rand() < this.dropRate) return;
    const delay = this.minDelay + Math.floor(this.rand() * (this.maxDelay - this.minDelay + 1));
    this.queue.push({ msg, deliverAt: this.now + delay });
  }

  get pendingCount(): number {
    return this.queue.length;
  }

  /** Deliver the earliest batch of due messages. Returns false when idle. */
  tick(): boolean {
    if (this.queue.length === 0) return false;
    let next = Infinity;
    for (const env of this.queue) next = Math.min(next, env.deliverAt);
    this.now = Math.max(this.now, next);
    const due = this.queue.filter((env) => env.deliverAt <= this.now);
    this.queue = this.queue.filter((env) => env.deliverAt > this.now);
    // Deterministic shuffle so delivery order within a batch is seeded-random.
    for (let i = due.length - 1; i > 0; i--) {
      const j = Math.floor(this.rand() * (i + 1));
      const tmp = due[i];
      due[i] = due[j];
      due[j] = tmp;
    }
    for (const env of due) this.handlers.get(env.msg.to)?.(env.msg);
    this.afterTick?.();
    return true;
  }

  runUntilIdle(maxTicks = 100000): void {
    let ticks = 0;
    while (this.tick()) {
      if (++ticks > maxTicks) throw new Error('network did not quiesce (possible livelock)');
    }
  }

  /** Advance the logical clock (e.g. to expire a leader lease). */
  advance(delta: number): void {
    this.now += delta;
  }
}
