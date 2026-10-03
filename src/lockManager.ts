import type { PageId, TxId } from './types.ts';

/**
 * 页级排他锁管理器：每页一条 FIFO 等待队列，事务持有锁直到提交/回滚（严格 2PL）。
 * 同一事务对同一页重复加锁为可重入。调用方按固定页序加锁可避免死锁。
 * 每个事务只释放自己的队首承诺，释放即唤醒该页队列中的下一个等待者。
 */
export class LockManager {
  private tails = new Map<PageId, Promise<void>>();
  private held = new Map<TxId, Map<PageId, () => void>>();

  async acquire(txId: TxId, pageId: PageId): Promise<void> {
    if (this.held.get(txId)?.has(pageId)) return; // 可重入

    const prev = this.tails.get(pageId) ?? Promise.resolve();
    let release!: () => void;
    const current = new Promise<void>((resolve) => {
      release = resolve;
    });
    this.tails.set(pageId, prev.then(() => current));
    await prev;

    if (!this.held.has(txId)) this.held.set(txId, new Map());
    this.held.get(txId)!.set(pageId, release);
  }

  /** 按页名字典序批量加锁（固定顺序，避免死锁） */
  async acquireAll(txId: TxId, pageIds: PageId[]): Promise<void> {
    for (const pageId of [...pageIds].sort()) {
      await this.acquire(txId, pageId);
    }
  }

  releaseAll(txId: TxId): void {
    const pages = this.held.get(txId);
    if (!pages) return;
    this.held.delete(txId);
    for (const release of pages.values()) release();
  }

  /** 当前被持有的锁总数 —— 泄漏断言用 */
  get heldCount(): number {
    let n = 0;
    for (const pages of this.held.values()) n += pages.size;
    return n;
  }
}
