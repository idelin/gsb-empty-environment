import type { Clock } from "./clock.ts";

export interface DriftSample {
  wallDeltaMs: number;
  monotonicDeltaMs: number;
  observedStepMs: number;
  toleranceMs: number;
  safe: boolean;
}

export type DriftListener = (sample: DriftSample) => void;

export class ClockDriftMonitor {
  private readonly wallClock: Clock;
  private readonly monotonicClock: Clock;
  private readonly maxDriftMs: number;
  private lastWallMs: number;
  private lastMonotonicMs: number;
  private readonly unsafeListeners = new Set<DriftListener>();

  constructor(wallClock: Clock, monotonicClock: Clock, maxDriftMs: number) {
    this.wallClock = wallClock;
    this.monotonicClock = monotonicClock;
    this.maxDriftMs = maxDriftMs;
    this.lastWallMs = wallClock.now();
    this.lastMonotonicMs = monotonicClock.now();
  }

  resetBaseline(): void {
    this.lastWallMs = this.wallClock.now();
    this.lastMonotonicMs = this.monotonicClock.now();
  }

  sample(): DriftSample {
    const wallNow = this.wallClock.now();
    const monotonicNow = this.monotonicClock.now();
    const wallDeltaMs = wallNow - this.lastWallMs;
    const monotonicDeltaMs = monotonicNow - this.lastMonotonicMs;
    this.lastWallMs = wallNow;
    this.lastMonotonicMs = monotonicNow;

    const observedStepMs = wallDeltaMs - monotonicDeltaMs;
    const sample: DriftSample = {
      wallDeltaMs,
      monotonicDeltaMs,
      observedStepMs,
      toleranceMs: this.maxDriftMs,
      safe: Math.abs(observedStepMs) <= this.maxDriftMs,
    };
    if (!sample.safe) {
      for (const listener of this.unsafeListeners) {
        listener(sample);
      }
    }
    return sample;
  }

  onUnsafe(listener: DriftListener): () => void {
    this.unsafeListeners.add(listener);
    return () => {
      this.unsafeListeners.delete(listener);
    };
  }
}
