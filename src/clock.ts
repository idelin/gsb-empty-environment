/**
 * 时钟源抽象：单调时钟（Monotonic）与墙钟（Wall-Clock）。
 * 所有时间单位均为毫秒。
 */

export interface ClockSource {
  /** 返回当前时钟读数（毫秒）。 */
  now(): number;
}

/** 系统单调时钟：不受系统时间调整影响，只增不减。 */
export class SystemMonotonicClock implements ClockSource {
  now(): number {
    return performance.now();
  }
}

/** 系统墙钟：受人为修改系统时间 / NTP 阶跃影响。 */
export class SystemWallClock implements ClockSource {
  now(): number {
    return Date.now();
  }
}

/** 可手动推进的时钟，用于测试与演示（可模拟回拨、阶跃、挂起）。 */
export class ManualClock implements ClockSource {
  private current: number;

  constructor(start = 0) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

  /** 推进 deltaMs 毫秒（可为负，用于模拟墙钟回拨）。 */
  advance(deltaMs: number): void {
    this.current += deltaMs;
  }

  set(value: number): void {
    this.current = value;
  }
}
