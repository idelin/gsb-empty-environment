/**
 * 自适应动态阈值伸缩 + 负载感知并发限制器。
 *
 * - AdaptiveThreshold: 基于外部压力指标（CPU/内存/错误率/队列深度）做 AIMD
 *   （加性增大、乘性减小）阈值伸缩，带滞回区间与冷却期，规避振荡与雪崩。
 * - LoadAwareLimiter: 以动态阈值为并发上限的信号量，支持负载卸载（shedding）、
 *   取消（AbortSignal）与严格无泄漏的资源回收。
 */

import os from 'node:os';

export interface PressureMetrics {
  /** 0..1 CPU 负载。 */
  cpuLoad: number;
  /** 0..1 内存占用率。 */
  memoryUsage: number;
  /** 0..1 近期错误率。 */
  errorRate: number;
  /** 等待中的请求数。 */
  queueDepth: number;
}

export interface AdaptiveThresholdOptions {
  min: number;
  max: number;
  initial?: number;
  /** 压力上阈：综合压力超过则乘性收缩。默认 0.7。 */
  highWatermark?: number;
  /** 压力下阈：综合压力低于则加性扩张。默认 0.3。 */
  lowWatermark?: number;
  /** 扩张步长（加性）。默认 1。 */
  increaseStep?: number;
  /** 收缩因子（乘性，0<factor<1）。默认 0.5。 */
  decreaseFactor?: number;
  /** 两次调整之间的冷却毫秒数，防抖防雪崩。默认 100。 */
  cooldownMs?: number;
  /** 队列深度归一化参考值。默认 100。 */
  queueDepthScale?: number;
  /** 历史记录保留上限（防内存泄漏）。默认 256。 */
  maxHistory?: number;
  now?: () => number;
}

export interface ThresholdSnapshot {
  threshold: number;
  pressure: number;
  direction: 'up' | 'down' | 'hold';
  at: number;
}

export class AdaptiveThreshold {
  private readonly min: number;
  private readonly max: number;
  private readonly high: number;
  private readonly low: number;
  private readonly step: number;
  private readonly factor: number;
  private readonly cooldownMs: number;
  private readonly queueScale: number;
  private readonly maxHistory: number;
  private readonly now: () => number;

  private current: number;
  private lastAdjustAt = -Infinity;
  private readonly history: ThresholdSnapshot[] = [];

  constructor(opts: AdaptiveThresholdOptions) {
    if (!(opts.min > 0) || !(opts.max >= opts.min)) {
      throw new RangeError(`非法阈值区间: [${opts.min}, ${opts.max}]`);
    }
    this.min = Math.floor(opts.min);
    this.max = Math.floor(opts.max);
    this.high = opts.highWatermark ?? 0.7;
    this.low = opts.lowWatermark ?? 0.3;
    if (!(this.low < this.high)) throw new RangeError('lowWatermark 必须小于 highWatermark');
    this.step = opts.increaseStep ?? 1;
    if (!(this.step > 0)) throw new RangeError('increaseStep 必须为正');
    this.factor = opts.decreaseFactor ?? 0.5;
    if (!(this.factor > 0 && this.factor < 1)) throw new RangeError('decreaseFactor 必须在 (0,1)');
    this.cooldownMs = opts.cooldownMs ?? 100;
    this.queueScale = opts.queueDepthScale ?? 100;
    this.maxHistory = opts.maxHistory ?? 256;
    this.now = opts.now ?? Date.now;
    const initial = opts.initial ?? this.max;
    this.current = Math.min(this.max, Math.max(this.min, Math.floor(initial)));
  }

  get threshold(): number {
    return this.current;
  }

  get snapshots(): readonly ThresholdSnapshot[] {
    return this.history;
  }

  /** 综合压力评分：取各维度的最大值（最短板优先），0..1。 */
  pressureOf(m: PressureMetrics): number {
    const clamp01 = (x: number) => (Number.isFinite(x) ? Math.min(1, Math.max(0, x)) : 1);
    return Math.max(
      clamp01(m.cpuLoad),
      clamp01(m.memoryUsage),
      clamp01(m.errorRate),
      clamp01(m.queueDepth / this.queueScale),
    );
  }

  /**
   * 根据压力指标调节阈值。冷却期内直接保持（hold），避免抖动放大引发雪崩。
   */
  adjust(metrics: PressureMetrics): ThresholdSnapshot {
    const pressure = this.pressureOf(metrics);
    const at = this.now();
    let direction: ThresholdSnapshot['direction'] = 'hold';

    if (at - this.lastAdjustAt >= this.cooldownMs) {
      if (pressure > this.high) {
        // 乘性收缩：压力越大降得越快，快速退让防止雪崩
        this.current = Math.max(this.min, Math.floor(this.current * this.factor));
        direction = 'down';
        this.lastAdjustAt = at;
      } else if (pressure < this.low) {
        // 加性扩张：缓慢爬坡，最大化资源利用但不冒进
        this.current = Math.min(this.max, this.current + this.step);
        direction = 'up';
        this.lastAdjustAt = at;
      }
      // 滞回区间内保持不动，避免在临界点来回振荡
    }

    const snap: ThresholdSnapshot = { threshold: this.current, pressure, direction, at };
    this.history.push(snap);
    if (this.history.length > this.maxHistory) {
      this.history.splice(0, this.history.length - this.maxHistory);
    }
    return snap;
  }
}

