/**
 * monitor.ts —— 内存监控器。
 *
 *  - 采样 process.memoryUsage()，写入建立期预分配的采样缓冲（自身零分配）；
 *  - 通过 perf_hooks 的 GC PerformanceObserver 统计 GC 次数与停顿时长；
 *  - 结合 ChunkPool.heapAllocations 给出"稳态零分配"的判定。
 *
 * 注意：PerformanceObserver 的回调在事件循环后续 tick 异步投递，
 * 读取 GC 统计前请先 `await flush()`。
 */

import { PerformanceObserver } from "node:perf_hooks";

/** 每次采样的字段数：heapUsed / heapTotal / external / rss。 */
const FIELDS = 4;

export class MemoryMonitor {
  /** 采样缓冲（构造期一次性预分配，采样过程零对象创建）。 */
  private readonly buf: Float64Array;
  private readonly maxSamples: number;
  private sampleCount = 0;
  private gcCount = 0;
  private gcPauseMs = 0;
  private readonly observer: PerformanceObserver;

  constructor(maxSamples = 1024) {
    this.maxSamples = maxSamples;
    this.buf = new Float64Array(maxSamples * FIELDS);
    this.observer = new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        this.gcCount++;
        this.gcPauseMs += entry.duration;
      }
    });
  }

  /** 开始观测 GC 事件。 */
  start(): void {
    this.observer.observe({ entryTypes: ["gc"], buffered: false });
  }

  stop(): void {
    this.observer.disconnect();
  }

  /** 等待挂起的 GC 通知异步投递完成（读取统计前调用）。 */
  async flush(): Promise<void> {
    await new Promise<void>((resolve) => setImmediate(resolve));
    await new Promise<void>((resolve) => setImmediate(resolve));
  }

  /** 采集一次内存快照，写入预分配缓冲。 */
  sample(): void {
    if (this.sampleCount >= this.maxSamples) return;
    const mu = process.memoryUsage();
    const o = this.sampleCount * FIELDS;
    this.buf[o] = mu.heapUsed;
    this.buf[o + 1] = mu.heapTotal;
    this.buf[o + 2] = mu.external;
    this.buf[o + 3] = mu.rss;
    this.sampleCount++;
  }

  get length(): number {
    return this.sampleCount;
  }

  heapUsedAt(index: number): number {
    return this.buf[index * FIELDS];
  }

  heapTotalAt(index: number): number {
    return this.buf[index * FIELDS + 1];
  }

  rssAt(index: number): number {
    return this.buf[index * FIELDS + 3];
  }

  get gcEvents(): number {
    return this.gcCount;
  }

  get gcTotalPauseMs(): number {
    return this.gcPauseMs;
  }

  /**
   * 稳态区间 [fromIndex, sampleCount) 内 heapUsed 的最大振幅（字节）。
   * 曲线平直 <=> 振幅趋近于 0。
   */
  heapAmplitude(fromIndex: number): number {
    let min = Infinity;
    let max = -Infinity;
    for (let i = fromIndex; i < this.sampleCount; i++) {
      const h = this.buf[i * FIELDS];
      if (h < min) min = h;
      if (h > max) max = h;
    }
    return this.sampleCount <= fromIndex ? 0 : max - min;
  }
}
