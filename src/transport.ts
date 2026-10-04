// In-memory message transport with deterministic async delivery.
// Supports link blocking / network partition for failure-injection tests,
// plus per-kind send statistics. No timers are used; delivery is drained
// through a microtask queue, so `settle()` observes a fully quiet network.

export interface Message {
  kind: string;
  from: number;
  to: number;
  [key: string]: unknown;
}

type Handler = (msg: Message) => void;

const key = (a: number, b: number): string => `${a}->${b}`;

export class Transport {
  private readonly handlers = new Map<number, Handler>();
  private readonly blocked = new Set<string>();
  private queue: Message[] = [];
  private draining: Promise<void> | null = null;
  readonly stats = { sent: new Map<string, number>(), dropped: 0 };

  register(id: number, handler: Handler): void {
    this.handlers.set(id, handler);
  }

  unregister(id: number): void {
    this.handlers.delete(id);
  }

  block(a: number, b: number): void {
    this.blocked.add(key(a, b));
  }

  unblock(a: number, b: number): void {
    this.blocked.delete(key(a, b));
  }

  partition(group: number[], all: number[]): void {
    for (const a of group) {
      for (const b of all) {
        if (!group.includes(b)) {
          this.block(a, b);
          this.block(b, a);
        }
      }
    }
  }

  heal(): void {
    this.blocked.clear();
  }

  send(msg: Message): void {
    this.stats.sent.set(msg.kind, (this.stats.sent.get(msg.kind) ?? 0) + 1);
    if (this.blocked.has(key(msg.from, msg.to))) {
      this.stats.dropped++;
      return;
    }
    this.queue.push(msg);
    if (!this.draining) this.draining = this.drain();
  }

  private async drain(): Promise<void> {
    while (this.queue.length > 0) {
      const msg = this.queue.shift() as Message;
      this.handlers.get(msg.to)?.(msg);
      await Promise.resolve();
    }
    this.draining = null;
  }

  async settle(): Promise<void> {
    while (this.draining) await this.draining;
  }

  get pending(): number {
    return this.queue.length;
  }

  sentCount(kind: string): number {
    return this.stats.sent.get(kind) ?? 0;
  }
}
