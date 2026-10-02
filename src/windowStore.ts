import type { WindowStats } from './types.ts';

export interface WindowEvent {
  subjectId: string;
  action: string;
  amount: number;
  timestamp: number;
}

export class WindowStore {
  private events: WindowEvent[] = [];

  record(event: WindowEvent): void {
    this.events.push(event);
  }

  query(subjectId: string, action: string, now: number, windowMs: number): WindowStats {
    const from = now - windowMs;
    let count = 0;
    let sum = 0;
    for (const event of this.events) {
      if (
        event.subjectId === subjectId &&
        event.action === action &&
        event.timestamp > from &&
        event.timestamp <= now
      ) {
        count += 1;
        sum += event.amount;
      }
    }
    return { count, sum };
  }

  prune(before: number): void {
    this.events = this.events.filter((event) => event.timestamp > before);
  }

  size(): number {
    return this.events.length;
  }
}
