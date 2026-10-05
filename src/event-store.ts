import type { SimEvent, SubscriptionFilter } from './types.js';
import { matchesFilter } from './cursor.js';

export type AppendListener = (event: SimEvent) => void;

export type Unsubscribe = () => void;

export class EventStore {
  private events: SimEvent[] = [];
  private baseSeq = 1;
  private nextSeq = 1;
  private pins = new Map<number, number>();
  private listeners = new Set<AppendListener>();

  append(kind: string, payload: Record<string, unknown>, at: number): SimEvent {
    const seq = this.nextSeq;
    this.nextSeq += 1;
    const event: SimEvent = { seq, id: 'evt-' + seq, kind, payload, at };
    this.events.push(event);
    for (const listener of [...this.listeners]) listener(event);
    return event;
  }

  head(): number {
    return this.nextSeq - 1;
  }

  oldest(): number {
    return this.baseSeq;
  }

  canReplayFrom(afterSeq: number): boolean {
    return afterSeq >= this.baseSeq - 1;
  }

  readFrom(afterSeq: number, filter: SubscriptionFilter): SimEvent[] {
    const start = Math.max(afterSeq + 1, this.baseSeq);
    const out: SimEvent[] = [];
    for (const event of this.events) {
      if (event.seq < start) continue;
      if (!matchesFilter(filter, event.kind)) continue;
      out.push(event);
    }
    return out;
  }

  compact(beforeSeq: number): number {
    let boundary = Math.min(beforeSeq, this.nextSeq);
    for (const pinSeq of this.pins.keys()) {
      boundary = Math.min(boundary, pinSeq);
    }
    if (boundary <= this.baseSeq) return 0;
    let dropped = 0;
    const kept: SimEvent[] = [];
    for (const event of this.events) {
      if (event.seq < boundary) dropped += 1;
      else kept.push(event);
    }
    this.events = kept;
    this.baseSeq = boundary;
    return dropped;
  }

  pin(seq: number): Unsubscribe {
    const current = this.pins.get(seq) ?? 0;
    this.pins.set(seq, current + 1);
    let released = false;
    return () => {
      if (released) return;
      released = true;
      const count = this.pins.get(seq) ?? 0;
      if (count <= 1) this.pins.delete(seq);
      else this.pins.set(seq, count - 1);
    };
  }

  onAppend(listener: AppendListener): Unsubscribe {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
}
