/**
 * 确定性任务队列:模拟浏览器微任务/动画帧的异步边界,
 * 让"异步挂载""交错提交"在测试中可重现。
 */
export class Scheduler {
  private queue: Array<() => void> = [];

  get pending(): number {
    return this.queue.length;
  }

  enqueue(task: () => void): void {
    this.queue.push(task);
  }

  /** 执行队首一个任务;队空返回 false。 */
  tick(): boolean {
    const task = this.queue.shift();
    if (task === undefined) return false;
    task();
    return true;
  }

  /** 排空队列(任务可继续入队)。 */
  flush(): void {
    while (this.tick()) {
      // drain
    }
  }
}
