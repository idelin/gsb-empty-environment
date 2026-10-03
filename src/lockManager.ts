import { LockConflictError } from './errors.ts';
import type { PageId, TxnId } from './types.ts';

/**
 * Strict 2PL exclusive page-lock manager. Locks are held to commit/abort.
 * Non-blocking acquisition lets the cooperative scheduler implement
 * wait-die deadlock avoidance (older waits, younger dies) deterministically.
 */
export class LockManager {
  private owners = new Map<PageId, TxnId>();
  private heldByTxn = new Map<TxnId, Set<PageId>>();

  holderOf(pageId: PageId): TxnId | undefined {
    return this.owners.get(pageId);
  }

  tryAcquire(txnId: TxnId, pageId: PageId): boolean {
    const holder = this.owners.get(pageId);
    if (holder === undefined || holder === txnId) {
      this.owners.set(pageId, txnId);
      let set = this.heldByTxn.get(txnId);
      if (!set) {
        set = new Set();
        this.heldByTxn.set(txnId, set);
      }
      set.add(pageId);
      return true;
    }
    return false;
  }

  acquire(txnId: TxnId, pageId: PageId): void {
    if (!this.tryAcquire(txnId, pageId)) {
      throw new LockConflictError(
        `txn ${txnId} cannot lock page ${pageId}: held by txn ${this.owners.get(pageId)}`,
      );
    }
  }

  releaseAll(txnId: TxnId): void {
    const pages = this.heldByTxn.get(txnId);
    if (!pages) return;
    for (const pageId of pages) {
      if (this.owners.get(pageId) === txnId) this.owners.delete(pageId);
    }
    this.heldByTxn.delete(txnId);
  }

  get lockCount(): number {
    return this.owners.size;
  }

  get isEmpty(): boolean {
    return this.owners.size === 0;
  }
}