export class ShedLoadError extends Error {
  constructor(queueDepth: number) {
    super(`负载卸载: 等待队列已满 (depth=${queueDepth})`);
    this.name = 'ShedLoadError';
  }
}

interface Waiter {
  resolve: () => void;
  reject: (err: Error) => void;
  onAbort?: () => void;
  signal?: AbortSignal;
}

export interface LimiterOptions {
  threshold: AdaptiveThreshold;
  /** 等待队列上限，超过即卸载。默认 1024。 */
  maxQueue?: number;
  /** 采样窗口内指标来源；默认使用 Node 进程真实指标。 */
  sampleMetrics?: (queueDepth: number) => PressureMetrics;
}

/**
 * 负载感知并发限制器（异步信号量）。
 * 每次 acquire 前根据实时指标调节阈值；并发数不超过动态阈值。
 */
export class LoadAwareLimiter {
  private readonly thresholdCtl: AdaptiveThreshold;
  private readonly maxQueue: number;
  private readonly sample: (queueDepth: number) => PressureMetrics;

  private inFlight = 0;
  private readonly waiters: Waiter[] = [];
  private completed = 0;
  private shed = 0;

  constructor(opts: LimiterOptions) {
    this.thresholdCtl = opts.threshold;
    this.maxQueue = opts.maxQueue ?? 1024;
    this.sample = opts.sampleMetrics ?? defaultMetrics;
  }

  get limit(): number {
    return this.thresholdCtl.threshold;
  }

  get active(): number {
    return this.inFlight;
  }

  get pending(): number {
    return this.waiters.length;
  }

  get stats(): { completed: number; shed: number; limit: number } {
    return { completed: this.completed, shed: this.shed, limit: this.limit };
  }

  /** 根据当前负载刷新动态阈值。 */
  private rebalance(): void {
    this.thresholdCtl.adjust(this.sample(this.waiters.length));
  }

  private drain(): void {
    while (this.inFlight < this.limit && this.waiters.length > 0) {
      const w = this.waiters.shift()!;
      if (w.onAbort && w.signal) w.signal.removeEventListener('abort', w.onAbort);
      this.inFlight++;
      w.resolve();
    }
  }

  /** 获取一个并发槽位；队列满时立即抛出 ShedLoadError（快速失败，防雪崩）。 */
  acquire(signal?: AbortSignal): Promise<void> {
    this.rebalance();
    if (signal?.aborted) {
      return Promise.reject(signal.reason ?? new Error('aborted'));
    }
    if (this.inFlight < this.limit && this.waiters.length === 0) {
      this.inFlight++;
      return Promise.resolve();
    }
    if (this.waiters.length >= this.maxQueue) {
      this.shed++;
      return Promise.reject(new ShedLoadError(this.waiters.length));
    }
    return new Promise<void>((resolve, reject) => {
      const w: Waiter = { resolve, reject, signal };
      if (signal) {
        w.onAbort = () => {
          const i = this.waiters.indexOf(w);
          if (i >= 0) this.waiters.splice(i, 1); // 从队列摘除，无泄漏
          reject(signal.reason ?? new Error('aborted'));
        };
        signal.addEventListener('abort', w.onAbort, { once: true });
      }
      this.waiters.push(w);
    });
  }

  /** 释放槽位并唤醒等待者。 */
  release(): void {
    if (this.inFlight <= 0) throw new Error('release 次数多于 acquire');
    this.inFlight--;
    this.completed++;
    this.rebalance();
    this.drain();
  }

  /** 在并发限制下执行任务，保证槽位必定释放（无泄漏）。 */
  async run<T>(fn: () => Promise<T> | T, signal?: AbortSignal): Promise<T> {
    await this.acquire(signal);
    try {
      return await fn();
    } finally {
      this.release();
    }
  }

  /** 校验内部状态已完全回收（测试/关闭时调用）。 */
  assertDrained(): void {
    if (this.inFlight !== 0 || this.waiters.length !== 0) {
      throw new Error(
        `泄漏检测失败: inFlight=${this.inFlight}, pending=${this.waiters.length}`,
      );
    }
  }
}

/** 默认指标采样：Node 进程真实负载（os.loadavg 归一化 + 堆内存占用）。 */
function defaultMetrics(queueDepth: number): PressureMetrics {
  const mem = process.memoryUsage();
  const cpus = os.cpus().length || 1;
  return {
    cpuLoad: Math.min(1, os.loadavg()[0] / cpus),
    memoryUsage: mem.heapTotal > 0 ? mem.heapUsed / mem.heapTotal : 0,
    errorRate: 0,
    queueDepth,
  };
}
