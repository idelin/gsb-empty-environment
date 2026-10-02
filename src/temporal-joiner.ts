/**
 * TemporalJoiner — 双流时序 Join 连接器（带否定/取消事件）。
 *
 * 语义（事件时间驱动，通过 advanceTime 推进水位线，定时器确定性地触发）：
 * - 左流事件 L 到达后，等待窗口 windowMs（默认 5000ms）内事件时间匹配的右流事件 R；
 *   匹配条件：R.ts <= L.ts + windowMs（边界含等号）。
 * - 窗口到期仍无匹配 → 输出 { kind: 'left-only' }。
 * - 超时前收到取消事件 cancel(key) → 放弃等待，不产生任何输出。
 * - 迟到的 R（L 已超时输出 left-only 后到达，但 R.ts 仍在窗口内）→ 补配：
 *   先输出 { kind: 'retract-left-only' } 撤回，再输出 { kind: 'matched', late: true }。
 * - 乱序的 R 先于 L 到达 → 缓冲至 L 到达即匹配；缓冲在 allowedLatenessMs 后过期丢弃。
 * - 所有内部状态（pending / buffered / timedOut）都有过期边界，最终清零、零泄漏。
 */

export interface StreamEvent<T = unknown> {
  /** 关联键（左右流按 key 匹配，取消事件按 key 生效） */
  key: string;
  /** 事件时间，毫秒 */
  ts: number;
  data?: T;
}

export type JoinOutput<L = unknown, R = unknown> =
  | { kind: 'matched'; key: string; left: StreamEvent<L>; right: StreamEvent<R>; late: boolean }
  | { kind: 'left-only'; key: string; left: StreamEvent<L> }
  | { kind: 'retract-left-only'; key: string; left: StreamEvent<L> };

export interface JoinerOptions<L = unknown, R = unknown> {
  /** 左流等待窗口，默认 5000ms */
  windowMs?: number;
  /** 允许迟到时长：超时左记录与乱序右缓冲的保留时长，默认等于 windowMs */
  allowedLatenessMs?: number;
  /** 输出回调（输出同时进入内部缓冲，可用 drain() 取出） */
  onOutput?: (output: JoinOutput<L, R>) => void;
}

export interface JoinerStats {
  now: number;
  pendingLefts: number;
  bufferedRights: number;
  timedOutLefts: number;
  matched: number;
  leftOnly: number;
  cancelsApplied: number;
  cancelsIgnored: number;
  duplicateLefts: number;
  droppedLateRights: number;
  expiredBufferedRights: number;
}

interface PendingLeft<L> {
  event: StreamEvent<L>;
  deadline: number;
}

interface BufferedRight<R> {
  event: StreamEvent<R>;
  expiresAt: number;
}

interface TimedOutLeft<L> {
  event: StreamEvent<L>;
  deadline: number;
  purgeAt: number;
}

export class TemporalJoiner<L = unknown, R = unknown> {
  private readonly windowMs: number;
  private readonly allowedLatenessMs: number;
  private readonly onOutput?: (output: JoinOutput<L, R>) => void;

  private now = Number.NEGATIVE_INFINITY;
  private readonly pendingLefts = new Map<string, PendingLeft<L>>();
  private readonly bufferedRights = new Map<string, BufferedRight<R>[]>();
  private readonly timedOutLefts = new Map<string, TimedOutLeft<L>>();
  private readonly buffer: Array<JoinOutput<L, R>> = [];

  private matched = 0;
  private leftOnly = 0;
  private cancelsApplied = 0;
  private cancelsIgnored = 0;
  private duplicateLefts = 0;
  private droppedLateRights = 0;
  private expiredBufferedRights = 0;

  constructor(options: JoinerOptions<L, R> = {}) {
    this.windowMs = options.windowMs ?? 5000;
    this.allowedLatenessMs = options.allowedLatenessMs ?? this.windowMs;
    this.onOutput = options.onOutput;
  }

