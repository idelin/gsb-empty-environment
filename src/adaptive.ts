/**
 * Adaptive dynamic-threshold controller with load awareness.
 *
 * Ingests external pressure / environment metrics, computes a normalized
 * pressure score, and elastically scales an internal concurrency limit:
 *  - multiplicative shrink with escalation + cooldown under pressure (anti-avalanche),
 *  - additive, jittered, streak-gated growth when idle (no thundering herd),
 *  - hysteresis band between thresholds to avoid oscillation,
 *  - adjustment rate limiting per time window,
 *  - permit-based admission control that sheds load when saturated.
 *
 * Leak safety: bounded ring buffers, idempotent permits, waiter timeouts,
 * and dispose() that clears every timer, waiter and listener.
 */

export interface LoadSample {
  /** Currently in-flight units of work. */
  inflight: number;
  /** Pending work items waiting upstream. */
  queueDepth: number;
  /** Recent failure ratio, 0..1. */
  errorRate: number;
  /** Observed latency of completed work, ms. */
  latencyMs: number;
  /** Event-loop lag, ms. */
  eventLoopLagMs: number;
  /** Heap usage fraction, 0..1. */
  memoryUsage: number;
}

export interface ScoreWeights {
  queue: number;
  error: number;
  latency: number;
  lag: number;
  memory: number;
}

export interface AdaptiveOptions {
  minLimit: number;
  maxLimit: number;
  initialLimit?: number;
  /** Score at or above which the limit shrinks. Default 0.75. */
  shrinkThreshold?: number;
  /** Score at or below which the limit may grow. Default 0.35. */
  growThreshold?: number;
  /** Consecutive low-pressure samples required before one growth step. Default 3. */
  growStreak?: number;
  /** Minimum quiet time after a shrink before any further adjustment. Default 1000ms. */
  cooldownMs?: number;
  /** Multiplicative shrink factor, 0 < f < 1. Default 0.7. */
  shrinkFactor?: number;
  /** Extra decay applied per consecutive shrink (escalation). Default 0.85. */
  escalation?: number;
  /** Hard floor for the compounded shrink factor. Default 0.5. */
  minShrinkFactor?: number;
  /** Additive growth step per accepted growth. Default 1. */
  growthStep?: number;
  /** Symmetric jitter applied to growth steps, 0..1. Default 0.1. */
  jitterPct?: number;
  /** Latency (ms) treated as "fully loaded" for score normalization. Default 100. */
  latencyTargetMs?: number;
  /** Event-loop lag (ms) treated as "fully loaded". Default 50. */
  lagTargetMs?: number;
  /** Score weights; normalized internally. */
  weights?: Partial<ScoreWeights>;
  /** Max adjustments accepted per windowMs. Default 10. */
  maxAdjustmentsPerWindow?: number;
  /** Rate-limit window, ms. Default 5000. */
  windowMs?: number;
  /** Ring-buffer capacity for samples and adjustments. Default 64. */
  historySize?: number;
  /** Injectable clock (ms) for deterministic tests. */
  now?: () => number;
  /** Injectable RNG in [0, 1) for jitter. */
  random?: () => number;
}

export type AdjustmentDirection = 'shrink' | 'grow';

export interface Adjustment {
  at: number;
  from: number;
  to: number;
  direction: AdjustmentDirection;
  score: number;
  reason: string;
  applied: boolean;
}

export type ControllerEvent = 'adjust' | 'reject' | 'dispose';
type Listener = (payload: unknown) => void;

export class DisposedError extends Error {
  constructor() {
    super('controller has been disposed');
    this.name = 'DisposedError';
  }
}

export class AcquireTimeoutError extends Error {
  constructor(timeoutMs: number) {
    super(`acquire timed out after ${timeoutMs}ms`);
    this.name = 'AcquireTimeoutError';
  }
}

