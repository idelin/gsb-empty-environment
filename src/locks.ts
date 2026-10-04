import type { PageId, TxnId } from "./types.ts";

export class LockConflictError extends Error {
  constructor(page: PageId, holder: TxnId, requester: TxnId) {
    super(`lock conflict on page "${page}": held by txn ${holder}, requested by txn ${requester} (strict 2PL, no-wait)`);
    this.name = "LockConflictError";
  }
}

/** Strict transaction-level exclusive locks, held until commit/abort. */
export class LockManager {
  private owners = new Map<PageId, TxnId>();

  acquire(page: PageId, txn: TxnId): void {
    const owner = this.owners.get(page);
    if (owner === undefined || owner === txn) {
      this.owners.set(page, txn);
      return;
    }
    throw new LockConflictError(page, owner, txn);
  }

  releaseAll(txn: TxnId): PageId[] {
    const released: PageId[] = [];
    for (const [p, o] of this.owners) {
      if (o === txn) {
        this.owners.delete(p);
        released.push(p);
      }
    }
    return released;
  }

  holder(page: PageId): TxnId | undefined {
    return this.owners.get(page);
  }
}
