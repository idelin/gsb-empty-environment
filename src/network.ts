/**
 * SimulatedNetwork — deterministic in-memory transport for the Paxos cluster.
 *
 * Messages are enqueued and delivered only on flush(), giving tests full
 * control over interleaving. Network partitions are modeled as blocked
 * directed pairs; messages across a cut are dropped (counted, not lost
 * silently).
 */

export interface Message {
  type: string;
  from: number;
  to: number;
  payload: unknown;
}

type Handler = (msg: Message) => void;

export class SimulatedNetwork {
  private readonly queue: Message[] = [];
  private readonly handlers = new Map<number, Handler>();
  private readonly blocked = new Set<string>();

  delivered = 0;
  dropped = 0;

  register(id: number, handler: Handler): void {
    this.handlers.set(id, handler);
  }

  private static pairKey(a: number, b: number): string {
    return a < b ? `${a}-${b}` : `${b}-${a}`;
  }

  /** Sever all links between the two groups (both directions). */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) {
      for (const b of groupB) this.blocked.add(SimulatedNetwork.pairKey(a, b));
    }
  }

  /** Restore all links. */
  healAll(): void {
    this.blocked.clear();
  }

  isBlocked(a: number, b: number): boolean {
    return this.blocked.has(SimulatedNetwork.pairKey(a, b));
  }

  /** Enqueue a message; dropped immediately if the link is partitioned. */
  send(msg: Message): void {
    if (msg.from !== msg.to && this.isBlocked(msg.from, msg.to)) {
      this.dropped++;
      return;
    }
    this.queue.push(msg);
  }

  broadcast(type: string, from: number, peers: number[], payload: unknown): void {
    for (const to of peers) this.send({ type, from, to, payload });
  }

  /**
   * Deliver messages until the queue is empty (responses enqueue further
   * messages; the loop converges because Paxos exchanges are finite per
   * round). Returns the number of messages delivered in this flush.
   */
  flush(maxRounds = 10_000): number {
    let count = 0;
    while (this.queue.length > 0) {
      if (++count > maxRounds) throw new Error("network flush did not converge");
      const msg = this.queue.shift() as Message;
      const handler = this.handlers.get(msg.to);
      if (handler === undefined) throw new Error(`no handler for node ${msg.to}`);
      handler(msg);
      this.delivered++;
    }
    return count;
  }

  /** Deliver at most n messages (models partial replication before a cut). */
  flushN(n: number): number {
    let count = 0;
    while (this.queue.length > 0 && count < n) {
      const msg = this.queue.shift() as Message;
      const handler = this.handlers.get(msg.to);
      if (handler === undefined) throw new Error(`no handler for node ${msg.to}`);
      handler(msg);
      this.delivered++;
      count++;
    }
    return count;
  }

  /** Discard all in-flight messages (models packets lost to the partition). */
  dropPending(): number {
    const n = this.queue.length;
    this.queue.length = 0;
    this.dropped += n;
    return n;
  }

  get pending(): number {
    return this.queue.length;
  }
}
