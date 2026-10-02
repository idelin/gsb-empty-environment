import { LockConflictError } from './errors.ts';

/**
 * In-memory exclusive lock table, keyed by resource name. Locks are held by
 * exactly one transaction and are only released on commit/rollback. After a
 * crash, locks for in-doubt (PREPARED) transactions are re-acquired during
 * recovery, so the prepare promise remains enforced across restarts.
 */
export class LockManager {
  private readonly ownerByKey = new Map<string, string>();

  acquire(xid: string, keys: Iterable<string>): void {
    for (const key of keys) {
      const holder = this.ownerByKey.get(key);
      if (holder !== undefined && holder !== xid) {
        throw new LockConflictError(key, holder);
      }
    }
    for (const key of keys) {
      this.ownerByKey.set(key, xid);
    }
  }

  release(xid: string): void {
    for (const [key, holder] of this.ownerByKey) {
      if (holder === xid) this.ownerByKey.delete(key);
    }
  }

  holderOf(key: string): string | undefined {
    return this.ownerByKey.get(key);
  }

  isLocked(key: string): boolean {
    return this.ownerByKey.has(key);
  }
}