/** Bounded FIFO ring buffer: fixed capacity, O(1) push, never grows. */
class RingBuffer<T> {
  private items: T[] = [];
  readonly capacity: number;
  constructor(capacity: number) {
    this.capacity = capacity;
  }
  push(item: T): void {
    if (this.items.length >= this.capacity) this.items.shift();
    this.items.push(item);
  }
  get length(): number {
    return this.items.length;
  }
  last(): T | undefined {
    return this.items[this.items.length - 1];
  }
  toArray(): T[] {
    return this.items.slice();
  }
  clear(): void {
    this.items.length = 0;
  }
}

export interface Permit {
  /** Idempotent: second and later calls are no-ops returning false. */
  release(): boolean;
  readonly released: boolean;
}

interface Waiter {
  resolve: (permit: Permit) => void;
  reject: (err: Error) => void;
  timer: ReturnType<typeof setTimeout> | null;
}

export class AdaptiveLoadController {
  private readonly opts: Required<Omit<AdaptiveOptions, 'weights' | 'now' | 'random'>> & { weights: ScoreWeights };
  private readonly now: () => number;
  private readonly random: () => number;

  private limit: number;
  private inflightCount = 0;
  private growStreakCount = 0;
  private consecutiveShrinks = 0;
  private lastShrinkAt = -Infinity;
  private adjustmentTimes: number[] = [];
  private readonly samples: RingBuffer<LoadSample & { score: number }>;
  private readonly adjustments: RingBuffer<Adjustment>;
  private readonly listeners = new Map<ControllerEvent, Set<Listener>>();
  private waiters: Waiter[] = [];
  private autoTimer: ReturnType<typeof setInterval> | null = null;
  private disposed = false;

  constructor(options: AdaptiveOptions) {
    if (options.minLimit < 1) throw new RangeError('minLimit must be >= 1');
    if (options.maxLimit < options.minLimit) throw new RangeError('maxLimit must be >= minLimit');
    const weights: ScoreWeights = {
      queue: 0.3,
      error: 0.25,
      latency: 0.2,
      lag: 0.15,
      memory: 0.1,
      ...options.weights,
    };
    this.opts = {
      initialLimit: options.initialLimit ?? options.minLimit,
      shrinkThreshold: options.shrinkThreshold ?? 0.75,
      growThreshold: options.growThreshold ?? 0.35,
      growStreak: options.growStreak ?? 3,
      cooldownMs: options.cooldownMs ?? 1000,
      shrinkFactor: options.shrinkFactor ?? 0.7,
      escalation: options.escalation ?? 0.85,
      minShrinkFactor: options.minShrinkFactor ?? 0.5,
      growthStep: options.growthStep ?? 1,
      jitterPct: options.jitterPct ?? 0.1,
      latencyTargetMs: options.latencyTargetMs ?? 100,
      lagTargetMs: options.lagTargetMs ?? 50,
      maxAdjustmentsPerWindow: options.maxAdjustmentsPerWindow ?? 10,
      windowMs: options.windowMs ?? 5000,
      historySize: options.historySize ?? 64,
      minLimit: options.minLimit,
      maxLimit: options.maxLimit,
      weights,
    };
    if (this.opts.growThreshold >= this.opts.shrinkThreshold) {
      throw new RangeError('growThreshold must be < shrinkThreshold (hysteresis band required)');
    }
    this.now = options.now ?? (() => Date.now());
    this.random = options.random ?? (() => Math.random());
    this.limit = this.clamp(this.opts.initialLimit);
    this.samples = new RingBuffer(this.opts.historySize);
    this.adjustments = new RingBuffer(this.opts.historySize);
  }

  // ---------------------------------------------------------------- metrics

  /** Normalized pressure score in [0, 1] from a load sample. */
  score(sample: LoadSample): number {
    const w = this.opts.weights;
    const normQueue = sample.queueDepth / (sample.queueDepth + this.limit);
    const normLatency = sample.latencyMs / (sample.latencyMs + this.opts.latencyTargetMs);
    const normLag = sample.eventLoopLagMs / (sample.eventLoopLagMs + this.opts.lagTargetMs);
    const clamp01 = (x: number) => (x < 0 ? 0 : x > 1 ? 1 : x);
    const total = w.queue + w.error + w.latency + w.lag + w.memory;
    return (
      (w.queue * clamp01(normQueue) +
        w.error * clamp01(sample.errorRate) +
        w.latency * clamp01(normLatency) +
        w.lag * clamp01(normLag) +
        w.memory * clamp01(sample.memoryUsage)) /
      total
    );
  }

