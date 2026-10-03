/**
 * 提交临界区的自旋闩锁。
 *
 * 读取阶段完全无锁、不阻塞；只有“验证 + 写入”这段极短的临界区
 * 需要闩锁保护。Node.js 单线程模型下该临界区是同步代码，天然原子，
 * 因此 acquire 永远不会真正自旋；若被重入说明存在并发缺陷，直接抛错。
 * 移植到多线程运行时（worker_threads + SharedArrayBuffer）时，
 * 此处对应 Atomics 自旋等待。
 */
export class SpinLatch {
  private held = false;

  acquire(): void {
    if (this.held) {
      throw new Error('commit critical section re-entered');
    }
    this.held = true;
  }

  release(): void {
    this.held = false;
  }
}
