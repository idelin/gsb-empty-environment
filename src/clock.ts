export interface Clock {
  now(): number;
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
}

export class MonotonicClock implements Clock {
  now(): number {
    return performance.now();
  }
}

export class ManualClock implements Clock {
  private currentMs: number;

  constructor(startMs = 0) {
    this.currentMs = startMs;
  }

  now(): number {
    return this.currentMs;
  }

  advance(deltaMs: number): void {
    if (deltaMs < 0) {
      throw new Error("ManualClock only moves forward");
    }
    this.currentMs += deltaMs;
  }
}

export interface ClockAdjustment {
  deltaMs: number;
  resultingOffsetMs: number;
}

export type AdjustmentListener = (adjustment: ClockAdjustment) => void;

export class AdjustableClock implements Clock {
  private offsetMs = 0;
  private readonly listeners = new Set<AdjustmentListener>();
  private readonly reference: Clock;

  constructor(reference: Clock) {
    this.reference = reference;
  }

  now(): number {
    return this.reference.now() + this.offsetMs;
  }

  offset(): number {
    return this.offsetMs;
  }

  adjust(deltaMs: number): void {
    this.offsetMs += deltaMs;
    const adjustment: ClockAdjustment = {
      deltaMs,
      resultingOffsetMs: this.offsetMs,
    };
    for (const listener of this.listeners) {
      listener(adjustment);
    }
  }

  onAdjust(listener: AdjustmentListener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
}
