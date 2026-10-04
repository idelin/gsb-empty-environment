/**
 * SimNet — deterministic in-memory network with a virtual clock.
 *
 * Supports point-to-point link cuts and arbitrary partitions, fixed
 * per-message latency, and message-drop accounting. Message envelopes are
 * drawn from a shared ObjectPool and released back immediately after
 * delivery, keeping the consensus hot path allocation-free.
 */

import { ObjectPool } from './pool.ts';

export type MsgType = 'prepare' | 'promise' | 'accept' | 'accepted' | 'commit';

/** Pooled message envelope. `entries` is only used on the promise path. */
export interface Msg {
  type: MsgType;
  from: number;
  to: number;
  ballot: number;
  slot: number;
  value: string;
  ok: boolean;
  firstUncommitted: number;
  entries: { slot: number; ballot: number; value: string; committed: boolean }[] | null;
}

export function resetMsg(m: Msg): void {
  m.type = 'prepare';
  m.from = -1;
  m.to = -1;
  m.ballot = 0;
  m.slot = -1;
  m.value = '';
  m.ok = false;
  m.firstUncommitted = 0;
  m.entries = null;
}

export function makeMsg(): Msg {
  return {
    type: 'prepare',
    from: -1,
    to: -1,
    ballot: 0,
    slot: -1,
    value: '',
    ok: false,
    firstUncommitted: 0,
    entries: null,
  };
}

export interface MsgHandler {
  handle(msg: Msg): void;
}

interface InFlight {
  msg: Msg;
  deliverAt: number;
  seq: number;
}

export class SimNet {
  now = 0;
  latency = 1;
  dropped = 0;
  delivered = 0;

  private readonly handlers: (MsgHandler | null)[] = [];
  private queue: InFlight[] = [];
  private seqCounter = 0;
  private readonly blocked = new Set<number>();
  readonly msgPool = new ObjectPool<Msg>(makeMsg, resetMsg, 4096, 256);

  private static pairKey(a: number, b: number): number {
    return a < b ? a * 1000 + b : b * 1000 + a;
  }

  register(id: number, handler: MsgHandler): void {
    this.handlers[id] = handler;
  }

  /** Cut the bidirectional link between nodes a and b. */
  cut(a: number, b: number): void {
    this.blocked.add(SimNet.pairKey(a, b));
  }

  heal(a: number, b: number): void {
    this.blocked.delete(SimNet.pairKey(a, b));
  }

  /** Split the cluster: every node in groupA is cut from every node in groupB. */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) for (const b of groupB) this.cut(a, b);
  }

  healAll(): void {
    this.blocked.clear();
  }

  isBlocked(a: number, b: number): boolean {
    return this.blocked.has(SimNet.pairKey(a, b));
  }

  /** Enqueue a message; dropped silently if the link is cut. */
  send(msg: Msg): void {
    if (msg.from !== msg.to && this.isBlocked(msg.from, msg.to)) {
      this.dropped++;
      this.msgPool.release(msg);
      return;
    }
    this.queue.push({ msg, deliverAt: this.now + this.latency, seq: this.seqCounter++ });
  }

  /** Deliver every currently-queued message in (time, seq) order. */
  flush(): void {
    while (this.queue.length > 0) {
      let best = 0;
      for (let i = 1; i < this.queue.length; i++) {
        const q = this.queue[i];
        const b = this.queue[best];
        if (q.deliverAt < b.deliverAt || (q.deliverAt === b.deliverAt && q.seq < b.seq)) best = i;
      }
      const { msg } = this.queue.splice(best, 1)[0];
      if (msg.deliverAt > this.now) this.now = msg.deliverAt;
      const handler = this.handlers[msg.to];
      this.delivered++;
      if (handler !== null && handler !== undefined) handler.handle(msg);
      this.msgPool.release(msg);
    }
  }

  /** Advance virtual time without delivering anything new. */
  advance(ms: number): void {
    this.now += ms;
  }

  get pending(): number {
    return this.queue.length;
  }
}
