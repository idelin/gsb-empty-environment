/**
 * 双流时序 Join 组件（带否定/取消事件）
 *
 * 语义：
 * - 左流事件 L 到达后，在事件时间窗口 [L.ts, L.ts + windowMs] 内等待右流匹配事件 R。
 * - 窗口内 R 到达（或 R 已乱序提前到达）→ 立即输出 matched。
 * - 窗口内收到该 key 的取消事件 → 放弃等待，不输出任何结果。
 * - 水位线越过 L 的截止时间仍未匹配 → 输出 left-only（孤立结果）。
 * - 迟到的 R（事件时间落在已超时 L 的窗口内）→ 输出 late-matched 补配修正。
 *
 * 时间模型：事件时间 + 水位线（watermark）。调用方通过 advanceWatermark(ts)
 * 推进时钟，组件内部不依赖真实墙钟，因此可确定性测试。
 */

export interface LeftEvent {
  kind: 'left';
  key: string;
  ts: number;
  payload?: unknown;
}

export interface RightEvent {
  kind: 'right';
  key: string;
  ts: number;
  payload?: unknown;
}

export interface CancelEvent {
  kind: 'cancel';
  key: string;
  ts: number;
}

export type StreamEvent = LeftEvent | RightEvent | CancelEvent;

export type JoinOutput =
  | { type: 'matched'; key: string; left: LeftEvent; right: RightEvent }
  | { type: 'left-only'; key: string; left: LeftEvent }
  | { type: 'late-matched'; key: string; left: LeftEvent; right: RightEvent };

interface PendingLeft {
  event: LeftEvent;
  /** 截止时间（含）：R.ts <= deadline 视为窗口内 */
  deadline: number;
}

export class TemporalJoiner {
  private readonly windowMs: number;
  /** 允许的最大乱序/迟到时长，决定内部状态的保留期 */
  private readonly allowedLatenessMs: number;

  /** 等待匹配的左流事件 */
  private pendingLefts = new Map<string, PendingLeft>();
  /** 乱序提前到达、尚未被消费的右流事件 */
  private pendingRights = new Map<string, RightEvent>();
  /** 取消墓碑：key -> 取消事件时间（处理 cancel 先于 L 到达的乱序） */
  private cancelTombstones = new Map<string, number>();
  /** 已超时输出 left-only 的左事件，保留用于迟到 R 的补配 */
  private expiredLefts = new Map<string, PendingLeft>();

  private watermark = Number.NEGATIVE_INFINITY;

  constructor(options: { windowMs?: number; allowedLatenessMs?: number } = {}) {
    this.windowMs = options.windowMs ?? 5000;
    this.allowedLatenessMs = options.allowedLatenessMs ?? this.windowMs;
  }

  /** 处理一个输入事件，返回本次产生的输出（可能为空） */
  process(event: StreamEvent): JoinOutput[] {
    switch (event.kind) {
      case 'left':
        return this.onLeft(event);
      case 'right':
        return this.onRight(event);
      case 'cancel':
        return this.onCancel(event);
    }
  }

  /**
   * 推进水位线：触发超时孤立输出，并回收不再可能被引用的内部状态。
   * 返回因超时产生的 left-only 输出。
   */
  advanceWatermark(ts: number): JoinOutput[] {
    if (ts <= this.watermark) return [];
    this.watermark = ts;
    const outputs: JoinOutput[] = [];

    for (const [key, pending] of this.pendingLefts) {
      if (pending.deadline <= ts) {
        this.pendingLefts.delete(key);
        this.expiredLefts.set(key, pending);
        outputs.push({ type: 'left-only', key, left: pending.event });
      }
    }

    // 状态回收：超过保留期的状态不可能再被任何迟到事件引用
    const horizon = ts - this.allowedLatenessMs;
    for (const [key, right] of this.pendingRights) {
      if (right.ts <= horizon) this.pendingRights.delete(key);
    }
    for (const [key, cancelTs] of this.cancelTombstones) {
      if (cancelTs <= horizon) this.cancelTombstones.delete(key);
    }
    for (const [key, pending] of this.expiredLefts) {
      if (pending.deadline <= horizon) this.expiredLefts.delete(key);
    }

    return outputs;
  }

  /** 内部状态总量（用于泄漏断言：所有事件处理完毕且水位线足够远时应为 0） */
  getStateSize(): number {
    return (
      this.pendingLefts.size +
      this.pendingRights.size +
      this.cancelTombstones.size +
      this.expiredLefts.size
    );
  }

  private onLeft(event: LeftEvent): JoinOutput[] {
    // 乱序取消：取消事件先于 L 到达，且取消时间不早于 L → 直接丢弃
    const cancelTs = this.cancelTombstones.get(event.key);
    if (cancelTs !== undefined && cancelTs >= event.ts) {
      this.cancelTombstones.delete(event.key);
      return [];
    }

    // 乱序右流：R 已提前到达且落在窗口内 → 立即配对
    const right = this.pendingRights.get(event.key);
    if (right !== undefined && right.ts >= event.ts && right.ts <= event.ts + this.windowMs) {
      this.pendingRights.delete(event.key);
      return [{ type: 'matched', key: event.key, left: event, right }];
    }

    this.pendingLefts.set(event.key, {
      event,
      deadline: event.ts + this.windowMs,
    });
    return [];
  }

  private onRight(event: RightEvent): JoinOutput[] {
    // 正常路径：L 在等待且 R 落在窗口内 → 立即配对
    const pending = this.pendingLefts.get(event.key);
    if (pending !== undefined && event.ts >= pending.event.ts && event.ts <= pending.deadline) {
      this.pendingLefts.delete(event.key);
      return [{ type: 'matched', key: event.key, left: pending.event, right: event }];
    }

    // 迟到补配：L 已超时输出 left-only，但 R 的事件时间实际落在窗口内 → 修正输出
    const expired = this.expiredLefts.get(event.key);
    if (expired !== undefined && event.ts >= expired.event.ts && event.ts <= expired.deadline) {
      this.expiredLefts.delete(event.key);
      return [{ type: 'late-matched', key: event.key, left: expired.event, right: event }];
    }

    // 可能是乱序提前到达的 R（L 尚未到达），缓存等待
    this.pendingRights.set(event.key, event);
    return [];
  }

  private onCancel(event: CancelEvent): JoinOutput[] {
    if (this.pendingLefts.delete(event.key)) {
      // 取消等待中的 L：放弃等待，不输出任何结果
      return [];
    }
    // L 尚未到达（乱序）：记录墓碑，后续到达的 L 若不比取消更新则被丢弃
    this.cancelTombstones.set(event.key, event.ts);
    return [];
  }
}
