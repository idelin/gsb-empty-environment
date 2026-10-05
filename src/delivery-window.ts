/**
 * 通知投递库：有限滑动窗口 + 连续水位 + 代次(epoch)防旧确认 + 失败/超时重投。
 *
 * 语义：
 * - 接收方可并发处理，完成事件可乱序到达；水位只越过连续完成的序号。
 * - 每个序号的一次投递携带一个代次；重投时代次递增。
 *   只有 (seq, epoch) 与当前在途记录精确匹配的确认才生效，
 *   因此旧代次确认无法确认新投递，重复确认无副作用。
 * - 窗口有界：在途序号全部落在 (watermark, watermark + windowSize]，
 *   窗口满时拒绝继续投递（背压），不会越界。
 * - 失败(fail)与超时(timeout)都会产生重投项，输出在 redeliveries 中。
 */

export type DeliveryEvent =
  | { type: 'deliver'; at: number }
  | { type: 'complete'; seq: number; epoch: number }
  | { type: 'fail'; seq: number; epoch: number; at: number }
  | { type: 'timeout'; at: number };

export interface Redelivery {
  seq: number;
  epoch: number;
}

export interface ApplyResult {
  /** 应用事件后的水位（已连续完成的最大序号）。 */
  watermark: number;
  /** 本次事件产生的重投列表（可能为空）。 */
  redeliveries: Redelivery[];
  /** deliver 事件成功投递时返回其序号与代次；窗口满被拒绝时为 null。 */
  delivered: Redelivery | null;
  /** 事件被忽略的原因（重复确认、代次不匹配、序号未知等），便于观测。 */
  ignored: string | null;
}

export interface Snapshot {
  watermark: number;
  /** 已分配过的最大序号。 */
  maxIssued: number;
  /** 在途（已投递未确认）序号数。 */
  inFlight: number;
  /** 在途序号明细。 */
  outstanding: Redelivery[];
}

interface OutstandingEntry {
  epoch: number;
  deadline: number;
}

export interface DeliveryWindowOptions {
  /** 滑动窗口大小（最大在途数），必须为正整数。 */
  windowSize: number;
  /** 投递超时毫秒数，超时后需重投。 */
  timeoutMs: number;
  /** 起始水位，序号从 watermark + 1 开始分配，默认 0。 */
  initialWatermark?: number;
}

export class DeliveryWindow {
  private readonly windowSize: number;
  private readonly timeoutMs: number;
  private watermark: number;
  private maxIssued: number;
  private readonly outstanding = new Map<number, OutstandingEntry>();

  constructor(options: DeliveryWindowOptions) {
    if (!Number.isInteger(options.windowSize) || options.windowSize <= 0) {
      throw new Error('windowSize must be a positive integer');
    }
    if (!(options.timeoutMs > 0)) {
      throw new Error('timeoutMs must be positive');
    }
    this.windowSize = options.windowSize;
    this.timeoutMs = options.timeoutMs;
    this.watermark = options.initialWatermark ?? 0;
    this.maxIssued = this.watermark;
  }

  getWatermark(): number {
    return this.watermark;
  }

  /** 应用单个事件，返回水位与重投列表。 */
  apply(event: DeliveryEvent): ApplyResult {
    switch (event.type) {
      case 'deliver':
        return this.deliver(event.at);
      case 'complete':
        return this.complete(event.seq, event.epoch);
      case 'fail':
        return this.fail(event.seq, event.epoch, event.at);
      case 'timeout':
        return this.timeout(event.at);
    }
  }

  /** 依次应用事件流，返回每步结果。 */
  run(events: DeliveryEvent[]): ApplyResult[] {
    return events.map((event) => this.apply(event));
  }

  snapshot(): Snapshot {
    return {
      watermark: this.watermark,
      maxIssued: this.maxIssued,
      inFlight: this.outstanding.size,
      outstanding: [...this.outstanding.entries()]
        .map(([seq, entry]) => ({ seq, epoch: entry.epoch }))
        .sort((a, b) => a.seq - b.seq),
    };
  }

  /**
   * 校验窗口不变量：
   * 1. 在途数不超过窗口大小；
   * 2. 所有在途序号落在 (watermark, watermark + windowSize]；
   * 3. 已分配序号不超过水位 + 窗口大小（不会越界）。
   */
  checkInvariant(): void {
    const upper = this.watermark + this.windowSize;
    if (this.outstanding.size > this.windowSize) {
      throw new Error(
        `invariant violated: inFlight=${this.outstanding.size} > windowSize=${this.windowSize}`,
      );
    }
    for (const seq of this.outstanding.keys()) {
      if (seq <= this.watermark || seq > upper) {
        throw new Error(
          `invariant violated: outstanding seq ${seq} outside (${this.watermark}, ${upper}]`,
        );
      }
    }
    if (this.maxIssued > upper) {
      throw new Error(
        `invariant violated: maxIssued=${this.maxIssued} beyond window upper bound ${upper}`,
      );
    }
  }

  private result(partial: Partial<ApplyResult> = {}): ApplyResult {
    return {
      watermark: this.watermark,
      redeliveries: [],
      delivered: null,
      ignored: null,
      ...partial,
    };
  }

  private deliver(at: number): ApplyResult {
    const seq = this.maxIssued + 1;
    if (seq > this.watermark + this.windowSize) {
      return this.result({ ignored: 'window-full' });
    }
    this.maxIssued = seq;
    this.outstanding.set(seq, { epoch: 1, deadline: at + this.timeoutMs });
    return this.result({ delivered: { seq, epoch: 1 } });
  }

  private complete(seq: number, epoch: number): ApplyResult {
    if (seq <= this.watermark) {
      return this.result({ ignored: 'already-confirmed' });
    }
    const entry = this.outstanding.get(seq);
    if (entry === undefined) {
      return this.result({ ignored: 'unknown-seq' });
    }
    if (entry.epoch !== epoch) {
      return this.result({ ignored: 'stale-epoch' });
    }
    this.outstanding.delete(seq);
    this.advanceWatermark();
    return this.result();
  }

  private fail(seq: number, epoch: number, at: number): ApplyResult {
    if (seq <= this.watermark) {
      return this.result({ ignored: 'already-confirmed' });
    }
    const entry = this.outstanding.get(seq);
    if (entry === undefined) {
      return this.result({ ignored: 'unknown-seq' });
    }
    if (entry.epoch !== epoch) {
      return this.result({ ignored: 'stale-epoch' });
    }
    const redelivery = this.redeliver(seq, entry, at);
    return this.result({ redeliveries: [redelivery] });
  }

  private timeout(at: number): ApplyResult {
    const redeliveries: Redelivery[] = [];
    const expired = [...this.outstanding.entries()]
      .filter(([, entry]) => entry.deadline <= at)
      .sort((a, b) => a[0] - b[0]);
    for (const [seq, entry] of expired) {
      redeliveries.push(this.redeliver(seq, entry, at));
    }
    return this.result({ redeliveries });
  }

  /** 重投：代次递增并重置截止时间，旧代次确认随之失效。 */
  private redeliver(seq: number, entry: OutstandingEntry, at: number): Redelivery {
    entry.epoch += 1;
    entry.deadline = at + this.timeoutMs;
    return { seq, epoch: entry.epoch };
  }

  /** 水位只越过连续完成的序号。 */
  private advanceWatermark(): void {
    let next = this.watermark + 1;
    while (next <= this.maxIssued && !this.outstanding.has(next)) {
      this.watermark = next;
      next += 1;
    }
  }
}
