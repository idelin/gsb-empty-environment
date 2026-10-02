import { MinHeap } from './heap.ts';
import { SplitMix64 } from './rng.ts';

export type EventHandler = (sim: Simulator) => void;

export interface SimEvent {
  time: number;
  priority: number;
  seq: number;
  label: string;
  handler: EventHandler;
  cancelled: boolean;
}

export interface EventHandle {
  cancel(): void;
  readonly cancelled: boolean;
}

export interface EventRecord {
  step: number;
  time: number;
  seq: number;
  label: string;
}

export type EventObserver = (rec: EventRecord) => void;

export interface RunOptions {
  untilTime?: number;
  maxSteps?: number;
  observer?: EventObserver;
}

export interface RunResult {
  steps: number;
  endTime: number;
  drained: boolean;
}

export class StepLimitExceeded extends Error {
  readonly steps: number;
  constructor(limit: number) {
    super(`simulation exceeded maxSteps=${limit}`);
    this.name = 'StepLimitExceeded';
    this.steps = limit;
  }
}

export class Simulator {
  now = 0;
  steps = 0;
  readonly rng: SplitMix64;
  private heap = new MinHeap<SimEvent>();
  private seq = 0;
  private disposed = false;

  constructor(seed: bigint | number = 1) {
    this.rng = new SplitMix64(seed);
  }

  get pending(): number {
    return this.heap.size;
  }

  schedule(delay: number, label: string, handler: EventHandler, priority = 0): EventHandle {
    if (this.disposed) throw new Error('simulator is disposed');
    if (!Number.isFinite(delay) || delay < 0) {
      throw new RangeError(`delay must be a finite non-negative number, got ${delay}`);
    }
    const event: SimEvent = {
      time: this.now + delay,
      priority,
      seq: this.seq++,
      label,
      handler,
      cancelled: false,
    };
    this.heap.push(event);
    return {
      cancel: () => { event.cancelled = true; },
      get cancelled() { return event.cancelled; },
    };
  }

  run(options: RunOptions = {}): RunResult {
    if (this.disposed) throw new Error('simulator is disposed');
    const untilTime = options.untilTime ?? Number.POSITIVE_INFINITY;
    const maxSteps = options.maxSteps ?? Number.POSITIVE_INFINITY;
    const observer = options.observer;

    for (;;) {
      const event = this.heap.peek();
      if (event === undefined || event.time > untilTime) {
        return { steps: this.steps, endTime: this.now, drained: this.heap.size === 0 };
      }
      this.heap.pop();
      if (event.cancelled) continue;
      if (this.steps >= maxSteps) {
        this.heap.push(event);
        throw new StepLimitExceeded(maxSteps);
      }
      this.now = event.time;
      observer?.({ step: this.steps, time: event.time, seq: event.seq, label: event.label });
      this.steps++;
      event.handler(this);
    }
  }

  dispose(): void {
    this.heap.clear();
    this.disposed = true;
  }

  get isDisposed(): boolean {
    return this.disposed;
  }
}