  /**
   * Feed one metrics sample. Returns the adjustment decision
   * (`applied: false` with a reason when suppressed by cooldown,
   * hysteresis, streak gating or rate limiting).
   */
  record(sample: LoadSample): Adjustment | null {
    if (this.disposed) throw new DisposedError();
    const score = this.score(sample);
    this.samples.push({ ...sample, score });
    const now = this.now();

    if (score >= this.opts.shrinkThreshold) {
      this.growStreakCount = 0;
      this.consecutiveShrinks++;
      const factor = Math.max(
        this.opts.minShrinkFactor,
        this.opts.shrinkFactor * this.opts.escalation ** (this.consecutiveShrinks - 1),
      );
      const target = Math.max(this.opts.minLimit, Math.floor(this.limit * factor));
      return this.tryAdjust(target, 'shrink', score, now, 'pressure above shrink threshold');
    }
    if (score <= this.opts.growThreshold) {
      this.consecutiveShrinks = 0;
      this.growStreakCount++;
      if (this.growStreakCount < this.opts.growStreak) {
        return this.suppressed('grow', score, now, `growth streak ${this.growStreakCount}/${this.opts.growStreak}`);
      }
      if (now - this.lastShrinkAt < this.opts.cooldownMs) {
        return this.suppressed('grow', score, now, 'cooldown after shrink');
      }
      const jitter = 1 + (this.random() * 2 - 1) * this.opts.jitterPct;
      const step = Math.max(1, Math.round(this.opts.growthStep * jitter));
      const target = Math.min(this.opts.maxLimit, this.limit + step);
      this.growStreakCount = 0;
      return this.tryAdjust(target, 'grow', score, now, 'sustained low pressure');
    }
    // Hysteresis band: no action, reset streaks so decisions need sustained signal.
    this.growStreakCount = 0;
    this.consecutiveShrinks = 0;
    return null;
  }

  private suppressed(
    direction: AdjustmentDirection,
    score: number,
    now: number,
    reason: string,
  ): Adjustment {
    return { at: now, from: this.limit, to: this.limit, direction, score, reason, applied: false };
  }

  private tryAdjust(
    target: number,
    direction: AdjustmentDirection,
    score: number,
    now: number,
    reason: string,
  ): Adjustment {
    if (target === this.limit) {
      return this.suppressed(direction, score, now, `already at ${direction === 'shrink' ? 'min' : 'max'} limit`);
    }
    if (direction === 'shrink' && now - this.lastShrinkAt < this.opts.cooldownMs) {
      this.consecutiveShrinks--;
      return this.suppressed(direction, score, now, 'cooldown after shrink');
    }
    this.adjustmentTimes = this.adjustmentTimes.filter((t) => now - t < this.opts.windowMs);
    if (this.adjustmentTimes.length >= this.opts.maxAdjustmentsPerWindow) {
      return this.suppressed(direction, score, now, 'adjustment rate limit reached');
    }
    const adj: Adjustment = {
      at: now,
      from: this.limit,
      to: target,
      direction,
      score,
      reason,
      applied: true,
    };
    this.limit = target;
    this.adjustmentTimes.push(now);
    if (direction === 'shrink') this.lastShrinkAt = now;
    this.adjustments.push(adj);
    this.emit('adjust', adj);
    this.drainWaiters();
    return adj;
  }

  private clamp(v: number): number {
    return Math.min(this.opts.maxLimit, Math.max(this.opts.minLimit, Math.floor(v)));
  }

  // ------------------------------------------------------------- admission

