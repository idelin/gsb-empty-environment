/**
 * 双时钟源偏差检测与租约熔断守护器。
 *
 * 持续比对单调时钟与墙钟的推进步调：
 *  - 墙钟回拨（人为修改系统时间）      -> 瞬时负偏差 -> 熔断
 *  - NTP 大幅阶跃（向前/向后）          -> 瞬时大偏差 -> 熔断
 *  - 虚拟机时钟挂起（两个时钟同时停滞） -> 采样间隔异常 -> 熔断
 *  - 缓慢漂移累积                       -> 累积偏差超限 -> 熔断
 *
 * 熔断动作：强制注销所有活跃租约，并将租约注册表降级为安全模式。
 */

import { EventEmitter } from "node:events";
import type { ClockSource } from "./clock.ts";
import type { Lease, LeaseRegistry } from "./lease.ts";

export type GuardianState = "NORMAL" | "SAFE_MODE";

export type TripReason =
  | "WALL_CLOCK_JUMP" // 墙钟与单调时钟撕裂（回拨或 NTP 阶跃）
  | "CLOCK_SUSPEND" // 时钟挂起（采样间隔远超预期）
  | "DRIFT_ACCUMULATED"; // 缓慢漂移累积超限

export interface TripEvent {
  reason: TripReason;
  detail: string;
  wallDeltaMs: number;
  monotonicDeltaMs: number;
  skewMs: number;
  revokedLeases: Lease[];
  at: { monotonic: number; wall: number };
}

export interface SampleEvent {
  wallDeltaMs: number;
  monotonicDeltaMs: number;
  skewMs: number;
  cumulativeSkewMs: number;
}

export interface GuardianOptions {
  /** 采样间隔（毫秒），默认 100ms。 */
  intervalMs?: number;
  /** 单次采样允许的最大偏差（毫秒），默认 250ms。 */
  maxSkewMs?: number;
  /** 累积漂移容忍上限（毫秒），默认 1000ms。 */
  maxCumulativeSkewMs?: number;
  /** 单调时钟单次推进上限（毫秒），超过则判定为时钟挂起，默认 5000ms。 */
  maxTickGapMs?: number;
  /** 是否自动启动定时采样，默认 false（由调用方驱动 sample()）。 */
  autoStart?: boolean;
}

export class ClockSkewGuardian extends EventEmitter {
  private readonly registry: LeaseRegistry;
  private readonly monotonic: ClockSource;
  private readonly wall: ClockSource;
  private readonly intervalMs: number;
  private readonly maxSkewMs: number;
  private readonly maxCumulativeSkewMs: number;
  private readonly maxTickGapMs: number;

  private timer: NodeJS.Timeout | null = null;
  private lastMonotonic: number | null = null;
  private lastWall: number | null = null;
  private cumulativeSkewMs = 0;
  private tripEvent: TripEvent | null = null;

  state: GuardianState = "NORMAL";

  constructor(
    registry: LeaseRegistry,
    clocks: { monotonic: ClockSource; wall: ClockSource },
    options: GuardianOptions = {},
  ) {
    super();
    this.registry = registry;
    this.monotonic = clocks.monotonic;
    this.wall = clocks.wall;
    this.intervalMs = options.intervalMs ?? 100;
    this.maxSkewMs = options.maxSkewMs ?? 250;
    this.maxCumulativeSkewMs = options.maxCumulativeSkewMs ?? 1000;
    this.maxTickGapMs = options.maxTickGapMs ?? 5000;
    if (options.autoStart) {
      this.start();
    }
  }

  /** 启动定时采样（幂等）。 */
  start(): void {
    if (this.timer) return;
    this.baseline();
    this.timer = setInterval(() => this.sample(), this.intervalMs);
    this.timer.unref?.();
  }

  stop(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
  }

  get tripped(): boolean {
    return this.state === "SAFE_MODE";
  }

  get tripInfo(): TripEvent | null {
    return this.tripEvent;
  }

  /** 建立/重建时钟基线。 */
  private baseline(): void {
    this.lastMonotonic = this.monotonic.now();
    this.lastWall = this.wall.now();
    this.cumulativeSkewMs = 0;
  }

  /**
   * 执行一次双时钟比对采样。
   * 返回是否发生熔断。
   */
  sample(): boolean {
    if (this.tripped) return true;
    if (this.lastMonotonic === null || this.lastWall === null) {
      this.baseline();
      return false;
    }

    const monoNow = this.monotonic.now();
    const wallNow = this.wall.now();
    const monotonicDeltaMs = monoNow - this.lastMonotonic;
    const wallDeltaMs = wallNow - this.lastWall;
    const skewMs = wallDeltaMs - monotonicDeltaMs;
    this.cumulativeSkewMs += skewMs;

    this.lastMonotonic = monoNow;
    this.lastWall = wallNow;

    const sampleEvent: SampleEvent = {
      wallDeltaMs,
      monotonicDeltaMs,
      skewMs,
      cumulativeSkewMs: this.cumulativeSkewMs,
    };
    this.emit("sample", sampleEvent);

    // 1. 时钟挂起：单调时钟单次推进远超采样间隔（VM suspend / 进程冻结）
    if (monotonicDeltaMs > this.maxTickGapMs) {
      return this.trip(
        "CLOCK_SUSPEND",
        `monotonic clock advanced ${monotonicDeltaMs}ms in one tick ` +
          `(limit ${this.maxTickGapMs}ms); VM/clock suspend suspected`,
        sampleEvent,
      );
    }

    // 2. 瞬时撕裂：墙钟回拨或 NTP 大幅阶跃
    if (Math.abs(skewMs) > this.maxSkewMs) {
      const direction = skewMs < 0 ? "backward" : "forward";
      return this.trip(
        "WALL_CLOCK_JUMP",
        `wall clock jumped ${direction} by ${Math.abs(skewMs)}ms ` +
          `relative to monotonic clock (limit ${this.maxSkewMs}ms)`,
        sampleEvent,
      );
    }

    // 3. 缓慢漂移累积超限
    if (Math.abs(this.cumulativeSkewMs) > this.maxCumulativeSkewMs) {
      return this.trip(
        "DRIFT_ACCUMULATED",
        `cumulative clock drift ${this.cumulativeSkewMs}ms exceeded ` +
          `limit ${this.maxCumulativeSkewMs}ms`,
        sampleEvent,
      );
    }

    return false;
  }

  /** 熔断：注销全部租约并降级为安全模式。 */
  private trip(
    reason: TripReason,
    detail: string,
    sample: SampleEvent,
  ): boolean {
    this.state = "SAFE_MODE";
    this.registry.enterSafeMode();
    const revokedLeases = this.registry.revokeAll();
    this.tripEvent = {
      reason,
      detail,
      wallDeltaMs: sample.wallDeltaMs,
      monotonicDeltaMs: sample.monotonicDeltaMs,
      skewMs: sample.skewMs,
      revokedLeases,
      at: { monotonic: this.monotonic.now(), wall: this.wall.now() },
    };
    this.emit("trip", this.tripEvent);
    this.stop();
    return true;
  }

  /**
   * 人工确认时钟源恢复一致后复位：
   * 退出安全模式、重建基线、恢复采样。
   */
  reset(): void {
    this.state = "NORMAL";
    this.tripEvent = null;
    this.registry.exitSafeMode();
    this.baseline();
    this.emit("reset");
  }
}
