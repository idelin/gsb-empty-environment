import { EventEmitter } from 'node:events';
import { HeartbeatHistory } from './heartbeat-history.ts';
import { normalSf } from './math.ts';

export interface PhiAccrualOptions {
  threshold?: number;
  maxSampleSize?: number;
  minStdDeviationMs?: number;
  acceptableHeartbeatPauseMs?: number;
  firstHeartbeatMeanMs?: number;
  firstHeartbeatStdDeviationMs?: number;
}

export type HealthState = 'available' | 'unavailable';

export interface StateChange {
  state: HealthState;
  phi: number;
  at: number;
}

const MIN_PROBABILITY = Number.MIN_VALUE;

export class PhiAccrualFailureDetector extends EventEmitter {
  readonly threshold: number;
  readonly minStdDeviationMs: number;
  readonly acceptableHeartbeatPauseMs: number;

  private readonly history: HeartbeatHistory;
  private readonly firstHeartbeatMeanMs: number;
  private readonly firstHeartbeatStdDeviationMs: number;
  private lastHeartbeatAt: number | undefined;
  private state: HealthState = 'available';

  constructor(options: PhiAccrualOptions = {}) {
    super();
    this.threshold = options.threshold ?? 8;
    this.minStdDeviationMs = options.minStdDeviationMs ?? 25;
    this.acceptableHeartbeatPauseMs = options.acceptableHeartbeatPauseMs ?? 0;
    this.firstHeartbeatMeanMs = options.firstHeartbeatMeanMs ?? 500;
    this.firstHeartbeatStdDeviationMs = options.firstHeartbeatStdDeviationMs ?? 100;
    this.history = new HeartbeatHistory(options.maxSampleSize ?? 200);
  }

  reportHeartbeat(now: number = Date.now()): void {
    if (this.lastHeartbeatAt === undefined) {
      this.history.add(this.firstHeartbeatMeanMs - this.firstHeartbeatStdDeviationMs);
      this.history.add(this.firstHeartbeatMeanMs + this.firstHeartbeatStdDeviationMs);
    } else {
      const interval = now - this.lastHeartbeatAt;
      if (interval >= 0) {
        this.history.add(interval);
      }
    }
    this.lastHeartbeatAt = now;
    this.check(now);
  }

  phi(now: number = Date.now()): number {
    if (this.lastHeartbeatAt === undefined || this.history.size === 0) {
      return 0;
    }
    const elapsed = now - this.lastHeartbeatAt;
    const mean = this.history.mean + this.acceptableHeartbeatPauseMs;
    const stdDeviation = Math.max(this.history.stdDeviation, this.minStdDeviationMs);
    const probabilityOfLaterArrival = normalSf(elapsed, mean, stdDeviation);
    return -Math.log10(Math.max(probabilityOfLaterArrival, MIN_PROBABILITY));
  }

  isAvailable(now: number = Date.now()): boolean {
    return this.phi(now) < this.threshold;
  }

  check(now: number = Date.now()): HealthState {
    const next: HealthState = this.isAvailable(now) ? 'available' : 'unavailable';
    if (next !== this.state) {
      this.state = next;
      const event: StateChange = { state: next, phi: this.phi(now), at: now };
      this.emit(next, event);
      if (next === 'unavailable') {
        this.emit('lease-revoked', event);
      } else {
        this.emit('lease-restored', event);
      }
    }
    return this.state;
  }

  get currentState(): HealthState {
    return this.state;
  }

  get sampleCount(): number {
    return this.history.size;
  }
}
