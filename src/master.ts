import type { Clock } from "./clock.ts";
import { SystemClock } from "./clock.ts";
import type { HeartbeatRequest, HeartbeatResponse } from "./transport.ts";

export interface Reclaim {
  from: string;
  to: string;
  tokens: number;
  at: number;
}

export interface MasterOptions {
  /** 全集群限流池总配额（每窗口，如 3000 QPS） */
  pool: number;
  /** 滑动窗口长度（毫秒） */
  windowMs: number;
  clock?: Clock;
  logger?: (line: string) => void;
}

interface WorkerState {
  /** 本窗口已预分配给该 Worker 的配额 */
  allocated: number;
  /** 该 Worker 最近一次心跳上报的累计用量 */
  used: number;
  lastSeen: number;
}

/**
 * 中心协调器：滑动窗口 + 租约式配额预分配。
 *
 * - 每个窗口开始时配额池重置为 pool；
 * - Worker 通过批量心跳申请配额，Master 从空闲池划拨；
 * - 空闲池不足时，按“用量最少者优先”从其他 Worker 未使用的
 *   预分配额度中回收（调拨），再授予请求方；
 * - 租约有效期 = 当前窗口结束时间，窗口滑动后所有租约自动失效。
 */
export class MasterCoordinator {
  readonly pool: number;
  readonly windowMs: number;
  readonly reclaims: Reclaim[] = [];

  private readonly clock: Clock;
  private readonly log?: (line: string) => void;
  private windowStart: number;
  private readonly workers = new Map<string, WorkerState>();

  constructor(opts: MasterOptions) {
    this.pool = opts.pool;
    this.windowMs = opts.windowMs;
    this.clock = opts.clock ?? new SystemClock();
    this.log = opts.logger;
    this.windowStart = this.clock.now();
  }

  get window(): number {
    return this.windowStart;
  }

  get totalAllocated(): number {
    let sum = 0;
    for (const w of this.workers.values()) sum += w.allocated;
    return sum;
  }

  get poolFree(): number {
    return this.pool - this.totalAllocated;
  }

  allocatedOf(workerId: string): number {
    return this.workers.get(workerId)?.allocated ?? 0;
  }

  usedOf(workerId: string): number {
    return this.workers.get(workerId)?.used ?? 0;
  }

  private rotateIfNeeded(now: number): void {
    if (now < this.windowStart + this.windowMs) return;
    const skipped = Math.floor((now - this.windowStart) / this.windowMs);
    this.windowStart += skipped * this.windowMs;
    this.workers.clear();
    this.log?.(
      `[master] 窗口滑动 -> [${this.windowStart}, ${this.windowStart + this.windowMs})，配额池重置为 ${this.pool}`,
    );
  }

  handleHeartbeat(req: HeartbeatRequest): HeartbeatResponse {
    const now = this.clock.now();
    this.rotateIfNeeded(now);

    let state = this.workers.get(req.workerId);
    if (!state) {
      state = { allocated: 0, used: 0, lastSeen: now };
      this.workers.set(req.workerId, state);
    }
    state.used = req.usedTotal;
    state.lastSeen = now;

    let grant = Math.min(req.requested, this.poolFree);
    let deficit = req.requested - grant;

    if (deficit > 0) {
      // 空闲池不足：从其他 Worker 未使用的预分配额度中调拨，用量最少者（最空闲）优先
      const candidates = [...this.workers.entries()]
        .filter(([id]) => id !== req.workerId)
        .map(([id, w]) => ({ id, used: w.used, unused: Math.max(0, w.allocated - w.used) }))
        .filter((c) => c.unused > 0)
        .sort((a, b) => a.used - b.used || b.unused - a.unused);

      for (const c of candidates) {
        if (deficit <= 0) break;
        const take = Math.min(c.unused, deficit);
        const victim = this.workers.get(c.id)!;
        victim.allocated -= take;
        deficit -= take;
        grant += take;
        this.reclaims.push({ from: c.id, to: req.workerId, tokens: take, at: now });
        this.log?.(
          `[master] 调拨 ${take} tokens: ${c.id} (空闲, 已用=${c.used}) -> ${req.workerId}`,
        );
      }
    }

    state.allocated += grant;
    if (grant > 0) {
      this.log?.(
        `[master] 授予 ${grant} tokens -> ${req.workerId} (持有=${state.allocated}, 池剩余=${this.poolFree})`,
      );
    } else if (req.requested > 0) {
      this.log?.(`[master] 授予 0 -> ${req.workerId} (配额池与他人余额均已耗尽)`);
    }

    return {
      allocated: state.allocated,
      expiresAt: this.windowStart + this.windowMs,
      windowStart: this.windowStart,
      poolFree: this.poolFree,
    };
  }
}
