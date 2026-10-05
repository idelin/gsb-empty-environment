import { JobBackend, ReconcileOutcome } from '../src/backend';
import { CancellationToken } from '../src/cancellation';
import { SubmissionRejectedError } from '../src/errors';
import { RecoveryRecord } from '../src/phases';

export type SubmitBehavior = 'accept' | 'reject' | 'inconclusive';

/**
 * Deterministic in-memory backend. Every hook counts its calls so tests
 * can prove (for example) that a cancelled prepare never reaches submit.
 */
export class FakeBackend implements JobBackend {
  prepareCalls = 0;
  submitCalls = 0;
  cleanupCalls = 0;
  reconcileCalls = 0;

  submitBehavior: SubmitBehavior = 'accept';
  cleanupError?: string;
  reconcileOutcome: ReconcileOutcome = { kind: 'accepted', receipt: 'reconciled-receipt' };
  receipt = 'receipt-001';

  /** Optional side effect run inside `prepare`, e.g. cancel the token. */
  duringPrepare?: (token: CancellationToken) => void;

  async prepare(token: CancellationToken): Promise<unknown> {
    this.prepareCalls += 1;
    await Promise.resolve();
    this.duringPrepare?.(token);
    await Promise.resolve();
    return { allocated: true };
  }

  async submit(_context: unknown): Promise<string> {
    this.submitCalls += 1;
    await Promise.resolve();
    switch (this.submitBehavior) {
      case 'accept':
        return this.receipt;
      case 'reject':
        throw new SubmissionRejectedError('quota exceeded');
      case 'inconclusive':
        throw new Error('socket hang up');
    }
  }

  async cleanup(_context: unknown, _record: RecoveryRecord): Promise<void> {
    this.cleanupCalls += 1;
    await Promise.resolve();
    if (this.cleanupError !== undefined) throw new Error(this.cleanupError);
  }

  async reconcile(_record: RecoveryRecord): Promise<ReconcileOutcome> {
    this.reconcileCalls += 1;
    await Promise.resolve();
    return this.reconcileOutcome;
  }
}
