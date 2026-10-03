/**
 * 会话超时可撤销合并窗口（Session Window Merger with Retraction）
 *
 * 语义：
 * - 按事件时间将相邻间隔不超过 gap 的事件合并为会话窗口 [start, end]。
 * - 迟到事件可能连接原本独立的两个窗口，触发窗口合并。
 * - 事件被删除后，窗口可能重新分裂。
 * - 窗口集合的任何变化都以 retract/upsert 变更流形式输出：
 *   消失的旧窗口发 retract，新出现的窗口发 upsert。
 * - 水位线推进后，超出最大延迟范围（不可能再被任何合法迟到事件触及）
 *   的状态被丢弃；过于迟到的事件直接丢弃。
 */

export interface SessionWindow {
  /** 窗口起始（含），等于窗口内最小事件时间 */
  start: number;
  /** 窗口结束（含），等于窗口内最大事件时间 */
  end: number;
  /** 窗口内当前事件数 */
  count: number;
}

export type ChangeKind = 'upsert' | 'retract';

export interface WindowChange {
  kind: ChangeKind;
  window: SessionWindow;
}

export interface SessionWindowMergerOptions {
  /** 会话间隔：相邻事件时间差 <= gap 时并入同一窗口 */
  gap: number;
  /** 最大允许迟到：事件时间 < watermark - maxLateness 的事件被丢弃 */
  maxLateness: number;
}

export class SessionWindowMerger {
  private readonly gap: number;
  private readonly maxLateness: number;
  /** 活跃事件状态：eventId -> eventTime */
  private readonly events = new Map<string, number>();
  /** 当前已发射的窗口集合（按 start 升序） */
  private emitted: SessionWindow[] = [];
  private watermark = Number.NEGATIVE_INFINITY;

  constructor(options: SessionWindowMergerOptions) {
    if (options.gap < 0) throw new Error('gap must be >= 0');
    if (options.maxLateness < 0) throw new Error('maxLateness must be >= 0');
    this.gap = options.gap;
    this.maxLateness = options.maxLateness;
  }

  /**
   * 添加事件，返回窗口变更流（retract 在前，upsert 在后）。
   * 事件时间超出最大延迟范围时被丢弃，返回空数组。
   * 相同 id 重复添加视为更新该事件的时间。
   */
  addEvent(id: string, timestamp: number): WindowChange[] {
    if (timestamp < this.watermark - this.maxLateness) {
      return []; // 过于迟到，丢弃
    }
    this.events.set(id, timestamp);
    return this.recompute();
  }

  /** 删除事件，返回窗口变更流；未知 id 返回空数组。 */
  removeEvent(id: string): WindowChange[] {
    if (!this.events.delete(id)) return [];
    return this.recompute();
  }

  /**
   * 推进水位线，并丢弃超出最大延迟范围的状态。
   *
   * 任何未来被接受的事件都满足 t >= watermark - maxLateness，
   * 因此 end < watermark - maxLateness - gap 的窗口不可能再被合并或触及，
   * 其窗口与事件状态一并清除。被清除的窗口视为已最终确定，
   * 不再发送 retract。返回被丢弃的窗口（仅用于可观测性）。
   */
  advanceWatermark(watermark: number): SessionWindow[] {
    if (watermark <= this.watermark) return [];
    this.watermark = watermark;
    const horizon = watermark - this.maxLateness - this.gap;

    const survivors: SessionWindow[] = [];
    const discarded: SessionWindow[] = [];
    for (const w of this.emitted) {
      (w.end < horizon ? discarded : survivors).push(w);
    }
    if (discarded.length === 0) return [];

    this.emitted = survivors;
    const minSurvivingStart = survivors.length > 0
      ? survivors[0].start
      : Number.POSITIVE_INFINITY;
    for (const [id, t] of this.events) {
      if (t < horizon && t < minSurvivingStart) this.events.delete(id);
    }
    return discarded;
  }

  /** 当前窗口集合快照（按 start 升序）。 */
  getWindows(): SessionWindow[] {
    return this.emitted.map((w) => ({ ...w }));
  }

  /** 当前持有的活跃事件数（用于验证状态清理）。 */
  get stateSize(): number {
    return this.events.size;
  }

  get currentWatermark(): number {
    return this.watermark;
  }

  /** 全量重算窗口并与已发射集合 diff，生成 retract/upsert 变更流。 */
  private recompute(): WindowChange[] {
    const next = this.computeWindows();
    const changes = diffWindows(this.emitted, next);
    this.emitted = next;
    return changes;
  }

  private computeWindows(): SessionWindow[] {
    const times = [...this.events.values()].sort((a, b) => a - b);
    const windows: SessionWindow[] = [];
    for (const t of times) {
      const last = windows[windows.length - 1];
      if (last && t - last.end <= this.gap) {
        last.end = t;
        last.count += 1;
      } else {
        windows.push({ start: t, end: t, count: 1 });
      }
    }
    return windows;
  }
}

function windowKey(w: SessionWindow): string {
  return `${w.start}:${w.end}`;
}

/**
 * 新旧窗口集合 diff：
 * - 消失的窗口 -> retract
 * - 新出现的窗口 -> upsert
 * - 边界不变但内容（count）变化的窗口 -> upsert（同键更新，无需 retract）
 */
export function diffWindows(
  previous: SessionWindow[],
  next: SessionWindow[],
): WindowChange[] {
  const changes: WindowChange[] = [];
  const nextKeys = new Map(next.map((w) => [windowKey(w), w]));
  const prevKeys = new Map(previous.map((w) => [windowKey(w), w]));
  for (const w of previous) {
    if (!nextKeys.has(windowKey(w))) {
      changes.push({ kind: 'retract', window: { ...w } });
    }
  }
  for (const w of next) {
    const before = prevKeys.get(windowKey(w));
    if (!before || before.count !== w.count) {
      changes.push({ kind: 'upsert', window: { ...w } });
    }
  }
  return changes;
}
