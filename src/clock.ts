/**
 * 时钟抽象：让耗时相关逻辑在测试中可确定、可加速。
 */
export interface Clock {
  now(): number;
  sleep(ms: number): Promise<void>;
}

/** 生产环境时钟，基于真实系统时间与定时器。 */
export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }

  sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }
}

/**
 * 测试时钟：时间由测试手动推进。
 * sleep 会按请求的虚拟时长自动推进时间，并在微任务中兑现，
 * 从而保证「固定耗时」逻辑完全确定且无需真实等待；
 * advance 仍可用于模拟 TTL / 滑动窗口的时间流逝。
 */
export class ManualClock implements Clock {
  private current: number;
  private pendingCount = 0;

  constructor(start = 0) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

  sleep(ms: number): Promise<void> {
    this.current += Math.max(0, ms);
    this.pendingCount += 1;
    return Promise.resolve().then(() => {
      this.pendingCount -= 1;
    });
  }

  /** 显式推进虚拟时间（用于模拟窗口 / TTL 到期）。 */
  advance(ms: number): void {
    this.current += ms;
  }

  /** 是否还有未兑现的 sleep 微任务。 */
  hasPending(): boolean {
    return this.pendingCount > 0;
  }
}
