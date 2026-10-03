import { Message, NodeId } from './types';

interface Envelope {
  msg: Message;
  deliverAt: number;
}

/**
 * Simulated message-passing network with partition support.
 * Messages are delivered one tick after being sent, and only when
 * sender and receiver currently share a partition group.
 */
export class Network {
  now = 0;
  dropHook: ((msg: Message) => boolean) | null = null;
  readonly stats = { sent: 0, delivered: 0, droppedByPartition: 0, droppedByHook: 0 };

  private queue: Envelope[] = [];
  private groups = new Map<NodeId, number>();

  join(nodeId: NodeId, group = 0): void {
    this.groups.set(nodeId, group);
  }

  partition(groups: NodeId[][]): void {
    groups.forEach((ids, idx) => ids.forEach((id) => this.groups.set(id, idx)));
  }

  heal(): void {
    for (const id of this.groups.keys()) this.groups.set(id, 0);
  }

  canDeliver(a: NodeId, b: NodeId): boolean {
    return this.groups.get(a) === this.groups.get(b);
  }

  send(msg: Message): void {
    this.stats.sent++;
    this.queue.push({ msg, deliverAt: this.now + 1 });
  }

  deliver(): Message[] {
    const out: Message[] = [];
    this.queue = this.queue.filter((env) => {
      if (env.deliverAt > this.now) return true;
      if (!this.canDeliver(env.msg.from, env.msg.to)) {
        this.stats.droppedByPartition++;
        return false;
      }
      if (this.dropHook && this.dropHook(env.msg)) {
        this.stats.droppedByHook++;
        return false;
      }
      this.stats.delivered++;
      out.push(env.msg);
      return false;
    });
    return out;
  }

  get pending(): number {
    return this.queue.length;
  }
}
