import type { Clock, TimerHandle } from "./clock.ts";

let nextReasonId = 1;

export class CancellationReason {
  readonly id: number;
  readonly message: string;
  readonly cause?: CancellationReason;

  constructor(message: string, cause?: CancellationReason) {
    this.id = nextReasonId++;
    this.message = message;
    this.cause = cause;
  }

  toString(): string {
    const base = `cancellation#${this.id}: ${this.message}`;
    return this.cause ? `${base} (caused by ${this.cause})` : base;
  }
}

export type CancelCallback = (reason: CancellationReason) => void;

export interface Context {
  readonly clock: Clock;
  deadline(): number | undefined;
  isCancelled(): boolean;
  reason(): CancellationReason | undefined;
  onCancel(callback: CancelCallback): () => void;
  cancel(message?: string): CancellationReason;
  setDeadline(at: number): number;
  clearDeadline(): void;
  detach(): void;
  done(): Promise<CancellationReason>;
}

class ContextImpl implements Context {
  readonly clock: Clock;

  private parent: ContextImpl | undefined;
  private readonly children = new Set<ContextImpl>();
  private readonly callbacks = new Set<CancelCallback>();
  private ownDeadline: number | undefined;
  private timer: TimerHandle | undefined;
  private timerAt: number | undefined;
  private cancellation: CancellationReason | undefined;
  private readonly donePromise: Promise<CancellationReason>;
  private resolveDone!: (reason: CancellationReason) => void;

  constructor(clock: Clock, parent?: ContextImpl) {
    this.clock = clock;
    this.donePromise = new Promise((resolve) => {
      this.resolveDone = resolve;
    });
    if (parent !== undefined) {
      if (parent.cancellation !== undefined) {
        this.cancelWith(new CancellationReason("parent cancelled", parent.cancellation));
      } else {
        this.parent = parent;
        parent.children.add(this);
      }
    }
  }

  deadline(): number | undefined {
    return this.effectiveDeadline();
  }

  isCancelled(): boolean {
    return this.cancellation !== undefined;
  }

  reason(): CancellationReason | undefined {
    return this.cancellation;
  }

  done(): Promise<CancellationReason> {
    return this.donePromise;
  }

  onCancel(callback: CancelCallback): () => void {
    if (this.cancellation !== undefined) {
      callback(this.cancellation);
      return () => {};
    }
    this.callbacks.add(callback);
    return () => {
      this.callbacks.delete(callback);
    };
  }

  cancel(message = "cancelled"): CancellationReason {
    if (this.cancellation !== undefined) return this.cancellation;
    const reason = new CancellationReason(message);
    this.cancelWith(reason);
    return reason;
  }

  // Stores the requested deadline as this context's own deadline and returns
  // the resulting effective deadline. The effective deadline is always
  // bounded by the parent's effective deadline, so an extension never
  // exceeds the parent; the own deadline is kept intact so detach() can
  // restore it.
  setDeadline(at: number): number {
    if (this.cancellation !== undefined) {
      return this.effectiveDeadline() ?? at;
    }
    this.ownDeadline = at;
    this.recompute();
    return this.effectiveDeadline() ?? at;
  }

  clearDeadline(): void {
    if (this.cancellation !== undefined) return;
    this.ownDeadline = undefined;
    this.recompute();
  }

  detach(): void {
    if (this.parent === undefined) return;
    this.parent.children.delete(this);
    this.parent = undefined;
    this.recompute();
  }

  private effectiveDeadline(): number | undefined {
    const parentDeadline = this.parent?.effectiveDeadline();
    if (this.ownDeadline === undefined) return parentDeadline;
    if (parentDeadline === undefined) return this.ownDeadline;
    return Math.min(this.ownDeadline, parentDeadline);
  }

  private recompute(): void {
    if (this.cancellation !== undefined) return;
    const effective = this.effectiveDeadline();
    if (effective !== this.timerAt) {
      if (this.timer !== undefined) {
        this.clock.clearTimer(this.timer);
        this.timer = undefined;
      }
      this.timerAt = effective;
      if (effective !== undefined) {
        this.timer = this.clock.setTimer(effective, () => {
          this.cancelWith(new CancellationReason("deadline exceeded"));
        });
      }
    }
    if (effective !== undefined && effective <= this.clock.now()) {
      this.cancelWith(new CancellationReason("deadline exceeded"));
      return;
    }
    for (const child of [...this.children]) {
      child.recompute();
    }
  }

  private cancelWith(reason: CancellationReason): void {
    if (this.cancellation !== undefined) return;
    this.cancellation = reason;
    if (this.timer !== undefined) {
      this.clock.clearTimer(this.timer);
      this.timer = undefined;
      this.timerAt = undefined;
    }
    const callbacks = [...this.callbacks];
    this.callbacks.clear();
    for (const callback of callbacks) {
      callback(reason);
    }
    const children = [...this.children];
    this.children.clear();
    for (const child of children) {
      child.parent = undefined;
      child.cancelWith(new CancellationReason("parent cancelled", reason));
    }
    this.resolveDone(reason);
  }
}

export function background(clock: Clock): Context {
  return new ContextImpl(clock);
}

export function withCancel(parent: Context): Context {
  return new ContextImpl(parent.clock, parent as ContextImpl);
}

export function withDeadline(parent: Context, at: number): Context {
  const child = new ContextImpl(parent.clock, parent as ContextImpl);
  child.setDeadline(at);
  return child;
}

export function withTimeout(parent: Context, ttl: number): Context {
  return withDeadline(parent, parent.clock.now() + ttl);
}
