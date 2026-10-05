/**
 * 虚拟时钟：以确定性的方式模拟节点延迟与复制滞后。
 * 所有定时器按虚拟时间排序，advance() 逐个触发并让出微任务队列，
 * 使依赖定时器的 Promise 链（如等待副本追平）可以同步推进。
 */
export interface TimerHandle {
  readonly id: number;
}

interface TimerEntry {
  at: number;
  id: number;
  cb: () => void;
}

export class VirtualClock {
  private current = 0;
  private seq = 0;
  private timers: TimerEntry[] = [];

  now(): number {
    return this.current;
  }

  schedule(delayMs: number, cb: () => void): TimerHandle {
    const entry: TimerEntry = {
      at: this.current + Math.max(0, delayMs),
      id: ++this.seq,
      cb,
    };
    let lo = 0;
    let hi = this.timers.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const t = this.timers[mid];
      if (t.at < entry.at || (t.at === entry.at && t.id < entry.id)) lo = mid + 1;
      else hi = mid;
    }
    this.timers.splice(lo, 0, entry);
    return { id: entry.id };
  }

  cancel(handle: TimerHandle): void {
    const idx = this.timers.findIndex((t) => t.id === handle.id);
    if (idx >= 0) this.timers.splice(idx, 1);
  }

  sleep(ms: number): Promise<void> {
    return new Promise((resolve) => {
      this.schedule(ms, resolve);
    });
  }

  /** 推进虚拟时间，按序触发到期定时器；每触发一个都让出事件循环以结算 Promise 链。 */
  async advance(ms: number): Promise<void> {
    // 先结算微任务：调用方常在同一同步刻度里“发起操作 + 推进时钟”，
    // 操作内部注册的定时器必须先进入队列，再计算推进终点。
    await new Promise((r) => setImmediate(r));
    const end = this.current + ms;
    for (;;) {
      const next = this.timers[0];
      if (!next || next.at > end) break;
      this.timers.shift();
      this.current = next.at;
      next.cb();
      await new Promise((r) => setImmediate(r));
    }
    this.current = end;
    await new Promise((r) => setImmediate(r));
  }
}
