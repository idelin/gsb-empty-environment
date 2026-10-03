type ScheduledTask = { at: number; seq: number; fn: () => void };

/**
 * Deterministic discrete-event scheduler driven by virtual time.
 * Tasks run in (time, insertion-sequence) order; `run()` drains the queue.
 */
export class Scheduler {
  now = 0;
  private queue: ScheduledTask[] = [];
  private seq = 0;

  schedule(delay: number, fn: () => void): void {
    this.queue.push({ at: this.now + delay, seq: this.seq++, fn });
  }

  run(): void {
    for (;;) {
      let best = -1;
      for (let i = 0; i < this.queue.length; i++) {
        const t = this.queue[i];
        if (
          best === -1 ||
          t.at < this.queue[best].at ||
          (t.at === this.queue[best].at && t.seq < this.queue[best].seq)
        ) {
          best = i;
        }
      }
      if (best === -1) return;
      const [task] = this.queue.splice(best, 1);
      this.now = Math.max(this.now, task.at);
      task.fn();
    }
  }

  get pending(): number {
    return this.queue.length;
  }
}
