import type { Clock, FrameId } from "./types.ts";

const DEFAULT_TTL_MS = 5000;

/**
 * 瞬时用户激活（transient user activation）。
 *
 * 每次用户手势为某个 frame 刷新一个带过期时间的激活戳；
 * 导航（包括顶层换站）会清空激活，激活过期后 requestStorageAccess 必须拒绝。
 */
export class TransientActivationTracker {
  private readonly timestamps = new Map<FrameId, number>();
  private readonly ttlMs: number;
  private readonly clock: Clock;

  constructor(clock: Clock, ttlMs: number = DEFAULT_TTL_MS) {
    this.clock = clock;
    this.ttlMs = ttlMs;
  }

  /** 记录一次发生在 frame 上的用户手势（点击/触摸/按键等）。 */
  notifyUserGesture(frameId: FrameId, at: number = this.clock.now()): void {
    this.timestamps.set(frameId, at);
  }

  isActive(frameId: FrameId, now: number = this.clock.now()): boolean {
    const since = this.timestamps.get(frameId);
    if (since === undefined) {
      return false;
    }
    return now - since < this.ttlMs;
  }

  /** 消费式检查：规范中激活只能支撑一次受保护操作，用后即失效。 */
  consume(frameId: FrameId, now: number = this.clock.now()): boolean {
    if (!this.isActive(frameId, now)) {
      return false;
    }
    this.timestamps.delete(frameId);
    return true;
  }

  clear(frameId: FrameId): void {
    this.timestamps.delete(frameId);
  }

  clearAll(): void {
    this.timestamps.clear();
  }
}
