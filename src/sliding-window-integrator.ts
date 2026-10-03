/**
 * 滑动窗口内的连续条件持续时间积分器。
 *
 * 计算流式指标在滑动窗口内满足布尔条件的累计毫秒数。
 * 核心思路：不做离散数值积分（黎曼求和），而是记录条件状态发生
 * 突变的精确时间戳，查询时对“满足状态的区间”与“当前窗口”做解析求交，
 * 因此窗口滑动把突变点移出时会精确扣减其贡献，不存在累积漂移。
 */

export type Condition = (value: number) => boolean;

interface Transition {
  /** 状态突变发生的时刻（毫秒，新状态从该时刻起生效）。 */
  timeMs: number;
  /** 突变后的状态：true 表示条件开始满足，false 表示不再满足。 */
  active: boolean;
}

export class SlidingWindowConditionIntegrator {
  private readonly windowMs: number;
  private readonly condition: Condition;

  /**
   * 基准状态：`baseTimeMs` 时刻（首个采样点或裁剪截止点）的状态。
   * 早于 baseTimeMs 的历史已被压缩进该字段，transitions 中只保留
   * baseTimeMs 之后的突变点。
   */
  private baseActive = false;
  private baseTimeMs: number | null = null;

  /** 按时间升序排列的状态突变点（均晚于 baseTimeMs）。 */
  private transitions: Transition[] = [];

  private currentActive = false;
  private lastTimeMs: number | null = null;

  constructor(windowMs: number, condition: Condition) {
    if (!Number.isFinite(windowMs) || windowMs <= 0) {
      throw new RangeError("windowMs must be a positive finite number");
    }
    this.windowMs = windowMs;
    this.condition = condition;
  }

  /**
   * 输入一个流式采样点。采样必须按时间非递减顺序到达。
   * 只有条件求值结果发生变化时才会记录突变点。
   */
  addSample(timeMs: number, value: number): void {
    if (!Number.isFinite(timeMs)) {
      throw new RangeError("timeMs must be finite");
    }
    if (this.lastTimeMs !== null && timeMs < this.lastTimeMs) {
      throw new RangeError("samples must arrive in non-decreasing time order");
    }
    const nextActive = this.condition(value);
    if (this.lastTimeMs === null) {
      // 流起点：此前没有数据，不计任何贡献。
      this.baseTimeMs = timeMs;
      this.baseActive = nextActive;
    } else if (nextActive !== this.currentActive) {
      this.transitions.push({ timeMs, active: nextActive });
    }
    this.currentActive = nextActive;
    this.lastTimeMs = timeMs;
    this.prune();
  }

  /**
   * 查询截至 nowMs、宽度为 windowMs 的滑动窗口内，
   * 条件满足的累计毫秒数。
   *
   * 注意：nowMs 不早于最近一次采样时刻时结果严格精确；
   * 更早的查询受裁剪影响，窗口起点会被钳制到已压缩的历史边界。
   */
  accumulatedMs(nowMs: number): number {
    if (this.baseTimeMs === null) return 0;
    const windowStart = Math.max(nowMs - this.windowMs, this.baseTimeMs);
    if (windowStart >= nowMs) return 0;

    let total = 0;
    let cursor = windowStart;
    let active = this.stateAt(windowStart);
    for (const transition of this.transitions) {
      if (transition.timeMs <= windowStart) continue;
      if (transition.timeMs >= nowMs) break;
      if (active) total += transition.timeMs - cursor;
      cursor = transition.timeMs;
      active = transition.active;
    }
    if (active) total += nowMs - cursor;
    return total;
  }

  /** 当前窗口宽度（毫秒）。 */
  get windowSizeMs(): number {
    return this.windowMs;
  }

  /** 保留的突变点数量（用于观察内存裁剪效果）。 */
  get retainedTransitions(): number {
    return this.transitions.length;
  }

  /**
   * 把已经滑出窗口的突变点压缩掉：它们的净效果只是
   * “窗口左边界处的状态”，用 baseActive 精确表示。
   * 这样突变点移出窗口时贡献被精确扣减，且内存有界。
   */
  private prune(): void {
    const cutoff = this.lastTimeMs! - this.windowMs;
    if (this.baseTimeMs === null || cutoff <= this.baseTimeMs) return;
    let state = this.baseActive;
    let index = 0;
    while (
      index < this.transitions.length &&
      this.transitions[index].timeMs <= cutoff
    ) {
      state = this.transitions[index].active;
      index += 1;
    }
    if (index > 0) this.transitions.splice(0, index);
    this.baseActive = state;
    this.baseTimeMs = cutoff;
  }

  private stateAt(timeMs: number): boolean {
    let active = this.baseActive;
    for (const transition of this.transitions) {
      if (transition.timeMs > timeMs) break;
      active = transition.active;
    }
    return active;
  }
}
