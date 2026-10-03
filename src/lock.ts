import type { PageId, TxnId } from './types.ts';

export class LockConflictError extends Error {
  readonly resource: string;
  readonly holder: TxnId;
  constructor(resource: string, holder: TxnId) {
    super(`lock conflict on ${resource}: held by txn ${holder}`);
    this.resource = resource;
    this.holder = holder;
  }
}

// Strict 2PL: exclusive (page,key) locks held until commit/abort. This is what
// makes physiological undo safe: a key's before-image always belongs to the
// last committed writer, so undo never clobbers committed data. Conflicts are
// reported immediately (no waiting => no deadlock).
export class LockManager {
  private locks = new Map<string, TxnId>();
  private byTxn = new Map<TxnId, Set<string>>();

  acquire(txnId: TxnId, pageId: PageId, key: string): void {
    const resource = `${pageId}:${key}`;
    const holder = this.locks.get(resource);
    if (holder === txnId) return;
    if (holder !== undefined) throw new LockConflictError(resource, holder);
    this.locks.set(resource, txnId);
    let owned = this.byTxn.get(txnId);
    if (!owned) {
      owned = new Set();
      this.byTxn.set(txnId, owned);
    }
    owned.add(resource);
  }

  releaseAll(txnId: TxnId): void {
    const owned = this.byTxn.get(txnId);
    if (!owned) return;
    for (const resource of owned) this.locks.delete(resource);
    this.byTxn.delete(txnId);
  }

  get heldCount(): number {
    return this.locks.size;
  }
}