  /** Non-blocking admission: returns a permit or null when saturated. */
  tryAcquire(): Permit | null {
    if (this.disposed) throw new DisposedError();
    if (this.inflightCount >= this.limit) {
      this.emit('reject', { inflight: this.inflightCount, limit: this.limit });
      return null;
    }
    return this.grant();
  }

  /** Async admission: waits for capacity, with optional timeout. */
  acquire(options: { timeoutMs?: number } = {}): Promise<Permit> {
    if (this.disposed) return Promise.reject(new DisposedError());
    const permit = this.tryAcquire();
    if (permit) return Promise.resolve(permit);
    return new Promise<Permit>((resolve, reject) => {
      const waiter: Waiter = { resolve, reject, timer: null };
      if (options.timeoutMs !== undefined) {
        waiter.timer = setTimeout(() => {
          const idx = this.waiters.indexOf(waiter);
          if (idx >= 0) this.waiters.splice(idx, 1);
          reject(new AcquireTimeoutError(options.timeoutMs!));
        }, options.timeoutMs);
      }
      this.waiters.push(waiter);
    });
  }

  private grant(): Permit {
    this.inflightCount++;
    let released = false;
    const self = this;
    return {
      get released() {
        return released;
      },
      release(): boolean {
        if (released) return false;
        released = true;
        self.inflightCount--;
        self.drainWaiters();
        return true;
      },
    };
  }

  private drainWaiters(): void {
    while (!this.disposed && this.waiters.length > 0 && this.inflightCount < this.limit) {
      const waiter = this.waiters.shift()!;
      if (waiter.timer) clearTimeout(waiter.timer);
      waiter.resolve(this.grant());
    }
  }

  // ---------------------------------------------------------------- events

  on(event: ControllerEvent, listener: Listener): void {
    let set = this.listeners.get(event);
    if (!set) this.listeners.set(event, (set = new Set()));
    set.add(listener);
  }

  off(event: ControllerEvent, listener: Listener): void {
    this.listeners.get(event)?.delete(listener);
  }

  listenerCount(event?: ControllerEvent): number {
    if (event) return this.listeners.get(event)?.size ?? 0;
    let n = 0;
    for (const set of this.listeners.values()) n += set.size;
    return n;
  }

  private emit(event: ControllerEvent, payload: unknown): void {
    const set = this.listeners.get(event);
    if (!set) return;
    for (const listener of [...set]) listener(payload);
  }

  // -------------------------------------------------------------- lifecycle

  /** Periodically record samples from `collect`. Returns a stop function. */
  startAutoSample(collect: () => LoadSample, intervalMs: number): () => void {
    if (this.disposed) throw new DisposedError();
    if (this.autoTimer) throw new Error('auto-sampling already started');
    this.autoTimer = setInterval(() => this.record(collect()), intervalMs);
    if (typeof this.autoTimer.unref === 'function') this.autoTimer.unref();
    return () => this.stopAutoSample();
  }

  stopAutoSample(): void {
    if (this.autoTimer) {
      clearInterval(this.autoTimer);
      this.autoTimer = null;
    }
  }

  get isAutoSampling(): boolean {
    return this.autoTimer !== null;
  }

  /** Release every resource: timers, waiters (rejected), listeners, history. */
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.stopAutoSample();
    const err = new DisposedError();
    for (const waiter of this.waiters.splice(0)) {
      if (waiter.timer) clearTimeout(waiter.timer);
      waiter.reject(err);
    }
    this.emit('dispose', undefined);
    this.listeners.clear();
    this.samples.clear();
    this.adjustments.clear();
    this.adjustmentTimes = [];
  }

  // ------------------------------------------------------------------ stats

  get limit(): number {
    return this.limit;
  }
  get inflight(): number {
    return this.inflightCount;
  }
  get waiting(): number {
    return this.waiters.length;
  }
  get isDisposed(): boolean {
    return this.disposed;
  }
  get sampleCount(): number {
    return this.samples.length;
  }
  get adjustmentHistory(): Adjustment[] {
    return this.adjustments.toArray();
  }
}
