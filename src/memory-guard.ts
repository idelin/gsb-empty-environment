/**
 * 内存保护限流器（按字节计数的异步信号量）。
 *
 * 传输管线中所有“已读取但未消费”的分片都必须先 acquire 对应字节数，
 * 消费完成后 release。当在途字节数达到上限时，新的 acquire 挂起等待，
 * 从而形成背压，防止接收端/发送端内存无限增长导致 OOM。
 */
export class MemoryGuard {
  private readonly limitBytes: number;
  private inFlight = 0;
  private peak = 0;
  private waiters: Array<{ bytes: number; resolve: () => void; reject: (err: unknown) => void }> = [];
  private abortReason: unknown = null;

  constructor(limitBytes: number) {
    if (!Number.isFinite(limitBytes) || limitBytes <= 0) {
      throw new RangeError(`limitBytes 必须为正数，收到: ${limitBytes}`);
    }
    this.limitBytes = limitBytes;
  }

  /** 当前在途字节数 */
  get usage(): number {
    return this.inFlight;
  }

  /** 历史峰值在途字节数 */
  get peakUsage(): number {
    return this.peak;
  }

  get limit(): number {
    return this.limitBytes;
  }

  async acquire(bytes: number): Promise<void> {
    if (this.abortReason !== null) {
      throw this.abortReason;
    }
    // 单个分片超过上限时允许独占通过（空闲时放行），避免死锁
    const oversized = bytes > this.limitBytes;
    if (
      this.waiters.length === 0 &&
      (this.inFlight + bytes <= this.limitBytes || (oversized && this.inFlight === 0))
    ) {
      this.inFlight += bytes;
      this.peak = Math.max(this.peak, this.inFlight);
      return;
    }
    await new Promise<void>((resolve, reject) => {
      this.waiters.push({ bytes, resolve, reject });
    });
  }

  release(bytes: number): void {
    this.inFlight -= bytes;
    if (this.inFlight < 0) {
      this.inFlight = 0;
    }
    this.drain();
  }

  /**
   * 中止限流器：拒绝所有挂起的 acquire 并令后续 acquire 立即失败。
   * 用于管线任一环节出错时解除生产者的背压阻塞，防止互相等待造成死锁。
   */
  abort(reason?: unknown): void {
    if (this.abortReason !== null) {
      return;
    }
    this.abortReason = reason instanceof Error ? reason : new Error('MemoryGuard 已中止', { cause: reason });
    const pending = this.waiters.splice(0);
    for (const waiter of pending) {
      waiter.reject(this.abortReason);
    }
  }

  private drain(): void {
    while (this.waiters.length > 0) {
      const next = this.waiters[0];
      const oversized = next.bytes > this.limitBytes;
      if (this.inFlight + next.bytes > this.limitBytes && !(oversized && this.inFlight === 0)) {
        break;
      }
      this.waiters.shift();
      this.inFlight += next.bytes;
      this.peak = Math.max(this.peak, this.inFlight);
      next.resolve();
    }
  }
}
