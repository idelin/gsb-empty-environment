export interface TimerHandle {
  id: number;
}

/** 时钟抽象：生产用系统时钟，测试用手动时钟可瞬间"跨越数小时" */
export interface Clock {
  now(): number;
  setTimeout(cb: () => void, ms: number): TimerHandle;
  clearTimeout(handle: TimerHandle): void;
}

export class SystemClock implements Clock {
  private handles = new Map<number, ReturnType<typeof setTimeout>>();
  private nextId = 1;

  now(): number {
    return Date.now();
  }

  setTimeout(cb: () => void, ms: number): TimerHandle {
    const id = this.nextId++;
    this.handles.set(id, setTimeout(() => {
      this.handles.delete(id);
      cb();
    }, ms));
    return { id };
  }

  clearTimeout(handle: TimerHandle): void {
    const t = this.handles.get(handle.id);
    if (t !== undefined) {
      clearTimeout(t);
      this.handles.delete(handle.id);
    }
  }
}

export class ManualClock implements Clock {
  private current: number;
  private timers: Array<{ id: number; at: number; cb: () => void }> = [];
  private nextId = 1;

  constructor(start = 0) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

  setTimeout(cb: () => void, ms: number): TimerHandle {
    const id = this.nextId++;
    this.timers.push({ id, at: this.current + Math.max(0, ms), cb });
    return { id };
  }

  clearTimeout(handle: TimerHandle): void {
    this.timers = this.timers.filter((t) => t.id !== handle.id);
  }

  /** 推进时间，按到期顺序触发所有到期定时器（含回调中新注册的） */
  advance(ms: number): void {
    const target = this.current + ms;
    for (;;) {
      const due = this.timers
        .filter((t) => t.at <= target)
        .sort((a, b) => a.at - b.at || a.id - b.id)[0];
      if (!due) break;
      this.current = due.at;
      this.timers = this.timers.filter((t) => t.id !== due.id);
      due.cb();
    }
    this.current = target;
  }
}
