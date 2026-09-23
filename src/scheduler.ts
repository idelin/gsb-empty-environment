/**
 * 可控调度器：所有内部续体（settle、dispose、late-release 等）都作为
 * 任务进入 FIFO 队列，每次 flush 一个微任务检查点，因此与用户的
 * promise 反应交错顺序完全确定，测试可逐步枚举交错场景。
 */
export class ManualScheduler {
  #queue: Array<() => void> = [];
  #idleWaiters: Array<() => void> = [];
  #flushing = false;
  #tick = 0;

  /** 已入队但尚未执行的任务数。 */
  get pending(): number {
    return this.#queue.length;
  }

  /** 单调递增的任务计数，便于测试断言。 */
  get tick(): number {
    return this.#tick;
  }

  /** 提交一个任务（同步入队，异步按 FIFO 执行）。 */
  post(task: () => void): void {
    this.#queue.push(task);
    this.#pump();
  }

  /** 把 promise 的完成桥接为一个调度任务。 */
  asTask<T>(promise: Promise<T>): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      promise.then(
        (value) => this.post(() => resolve(value)),
        (err) => this.post(() => reject(err)),
      );
    });
  }

  /** 在当前微任务检查点内尽量执行队列（不等待新提交的任务）。 */
  flush(): void {
    while (this.#queue.length > 0) {
      const task = this.#queue.shift()!;
      this.#tick++;
      task();
    }
  }

  /**
   * 排空：反复执行“清空当前队列 -> 让出微任务”，直到没有新任务产生。
   * 每个 maxTick 对应一次微任务检查点，交错顺序确定。
   */
  async drain(maxTicks = 1000): Promise<void> {
    for (let i = 0; i < maxTicks; i++) {
      await Promise.resolve();
      this.flush();
      if (this.#queue.length === 0) {
        // 再给一个检查点，确认没有延迟一拍的新任务。
        await Promise.resolve();
        this.flush();
        if (this.#queue.length === 0) return;
      }
    }
    throw new Error(`ManualScheduler.drain: exceeded ${maxTicks} ticks (possible livelock)`);
  }

  /** 等待“下一次有任务被提交”，用于 destroy 等待在途 acquire 收敛。 */
  waitActivity(): Promise<void> {
    if (this.#queue.length > 0) return Promise.resolve();
    return new Promise<void>((resolve) => {
      this.#idleWaiters.push(resolve);
    });
  }

  #pump(): void {
    if (this.#flushing) return;
    this.#flushing = true;
    queueMicrotask(() => {
      this.#flushing = false;
      this.flush();
      if (this.#queue.length === 0 && this.#idleWaiters.length > 0) {
        const waiters = this.#idleWaiters;
        this.#idleWaiters = [];
        for (const resolve of waiters) resolve();
      }
    });
  }
}
