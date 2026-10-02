/** 时钟抽象：测试用手动时钟，运行时用系统时钟。 */
export interface Clock {
  now(): number;
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

/** 真实时钟，包装全局定时器。 */
export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
  setTimeout(callback: () => void, delayMs: number): unknown {
    return setTimeout(callback, delayMs);
  }
  clearTimeout(handle: unknown): void {
    clearTimeout(handle as ReturnType<typeof setTimeout>);
  }
}

/** 手动时钟：虚拟时间，由测试显式推进，保证确定性。 */
export class ManualClock implements Clock {
  private current = 0;
  private nextId = 0;
  private queue: Array<{ id: number; time: number; callback: () => void }> = [];

  now(): number {
    return this.current;
  }

  setTimeout(callback: () => void, delayMs: number): unknown {
    const id = ++this.nextId;
    this.queue.push({ id, time: this.current + delayMs, callback });
    return id;
  }

  clearTimeout(handle: unknown): void {
    this.queue = this.queue.filter((entry) => entry.id !== handle);
  }

  /** 推进虚拟时间 ms 毫秒，按到期顺序执行回调（同刻按注册序）。 */
  advance(ms: number): void {
    const end = this.current + ms;
    for (;;) {
      let soonest: { id: number; time: number; callback: () => void } | undefined;
      for (const entry of this.queue) {
        if (entry.time > end) continue;
        if (!soonest || entry.time < soonest.time || (entry.time === soonest.time && entry.id < soonest.id)) {
          soonest = entry;
        }
      }
      if (!soonest) break;
      this.queue = this.queue.filter((entry) => entry.id !== soonest!.id);
      this.current = soonest.time;
      soonest.callback();
    }
    this.current = end;
  }
}
