import { IllegalLockReleaseError } from './errors.ts';
import type { LockManager, ReleasedLock } from './lock-manager.ts';

export type TransactionState = 'growing' | 'committed' | 'aborted';

/**
 * Shrinking-phase gatekeeper. In strict 2PL the shrinking phase is a single
 * atomic point: the moment a transaction commits or aborts, all of its
 * locks are released at once. Any attempt to release locks while the
 * transaction is still in its growing phase is rejected, which makes
 * premature partial release of row locks structurally impossible through
 * this API.
 */
export class ShrinkingPhaseManager {
  private readonly lockManager: LockManager;
  private readonly stateOf: (txnId: number) => TransactionState | undefined;

  constructor(lockManager: LockManager, stateOf: (txnId: number) => TransactionState | undefined) {
    this.lockManager = lockManager;
    this.stateOf = stateOf;
  }

  enterShrinkingPhase(txnId: number): ReleasedLock[] {
    const state = this.stateOf(txnId);
    if (state !== 'committed' && state !== 'aborted') {
      throw new IllegalLockReleaseError(
        `transaction ${txnId} is in state [${state ?? 'unknown'}]: SS2PL forbids releasing any lock before commit/abort`,
      );
    }
    return this.lockManager.releaseAll(txnId);
  }
}