  /** 左流事件：开启等待窗口；若已有缓冲的乱序 R 则立即匹配 */
  left(event: StreamEvent<L>): void {
    if (this.pendingLefts.has(event.key)) {
      this.duplicateLefts++;
      return;
    }
    const deadline = event.ts + this.windowMs;

    const candidates = this.bufferedRights.get(event.key);
    const hit = candidates?.findIndex((c) => c.event.ts <= deadline) ?? -1;
    if (candidates && hit >= 0) {
      const [right] = candidates.splice(hit, 1);
      if (candidates.length === 0) this.bufferedRights.delete(event.key);
      this.matched++;
      this.emit({ kind: 'matched', key: event.key, left: event, right: right.event, late: true });
      return;
    }

    this.pendingLefts.set(event.key, { event, deadline });
    if (deadline <= this.now) this.expireLefts(); // 左事件本身迟到，窗口立即到期
  }

  /** 右流事件：优先匹配等待中的 L；其次对已超时的 L 补配；否则缓冲等待迟到的 L */
  right(event: StreamEvent<R>): void {
    const pending = this.pendingLefts.get(event.key);
    if (pending && event.ts <= pending.deadline) {
      this.pendingLefts.delete(event.key);
      this.matched++;
      this.emit({ kind: 'matched', key: event.key, left: pending.event, right: event, late: false });
      return;
    }

    const timedOut = this.timedOutLefts.get(event.key);
    if (timedOut && event.ts <= timedOut.deadline) {
      this.timedOutLefts.delete(event.key);
      this.emit({ kind: 'retract-left-only', key: event.key, left: timedOut.event });
      this.matched++;
      this.emit({ kind: 'matched', key: event.key, left: timedOut.event, right: event, late: true });
      return;
    }

    const expiresAt = event.ts + this.allowedLatenessMs;
    if (expiresAt <= this.now) {
      this.droppedLateRights++;
      return;
    }
    const list = this.bufferedRights.get(event.key) ?? [];
    list.push({ event, expiresAt });
    this.bufferedRights.set(event.key, list);
  }

  /** 否定/取消事件：放弃等待，清除该 key 的全部残余状态，不产生任何输出 */
  cancel(key: string): void {
    const applied =
      this.pendingLefts.delete(key) ||
      this.timedOutLefts.delete(key) ||
      this.bufferedRights.delete(key);
    if (applied) this.cancelsApplied++;
    else this.cancelsIgnored++;
  }

  /** 推进事件时间水位线，触发所有到期定时器并清理过期状态 */
  advanceTime(t: number): void {
    if (t > this.now) this.now = t;
    this.expireLefts();

    for (const [key, entry] of this.timedOutLefts) {
      if (entry.purgeAt <= this.now) this.timedOutLefts.delete(key);
    }
    for (const [key, list] of this.bufferedRights) {
      const kept = list.filter((r) => r.expiresAt > this.now);
      this.expiredBufferedRights += list.length - kept.length;
      if (kept.length === 0) this.bufferedRights.delete(key);
      else this.bufferedRights.set(key, kept);
    }
  }

  /** 取出并清空已缓冲的输出 */
  drain(): Array<JoinOutput<L, R>> {
    return this.buffer.splice(0, this.buffer.length);
  }

  stats(): JoinerStats {
    let buffered = 0;
    for (const list of this.bufferedRights.values()) buffered += list.length;
    return {
      now: this.now,
      pendingLefts: this.pendingLefts.size,
      bufferedRights: buffered,
      timedOutLefts: this.timedOutLefts.size,
      matched: this.matched,
      leftOnly: this.leftOnly,
      cancelsApplied: this.cancelsApplied,
      cancelsIgnored: this.cancelsIgnored,
      duplicateLefts: this.duplicateLefts,
      droppedLateRights: this.droppedLateRights,
      expiredBufferedRights: this.expiredBufferedRights,
    };
  }

  private expireLefts(): void {
    const expired = [...this.pendingLefts.entries()]
      .filter(([, p]) => p.deadline <= this.now)
      .sort((a, b) => a[1].deadline - b[1].deadline);
    for (const [key, p] of expired) {
      this.pendingLefts.delete(key);
      this.timedOutLefts.set(key, {
        event: p.event,
        deadline: p.deadline,
        purgeAt: p.deadline + this.allowedLatenessMs,
      });
      this.leftOnly++;
      this.emit({ kind: 'left-only', key, left: p.event });
    }
  }

  private emit(output: JoinOutput<L, R>): void {
    this.buffer.push(output);
    this.onOutput?.(output);
  }
}
