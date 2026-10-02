/**
 * 滑动窗口峰值重叠区间检测器。
 *
 * 维护一组带 id 的半开区间 [start, end)，支持乱序到达（结束时间先于
 * 开始时间补录）、迟到补录与撤回；每次变更后可动态查询并发峰值及
 * 达到峰值的全部连续时间段。
 *
 * 区间语义为左闭右开：[0, 5) 与 [5, 9) 在端点 5 处不重叠，并发为 1。
 */

export interface Interval {
  readonly start: number;
  readonly end: number;
}

export interface Segment {
  readonly start: number;
  readonly end: number;
}

export interface PeakSnapshot {
  /** 当前并发峰值（空检测器为 0）。 */
  readonly peak: number;
  /**
   * 达到峰值的全部极大连续时间段（相邻同峰值原子段已合并），
   * 按开始时间升序。
   */
  readonly segments: readonly Segment[];
  /**
   * 达到峰值的原子时间段（以所有区间端点为切分点的最小分段），
   * 按开始时间升序。
   */
  readonly atomicSegments: readonly Segment[];
}

export class PeakOverlapDetector {
  private readonly intervals = new Map<string, Interval>();

  /** 当前在册区间数量。 */
  get size(): number {
    return this.intervals.size;
  }

  has(id: string): boolean {
    return this.intervals.has(id);
  }

  /**
   * 登记（或按 id 覆盖）一条占用区间 [start, end)，返回最新峰值快照。
   * 允许以任意顺序调用，迟到补录与先到的区间同等参与统计。
   */
  add(id: string, start: number, end: number): PeakSnapshot {
    if (!Number.isFinite(start) || !Number.isFinite(end)) {
      throw new RangeError(`interval bounds must be finite numbers, got [${start}, ${end})`);
    }
    if (end <= start) {
      throw new RangeError(`interval end must be greater than start, got [${start}, ${end})`);
    }
    this.intervals.set(id, { start, end });
    return this.snapshot();
  }

  /** 撤回一条区间（不存在时静默忽略），返回最新峰值快照。 */
  remove(id: string): PeakSnapshot {
    this.intervals.delete(id);
    return this.snapshot();
  }

  /** 清空全部区间，返回最新峰值快照。 */
  clear(): PeakSnapshot {
    this.intervals.clear();
    return this.snapshot();
  }

  /**
   * 计算当前峰值快照。
   *
   * 扫描线法：取所有区间端点排序，相邻端点构成原子段；半开区间
   * [s, e) 覆盖原子段 [a, b) 当且仅当 s <= a 且 b <= e，端点恰好
   * 相接的区间不会互相多计。
   */
  snapshot(): PeakSnapshot {
    if (this.intervals.size === 0) {
      return { peak: 0, segments: [], atomicSegments: [] };
    }

    const points = new Set<number>();
    for (const iv of this.intervals.values()) {
      points.add(iv.start);
      points.add(iv.end);
    }
    const sorted = [...points].sort((a, b) => a - b);

    let peak = 0;
    const atomic: Segment[] = [];
    for (let i = 0; i < sorted.length - 1; i += 1) {
      const a = sorted[i];
      const b = sorted[i + 1];
      let count = 0;
      for (const iv of this.intervals.values()) {
        if (iv.start <= a && b <= iv.end) count += 1;
      }
      if (count > peak) {
        peak = count;
        atomic.length = 0;
        atomic.push({ start: a, end: b });
      } else if (count === peak) {
        atomic.push({ start: a, end: b });
      }
    }

    const segments: Segment[] = [];
    for (const seg of atomic) {
      const last = segments[segments.length - 1];
      if (last && last.end === seg.start) {
        segments[segments.length - 1] = { start: last.start, end: seg.end };
      } else {
        segments.push({ start: seg.start, end: seg.end });
      }
    }

    return { peak, segments, atomicSegments: atomic };
  }
}
