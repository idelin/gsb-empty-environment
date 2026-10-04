import type { Message, MessagePred } from './types';

// Anything that can receive messages from the bus (cache nodes).
export interface MessageSink {
  name: string;
  isOnline(): boolean;
  receive(msg: Message): void;
}

interface Envelope {
  to: string;
  msg: Message;
}

// MessageBus is a deterministic, manually-clocked network.
// Messages are queued by send()/broadcast() and only delivered when the
// test (or simulator driver) calls deliverNext()/deliverAll()/deliverWhere().
// Messages addressed to offline sinks are dropped, simulating a partition.
export class MessageBus {
  private sinks: Map<string, MessageSink> = new Map();
  private queue: Array<Envelope> = [];
  private dropped = 0;

  attach(sink: MessageSink): void {
    this.sinks.set(sink.name, sink);
  }

  detach(name: string): void {
    this.sinks.delete(name);
  }

  send(to: string, msg: Message): void {
    this.queue.push({ to, msg });
  }

  broadcast(msg: Message): void {
    for (const name of this.sinks.keys()) {
      this.queue.push({ to: name, msg });
    }
  }

  pendingCount(): number {
    return this.queue.length;
  }

  droppedCount(): number {
    return this.dropped;
  }

  // Delivers the oldest queued message. Returns false when the queue is empty.
  deliverNext(): boolean {
    const env = this.queue.shift();
    if (!env) return false;
    this.deliver(env);
    return true;
  }

  deliverAll(): void {
    while (this.deliverNext()) {
      // drain
    }
  }

  // Delivers the first queued message matching pred (out-of-order delivery).
  deliverWhere(pred: MessagePred): boolean {
    const idx = this.queue.findIndex((env) => pred(env.msg, env.to));
    if (idx < 0) return false;
    const picked = this.queue.splice(idx, 1);
    this.deliver(picked[0]);
    return true;
  }

  private deliver(env: Envelope): void {
    const sink = this.sinks.get(env.to);
    if (!sink || !sink.isOnline()) {
      this.dropped++;
      return;
    }
    sink.receive(env.msg);
  }
}
