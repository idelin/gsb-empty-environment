/**
 * 公平 FIFO 读写闩锁（Read/Write Latch）。
 *
 * 数据库意义上的 "latch" 保护的是内存数据结构（这里是 B+ 树节点），
 * 与保护事务逻辑一致性的 "lock" 不同。本实现基于 Promise 模拟
 * 多线程阻塞/唤醒语义：获取失败的任务进入 FIFO 队列挂起，
 * 释放时按到达顺序唤醒，写者不会被后来的读者插队（无 barging）。
 */

export interface LatchStats {
  /** 未排队立即获得的读闩次数 */
  readImmediate: number;
  /** 排队等待后才获得的读闩次数（即"被阻塞"次数） */
  readWait: number;
  /** 未排队立即获得的写闩次数 */
  writeImmediate: number;
  /** 排队等待后才获得的写闩次数 */
  writeWait: number;
}

interface QueuedRequest {
  kind: "read" | "write";
  grant: () => void;
}

export class ReadWriteLatch {
  private activeReaders = 0;
  private writerActive = false;
  private readonly queue: QueuedRequest[] = [];

  readonly stats: LatchStats = {
    readImmediate: 0,
    readWait: 0,
    writeImmediate: 0,
    writeWait: 0,
  };

  /** 当前是否完全空闲（用于测试与调试断言） */
  get isIdle(): boolean {
    return (
      !this.writerActive && this.activeReaders === 0 && this.queue.length === 0
    );
  }

  get waitingCount(): number {
    return this.queue.length;
  }

  async acquireRead(): Promise<void> {
    // 队列非空时读者不得插队（barging），否则写者会饿死
    if (!this.writerActive && this.queue.length === 0) {
      this.activeReaders++;
      this.stats.readImmediate++;
      return;
    }
    this.stats.readWait++;
    await new Promise<void>((resolve) =>
      this.queue.push({ kind: "read", grant: resolve }),
    );
  }

  async acquireWrite(): Promise<void> {
    if (
      !this.writerActive &&
      this.activeReaders === 0 &&
      this.queue.length === 0
    ) {
      this.writerActive = true;
      this.stats.writeImmediate++;
      return;
    }
    this.stats.writeWait++;
    await new Promise<void>((resolve) =>
      this.queue.push({ kind: "write", grant: resolve }),
    );
  }

  releaseRead(): void {
    if (this.activeReaders <= 0) {
      throw new Error("releaseRead without matching acquireRead");
    }
    this.activeReaders--;
    this.drain();
  }

  releaseWrite(): void {
    if (!this.writerActive) {
      throw new Error("releaseWrite without matching acquireWrite");
    }
    this.writerActive = false;
    this.drain();
  }

  /** 按 FIFO 顺序唤醒队列中当前可以满足的请求 */
  private drain(): void {
    for (;;) {
      const head = this.queue[0];
      if (head === undefined) return;
      if (head.kind === "read") {
        if (this.writerActive) return;
        this.queue.shift();
        this.activeReaders++;
        head.grant();
      } else {
        if (this.writerActive || this.activeReaders > 0) return;
        this.queue.shift();
        this.writerActive = true;
        head.grant();
      }
    }
  }
}
