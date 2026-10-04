import { SystemClock } from './clock.ts';
import type { Clock } from './clock.ts';

export type CancelCode = 'canceled' | 'deadline_exceeded';

export interface CancelReason {
  readonly code: CancelCode;
  readonly message: string;
  readonly at: number;
}

type CancelCallback = (reason: CancelReason) => void;

function minDeadline(a: number | null, b: number | null): number | null {
  if (a === null) return b;
  if (b === null) return a;
  return a < b ? a : b;
}

export class Context {
  private readonly clock: Clock;
  private parent: Context | null;
  private readonly children = new Set<Context>();
  private ownDeadline: number | null = null;
  private effectiveDeadline: number | null = null;
  private timer: unknown = null;
  private readonly callbacks = new Set<CancelCallback>();
  private cancelReason: CancelReason | null = null;

  private constructor(clock: Clock, parent: Context | null) {
    this.clock = clock;
    this.parent = parent;
    if (parent !== null) {
      parent.children.add(this);
      this.effectiveDeadline = parent.effectiveDeadline;
      if (parent.cancelReason !== null) {
        this.cancelWith(parent.cancelReason);
      }
    }
  }

  static background(clock?: Clock): Context {
    return new Context(clock ?? new SystemClock(), null);
  }

  get cancelled(): boolean {
    return this.cancelReason !== null;
  }

  get reason(): CancelReason | null {
    return this.cancelReason;
  }

  get deadline(): number | null {
    return this.effectiveDeadline;
  }

  withCancel(): { context: Context; cancel: (message?: string) => boolean } {
    const child = new Context(this.clock, this);
    return {
      context: child,
      cancel: (message?: string) => child.cancel(message),
    };
  }

  withDeadline(at: number): Context {
    const child = new Context(this.clock, this);
    child.ownDeadline = at;
    child.recomputeEffective();
    return child;
  }

  withTimeout(ms: number): Context {
    return this.withDeadline(this.clock.now() + ms);
  }

  detach(): Context {
    const detached = new Context(this.clock, null);
    detached.ownDeadline = this.effectiveDeadline;
    detached.recomputeEffective();
    if (this.cancelReason !== null) {
      detached.cancelWith(this.cancelReason);
    }
    return detached;
  }

  setDeadline(at: number): void {
    if (this.cancelReason !== null) return;
    this.ownDeadline = at;
    this.recomputeEffective();
  }

  clearDeadline(): void {
    if (this.cancelReason !== null) return;
    this.ownDeadline = null;
    this.recomputeEffective();
  }

  cancel(message = 'context canceled'): boolean {
    return this.cancelWith({
      code: 'canceled',
      message,
      at: this.clock.now(),
    });
  }

  onCancel(callback: CancelCallback): () => void {
    if (this.cancelReason !== null) {
      callback(this.cancelReason);
      return () => {};
    }
    this.callbacks.add(callback);
    return () => {
      this.callbacks.delete(callback);
    };
  }

  private recomputeEffective(): void {
    if (this.cancelReason !== null) return;
    const parentDeadline = this.parent === null ? null : this.parent.effectiveDeadline;
    const next = minDeadline(this.ownDeadline, parentDeadline);
    if (next === this.effectiveDeadline) return;
    this.effectiveDeadline = next;
    this.reschedule();
    for (const child of this.children) {
      child.recomputeEffective();
    }
  }

  private reschedule(): void {
    if (this.timer !== null) {
      this.clock.clearTimeout(this.timer);
      this.timer = null;
    }
    if (this.effectiveDeadline === null) return;
    const delay = this.effectiveDeadline - this.clock.now();
    if (delay <= 0) {
      this.expire();
      return;
    }
    this.timer = this.clock.setTimeout(() => this.expire(), delay);
  }

  private expire(): void {
    this.timer = null;
    this.cancelWith({
      code: 'deadline_exceeded',
      message: 'context deadline exceeded',
      at: this.clock.now(),
    });
  }

  private cancelWith(reason: CancelReason): boolean {
    if (this.cancelReason !== null) return false;
    this.cancelReason = reason;
    if (this.timer !== null) {
      this.clock.clearTimeout(this.timer);
      this.timer = null;
    }
    if (this.parent !== null) {
      this.parent.children.delete(this);
      this.parent = null;
    }
    const pending = [...this.callbacks];
    this.callbacks.clear();
    for (const callback of pending) {
      callback(reason);
    }
    for (const child of [...this.children]) {
      child.cancelWith(reason);
    }
    this.children.clear();
    return true;
  }
}
