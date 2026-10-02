import type { Clock } from './clock.ts';
import type { Message, NodeId } from './raft.ts';

interface Envelope {
  to: NodeId;
  msg: Message;
  deliverAt: number;
}

/**
 * In-memory message bus. No sockets: messages are queued and delivered
 * when the test clock reaches their delivery tick. Partitions are modeled
 * as blocked directed edges; blocked messages are dropped on send.
 */
export class Network {
  latency = 1;
  private clock: Clock;
  private handlers = new Map<NodeId, (msg: Message) => void>();
  private queue: Envelope[] = [];
  private blocked = new Set<string>();
  dropped = 0;
  delivered = 0;

  constructor(clock: Clock) {
    this.clock = clock;
  }

  register(id: NodeId, handler: (msg: Message) => void): void {
    this.handlers.set(id, handler);
  }

  private key(from: NodeId, to: NodeId): string {
    return `${from}->${to}`;
  }

  unlink(a: NodeId, b: NodeId): void {
    this.blocked.add(this.key(a, b));
    this.blocked.add(this.key(b, a));
  }

  link(a: NodeId, b: NodeId): void {
    this.blocked.delete(this.key(a, b));
    this.blocked.delete(this.key(b, a));
  }

  isolate(id: NodeId): void {
    for (const peer of this.handlers.keys()) {
      if (peer !== id) this.unlink(id, peer);
    }
  }

  heal(id: NodeId): void {
    for (const peer of this.handlers.keys()) {
      if (peer !== id) this.link(id, peer);
    }
  }

  send(from: NodeId, to: NodeId, msg: Message): void {
    if (this.blocked.has(this.key(from, to))) {
      this.dropped += 1;
      return;
    }
    this.queue.push({ to, msg, deliverAt: this.clock.now + this.latency });
  }

  /** Deliver every message whose delivery tick has arrived. */
  deliverDue(): void {
    const due: Envelope[] = [];
    this.queue = this.queue.filter((env) => {
      if (env.deliverAt <= this.clock.now) {
        due.push(env);
        return false;
      }
      return true;
    });
    for (const env of due) {
      const handler = this.handlers.get(env.to);
      if (handler) {
        this.delivered += 1;
        handler(env.msg);
      }
    }
  }
}
