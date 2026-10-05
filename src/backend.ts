import { CancellationToken } from './cancellation';
import { RecoveryRecord } from './phases';

/** Definitive outcome of reconciling an in-doubt submission. */
export type ReconcileOutcome =
  | { kind: 'accepted'; receipt: string }
  | { kind: 'rejected'; reason: string };

/**
 * Backend hooks for the three stages. Implementations talk to whatever
 * real system runs the job (queue, cluster, CI, payment gateway, ...).
 *
 * Contract notes the executor relies on:
 * - `prepare` receives the cancellation token and SHOULD abort promptly
 *   when it is cancelled; nothing has been committed at this point.
 * - `submit` is intentionally NOT given the token. Once called, the
 *   executor waits for a definitive settlement: resolve with a receipt,
 *   or throw `SubmissionRejectedError`. Any other throw is treated as
 *   "outcome unknown" and puts the record into `in-doubt`.
 * - `cleanup` failures must not destroy submission information; the
 *   executor records the error and preserves the submission state.
 * - `reconcile` is used during recovery to resolve an `in-doubt`
 *   submission (e.g. query the remote side by idempotency key).
 */
export interface JobBackend {
  prepare(token: CancellationToken): Promise<unknown>;
  submit(prepareContext: unknown): Promise<string>;
  cleanup(prepareContext: unknown, record: RecoveryRecord): Promise<void>;
  reconcile(record: RecoveryRecord): Promise<ReconcileOutcome>;
}
