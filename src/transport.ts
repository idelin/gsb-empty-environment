/**
 * In-memory synchronous message transport with partition control.
 * Delivery is fully deterministic: messages are queued by `send` and
 * delivered by `flush`, which pumps until the queue drains (handlers may
 * enqueue further messages). Partitions drop cross-partition traffic.
 */
export interface Envelope {
  type: string;
  from: string;
  to: string;
}

export class Network {
  private handlers = new Map<string, (msg: Envelope) => void>();
  private queue: Envelope[] = [];
  private blockedLinks = new Set<string>(); // "from>to" directed
  private flushing = false;

  sent = 0;
  delivered = 0;
  dropped = 0;

  register(id: string, handler: (msg: Envelope) => void): void {
    this.handlers.set(id, handler);
  }

  send(msg: Envelope): void {
    this.sent++;
    this.queue.push(msg);
  }

  flush(): void {
    if (this.flushing) return; // outer pump will drain nested sends
    this.flushing = true;
    try {
      while (this.queue.length > 0) {
        const msg = this.queue.shift()!;
        if (this.isBlocked(msg.from, msg.to)) {
          this.dropped++;
          continue;
        }
        this.delivered++;
        this.handlers.get(msg.to)?.(msg);
      }
    } finally {
      this.flushing = false;
    }
  }

  private key(from: string, to: string): string {
    return `${from}>${to}`;
  }

  isBlocked(from: string, to: string): boolean {
    return this.blockedLinks.has(this.key(from, to));
  }

  block(from: string, to: string): void {
    this.blockedLinks.add(this.key(from, to));
  }

  unblock(from: string, to: string): void {
    this.blockedLinks.delete(this.key(from, to));
  }

  /** Cut all traffic between the given groups (traffic inside a group flows). */
  partition(groups: string[][]): void {
    for (let i = 0; i < groups.length; i++) {
      for (let j = i + 1; j < groups.length; j++) {
        for (const a of groups[i]) {
          for (const b of groups[j]) {
            this.block(a, b);
            this.block(b, a);
          }
        }
      }
    }
  }

  healAll(): void {
    this.blockedLinks.clear();
  }

  /** Messages still queued (must be 0 after a flush for leak assertions). */
  get inFlight(): number {
    return this.queue.length;
  }
}
