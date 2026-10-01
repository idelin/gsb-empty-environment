/**
 * 确定性手动调度器：替代真实定时器，让异步挂载/滚动在测试中可重放。
 * runAll 带任务数上限，从机制上保证“无循环等待”可被检测。
 */

interface Task {
  at: number;
  seq: number;
  fn: () => void;
  cancelled: boolean;
}

export const MAX_TASKS_PER_RUN = 10000;

export class Scheduler {
  now = 0;
  private queue: Task[] = [];
  private seq = 0;

  get pendingCount(): number {
    return this.queue.filter((t) => !t.cancelled).length;
  }

  setTimeout(fn: () => void, delay: number): Task {
    const task: Task = { at: this.now + delay, seq: this.seq++, fn, cancelled: false };
    this.queue.push(task);
    return task;
  }

  cancel(task: Task): void {
    task.cancelled = true;
  }

  /** 推进时钟并执行到期任务（按到期时间、提交次序）。 */
  advance(ms: number): void {
    const until = this.now + ms;
    let ran = 0;
    for (;;) {
      const next = this.queue
        .filter((t) => !t.cancelled && t.at <= until)
        .sort((a, b) => a.at - b.at || a.seq - b.seq)[0];
      if (!next) break;
      next.cancelled = true;
      this.now = next.at;
      next.fn();
      if (++ran > MAX_TASKS_PER_RUN) {
        throw new Error('scheduler: task overflow (circular wait suspected)');
      }
    }
    this.now = until;
    this.queue = this.queue.filter((t) => !t.cancelled);
  }

  /** 执行所有已排队任务；超过上限即视为循环等待并抛错。 */
  runAll(): void {
    let ran = 0;
    while (this.pendingCount > 0) {
      const nextAt = Math.min(...this.queue.filter((t) => !t.cancelled).map((t) => t.at));
      this.advance(nextAt - this.now);
      if (++ran > MAX_TASKS_PER_RUN) {
        throw new Error('scheduler: task overflow (circular wait suspected)');
      }
    }
  }
}
