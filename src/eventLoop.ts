/** 离散事件循环：按虚拟时间顺序调度执行回调。 */
export interface TimerHandle {
  cancel(): void;
}

interface ScheduledEvent {
  time: number;
  seq: number;
  fn: () => void;
  cancelled: boolean;
}

export class EventLoop {
  now = 0;
  private queue: ScheduledEvent[] = [];
  private seq = 0;

  schedule(delay: number, fn: () => void): TimerHandle {
    const ev: ScheduledEvent = { time: this.now + delay, seq: this.seq++, fn, cancelled: false };
    this.queue.push(ev);
    return { cancel: () => { ev.cancelled = true; } };
  }

  /** 处理完队列中所有事件（含处理过程中新调度的事件）。 */
  run(): void {
    for (;;) {
      let idx = -1;
      for (let i = 0; i < this.queue.length; i++) {
        const e = this.queue[i];
        if (e.cancelled) continue;
        if (idx === -1 || e.time < this.queue[idx].time ||
            (e.time === this.queue[idx].time && e.seq < this.queue[idx].seq)) {
          idx = i;
        }
      }
      if (idx === -1) break;
      const [ev] = this.queue.splice(idx, 1);
      this.now = Math.max(this.now, ev.time);
      ev.fn();
    }
    this.queue = this.queue.filter((e) => !e.cancelled);
  }
}
