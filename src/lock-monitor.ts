import type { LockManager } from './lock-manager.ts';
import type { TransactionState } from './shrinking-phase-manager.ts';

/**
 * Watches the lock manager's audit stream and flags any lock release that
 * happens while the owning transaction is still in its growing phase —
 * i.e. any premature partial release of a row lock before Commit/Abort.
 */
export class LockMonitor {
  private readonly violations: string[] = [];

  constructor(lockManager: LockManager, stateOf: (txnId: number) => TransactionState | undefined) {
    lockManager.onAudit((event) => {
      if (event.kind !== 'release') return;
      const state = stateOf(event.txnId);
      if (state === 'growing' || state === undefined) {
        this.violations.push(
          `txn ${event.txnId} released ${event.mode} lock on [${event.resource}] while in state [${state ?? 'unknown'}]`,
        );
      }
    });
  }

  get violationCount(): number {
    return this.violations.length;
  }

  assertNoEarlyRelease(): void {
    if (this.violations.length > 0) {
      throw new Error(`SS2PL violation detected:\n${this.violations.join('\n')}`);
    }
  }
}
