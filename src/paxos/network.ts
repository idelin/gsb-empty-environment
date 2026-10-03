import type { Message } from './types.ts';

type Handler = (msg: Message) => void;

/**
 * Deterministic in-memory message bus.
 *
 * Messages are enqueued by send()/broadcast() and delivered only when flush()
 * is called, so tests have full control over interleavings. Partition() splits
 * the bus into isolated groups; messages crossing group boundaries are dropped.
 */
export class Network {
  private handlers = new Map<string, Handler>();
  private queue: Message[] = [];
  private groups: Map<string, number> | null = null;
  private deliveredCount = 0;
  private droppedCount = 0;
  private byType = new Map<string, number>();

  register(id: string, handler: Handler): void {
    if (this.handlers.has(id)) throw new Error(`node already registered: ${id}`);
    this.handlers.set(id, handler);
  }

  unregister(id: string): void {
    this.handlers.delete(id);
  }

  send(msg: Message): void {
    this.queue.push(msg);
  }

  broadcast(from: string, make: (to: string) => Message): void {
    for (const id of this.handlers.keys()) {
      if (id !== from) this.send(make(id));
    }
  }

  private connected(a: string, b: string): boolean {
    if (this.groups === null) return true;
    const ga = this.groups.get(a);
    const gb = this.groups.get(b);
    return ga !== undefined && ga === gb;
  }

  /** Deliver every queued message (including messages enqueued while flushing). */
  flush(): void {
    while (this.queue.length > 0) {
      const msg = this.queue.shift() as Message;
      const handler = this.handlers.get(msg.to);
      if (handler !== undefined && this.connected(msg.from, msg.to)) {
        this.deliveredCount++;
        this.byType.set(msg.type, (this.byType.get(msg.type) ?? 0) + 1);
        handler(msg);
      } else {
        this.droppedCount++;
      }
    }
  }

  /** Split the cluster into isolated groups; unknown ids are unreachable. */
  partition(groups: string[][]): void {
    this.groups = new Map();
    groups.forEach((group, index) => {
      for (const id of group) this.groups?.set(id, index);
    });
  }

  heal(): void {
    this.groups = null;
  }

  get pending(): number {
    return this.queue.length;
  }
  get delivered(): number {
    return this.deliveredCount;
  }
  get dropped(): number {
    return this.droppedCount;
  }
  get registeredCount(): number {
    return this.handlers.size;
  }
  countByType(type: Message['type']): number {
    return this.byType.get(type) ?? 0;
  }
  resetStats(): void {
    this.deliveredCount = 0;
    this.droppedCount = 0;
    this.byType.clear();
  }

  dispose(): void {
    this.queue = [];
    this.handlers.clear();
    this.groups = null;
    this.resetStats();
  }
}
