import type { Operation } from "./operations.ts";

type Handler = (op: Operation) => void;

interface Subscription {
  handler: Handler;
  online: boolean;
  offset: number;
}

export class MessageBus {
  private log: Operation[] = [];
  private subs = new Map<string, Subscription>();

  subscribe(id: string, handler: Handler): void {
    this.subs.set(id, { handler, online: true, offset: 0 });
  }

  publish(op: Operation): void {
    this.log.push(op);
  }

  flush(onlyId?: string): void {
    for (const [id, sub] of this.subs) {
      if (onlyId !== undefined && id !== onlyId) continue;
      if (!sub.online) continue;
      while (sub.offset < this.log.length) {
        sub.handler(this.log[sub.offset]);
        sub.offset += 1;
      }
    }
  }

  disconnect(id: string): void {
    const sub = this.subs.get(id);
    if (sub) sub.online = false;
  }

  reconnect(id: string): void {
    const sub = this.subs.get(id);
    if (sub) sub.online = true;
  }

  isOnline(id: string): boolean {
    return this.subs.get(id)?.online ?? false;
  }

  get logLength(): number {
    return this.log.length;
  }
}
