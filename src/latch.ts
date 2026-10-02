/**
 * RwLatch —— 异步读写锁（Node 单线程事件循环下的协程式闩锁）。
 *
 * 语义与多线程读写锁一致：await 点即"线程切换点"。
 * - 读锁共享：多个读者可同时持有同一节点的读锁；
 * - 写锁互斥：写者独占，与任何读/写互斥；
 * - FIFO 公平：等待队列先进先出，写者不会被后来的读者插队（防写者饥饿）。
 *
 * 死锁自由由上层协议保证：所有锁都按"自上而下"的树序获取，
 * 锁顺序构成偏序（DAG），不存在循环等待。
 */

export interface LatchStats {
  readAcquires: number;
  writeAcquires: number;
  /** 读者被迫排队等待的次数（被写者阻塞） */
  readWaits: number;
  /** 写者被迫排队等待的次数 */
  writeWaits: number;
  /** 当前全局持有读锁的读者数 */
  activeReaders: number;
  /** 历史上同时持有读锁的最大读者数（>1 即证明读者间无阻塞高速穿越） */
  maxConcurrentReaders: number;
  reset(): void;
}

export const latchStats: LatchStats = {
  readAcquires: 0,
  writeAcquires: 0,
  readWaits: 0,
  writeWaits: 0,
  activeReaders: 0,
  maxConcurrentReaders: 0,
  reset() {
    this.readAcquires = 0;
    this.writeAcquires = 0;
    this.readWaits = 0;
    this.writeWaits = 0;
    this.activeReaders = 0;
    this.maxConcurrentReaders = 0;
  },
};

type Waiter = { kind: "read" | "write"; grant: () => void };

export class RwLatch {
  private readers = 0;
  private writing = false;
  private queue: Waiter[] = [];

  async acquireRead(): Promise<void> {
    latchStats.readAcquires++;
    // 快速路径：无写者且无人排队（队列非空时队首必为写者，需排队保证公平）
    if (!this.writing && this.queue.length === 0) {
      this.enterReader();
      return;
    }
    latchStats.readWaits++;
    await new Promise<void>((resolve) => this.queue.push({ kind: "read", grant: resolve }));
  }

  async acquireWrite(): Promise<void> {
    latchStats.writeAcquires++;
    if (!this.writing && this.readers === 0 && this.queue.length === 0) {
      this.writing = true;
      return;
    }
    latchStats.writeWaits++;
    await new Promise<void>((resolve) => this.queue.push({ kind: "write", grant: resolve }));
  }

  releaseRead(): void {
    this.readers--;
    latchStats.activeReaders--;
    this.drain();
  }

  releaseWrite(): void {
    this.writing = false;
    this.drain();
  }

  private enterReader(): void {
    this.readers++;
    latchStats.activeReaders++;
    if (latchStats.activeReaders > latchStats.maxConcurrentReaders) {
      latchStats.maxConcurrentReaders = latchStats.activeReaders;
    }
  }

  /** 锁状态变化后，按 FIFO 顺序唤醒等待者：连续读者一起放行，写者独占放行。 */
  private drain(): void {
    while (this.queue.length > 0) {
      const head = this.queue[0];
      if (head.kind === "read") {
        if (this.writing) return;
        this.queue.shift();
        this.enterReader();
        head.grant();
      } else {
        if (this.writing || this.readers > 0) return;
        this.queue.shift();
        this.writing = true;
        head.grant();
      }
    }
  }
}
