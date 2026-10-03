import type { Message, NodeId } from './types.ts';

interface Envelope {
  readonly deliverAt: number;
  readonly seq: number;
  readonly msg: Message;
}

/**
 * Deterministic point-to-point network.
 *
 *  - Fixed 1-tick latency; delivery order is fully determined by
 *    (deliverAt, seq), so identical runs produce identical traces.
 *  - Links can be cut to simulate network partitions; messages crossing a
 *    cut link are silently dropped at delivery time.
 *  - An optional drop hook lets tests suppress individual messages
 *    (used to manufacture acceptor-log holes).
 */
export class Network {
  private queue: Envelope[] = [];
  private seq = 0;
  private readonly cutLinks = new Set<string>();
  private dropHook: ((msg: Message) => boolean) | null = null;

  private static key(a: NodeId, b: NodeId): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  send(now: number, msg: Message): void {
    this.queue.push({ deliverAt: now + 1, seq: this.seq++, msg });
  }

  cut(a: NodeId, b: NodeId): void {
    this.cutLinks.add(Network.key(a, b));
  }

  heal(a: NodeId, b: NodeId): void {
    this.cutLinks.delete(Network.key(a, b));
  }

  healAll(): void {
    this.cutLinks.clear();
  }

  isCut(a: NodeId, b: NodeId): boolean {
    return this.cutLinks.has(Network.key(a, b));
  }

  setDropHook(hook: ((msg: Message) => boolean) | null): void {
    this.dropHook = hook;
  }

  /** Pops every message due at `now`; returns the ones that survive filtering. */
  deliverDue(now: number): Message[] {
    const due: Envelope[] = [];
    const remaining: Envelope[] = [];
    for (const env of this.queue) {
      (env.deliverAt <= now ? due : remaining).push(env);
    }
    due.sort((x, y) => x.deliverAt - y.deliverAt || x.seq - y.seq);
    this.queue = remaining;
    const delivered: Message[] = [];
    for (const env of due) {
      const { msg } = env;
      if (this.cutLinks.has(Network.key(msg.from, msg.to))) continue;
      if (this.dropHook !== null && this.dropHook(msg)) continue;
      delivered.push(msg);
    }
    return delivered;
  }

  get pending(): number {
    return this.queue.length;
  }
}
