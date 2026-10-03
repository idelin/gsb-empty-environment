/**
 * Logical key-level lock manager: strict 2PL.
 *
 * Exclusive locks are held until the transaction ends (commit/abort),
 * which guarantees that undo never clobbers another transaction's write.
 * Single-node, deterministic: conflicts are reported immediately
 * (a caller driving a deterministic schedule retries with another action).
 */

import type { PageID, TxnID } from './log-record.ts';

export class LockConflictError extends Error {
  readonly lockKey: string;
  readonly holder: TxnID;
  readonly requester: TxnID;

  constructor(lockKey: string, holder: TxnID, requester: TxnID) {
    super(`lock conflict on ${lockKey}: held by T${holder}, requested by T${requester}`);
    this.lockKey = lockKey;
    this.holder = holder;
    this.requester = requester;
  }
}

export class LockManager {
  private holders = new Map<string, TxnID>();
  private byTxn = new Map<TxnID, Set<string>>();

  private static key(pageId: PageID, key: string): string {
    return `${pageId}.${key}`;
  }

  /** Acquire an exclusive lock; re-entrant for the same transaction. */
  acquire(txnId: TxnID, pageId: PageID, key: string): void {
    const k = LockManager.key(pageId, key);
    const holder = this.holders.get(k);
    if (holder !== undefined && holder !== txnId) {
      throw new LockConflictError(k, holder, txnId);
    }
    this.holders.set(k, txnId);
    let owned = this.byTxn.get(txnId);
    if (!owned) this.byTxn.set(txnId, (owned = new Set()));
    owned.add(k);
  }

  /** Release every lock held by a transaction (called at commit/abort). */
  releaseAll(txnId: TxnID): void {
    const owned = this.byTxn.get(txnId);
    if (!owned) return;
    for (const k of owned) this.holders.delete(k);
    this.byTxn.delete(txnId);
  }

  isLocked(pageId: PageID, key: string): boolean {
    return this.holders.has(LockManager.key(pageId, key));
  }

  get heldCount(): number {
    return this.holders.size;
  }
}
