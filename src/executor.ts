import { JobBackend } from './backend';
import { CancellationToken } from './cancellation';
import { CancelledError, SubmissionRejectedError } from './errors';
import { Phase, PublicStatus, RecoveryRecord } from './phases';
import {
  RecoveryStore,
  InMemoryRecoveryStore,
  deserializeRecord,
  initialRecord,
  serializeRecord,
  toPublicStatus,
} from './record';
import { assertTransition } from './stateMachine';

/**
 * Named async boundaries of the executor pipeline. Every `await` in
 * `run()`/`resume()` sits behind exactly one of these, which lets tests
 * (and operators) inject cancellation at each individual boundary and
 * audit the externally visible state there.
 */
export type Boundary =
  | 'prepare'
  | 'after-prepare'
  | 'submit'
  | 'after-submit'
  | 'cleanup'
  | 'after-cleanup'
  | 'reconcile';

export const BOUNDARIES: readonly Boundary[] = [
  'prepare',
  'after-prepare',
  'submit',
  'after-submit',
  'cleanup',
  'after-cleanup',
];

export interface ExecutorOptions {
  jobId: string;
  backend: JobBackend;
  store?: RecoveryStore;
  now?: () => number;
  /** Observability hook fired synchronously before each async boundary. */
  onBoundary?: (boundary: Boundary, record: Readonly<RecoveryRecord>) => void;
}

/**
 * Drives one job through prepare -> submit -> cleanup with crash-safe,
 * cancellation-safe semantics:
 *
 * 1. PREPARE is cancellable. Cancellation checkpoints exist only here.
 * 2. SUBMIT is a commit point. The record is persisted as `in-doubt`
 *    BEFORE the backend call, and the executor then waits for a
 *    definitive outcome no matter what (cancellation included).
 * 3. CLEANUP never clobbers the submission state; its failures are
 *    recorded alongside, not instead of, the submission outcome.
 */
export class StagedJobExecutor {
  private backend: JobBackend;
  private store: RecoveryStore;
  private now: () => number;
  private onBoundary?: ExecutorOptions['onBoundary'];
  private token = new CancellationToken();
  private record: RecoveryRecord;

  constructor(options: ExecutorOptions, record?: RecoveryRecord) {
    this.backend = options.backend;
    this.store = options.store ?? new InMemoryRecoveryStore();
    this.now = options.now ?? (() => Date.now());
    this.onBoundary = options.onBoundary;
    this.record = record ?? initialRecord(options.jobId, this.now());
    this.persist();
  }

  /** Rebuild an executor from a serialized recovery record. */
  static restore(json: string, options: Omit<ExecutorOptions, 'jobId'>): StagedJobExecutor {
    const record = deserializeRecord(json);
    return new StagedJobExecutor({ ...options, jobId: record.jobId }, record);
  }

  /** Request cancellation. Honored only before the submit commit point. */
  cancel(): void {
    this.record.cancelRequested = true;
    this.persist();
    this.token.cancel();
  }

  get phase(): Phase {
    return this.record.phase;
  }

  /** Serializable recovery record (defensive copy). */
  snapshot(): RecoveryRecord {
    return deserializeRecord(serializeRecord(this.record));
  }

  serialize(): string {
    return serializeRecord(this.record);
  }

  /** Externally visible status; never lies about a submitted job. */
  status(): PublicStatus {
    return toPublicStatus(this.record);
  }

  async run(): Promise<PublicStatus> {
    this.transition('preparing');

    // ---- Stage 1: prepare (cancellable) -------------------------------
    let context: unknown;
    try {
      await this.boundary('prepare');
      this.token.throwIfCancelled();
      context = await this.backend.prepare(this.token);
      this.record.prepareContext = context ?? null;
      this.persist();

      await this.boundary('after-prepare');
      // Last cancellable checkpoint: beyond this line the job commits.
      this.token.throwIfCancelled();
    } catch (err) {
      if (err instanceof CancelledError) {
        this.transition('cancelled');
      } else {
        this.record.failure = errMessage(err);
        this.transition('failed');
      }
      return this.status();
    }

    // ---- Stage 2: submit (commit point, uncancellable) ----------------
    // Persist `in-doubt` BEFORE the call: if the process dies mid-submit
    // the recovery record says "maybe running", never "not executed".
    this.record.submission = { kind: 'in-doubt', startedAt: this.now() };
    this.transition('submitting');

    await this.boundary('submit');
    try {
      const receipt = await this.backend.submit(context);
      this.record.submission = { kind: 'accepted', receipt, at: this.now() };
      this.persist();
    } catch (err) {
      if (err instanceof SubmissionRejectedError) {
        this.record.submission = {
          kind: 'rejected',
          reason: err.message,
          at: this.now(),
        };
        this.persist();
      } else {
        // Inconclusive (network reset, timeout, crash, ...). The job may
        // be running remotely; park in `in-doubt` for reconciliation.
        this.transition('in-doubt');
        return this.status();
      }
    }

    await this.boundary('after-submit');
    await this.runCleanup(context);
    return this.status();
  }

  /**
   * Resume after a crash/restart from the restored record. Only
   * post-commit phases have work to do; anything earlier is already
   * terminal or safely reports "not executed".
   */
  async resume(): Promise<PublicStatus> {
    switch (this.record.phase) {
      case 'submitting':
      case 'in-doubt': {
        // We crashed (or errored) with the submission outcome unknown.
        // Reconcile against the backend; like submit, this waits for a
        // definitive answer and is not aborted by cancellation.
        await this.boundary('reconcile');
        const outcome = await this.backend.reconcile(this.snapshot());
        this.record.submission =
          outcome.kind === 'accepted'
            ? { kind: 'accepted', receipt: outcome.receipt, at: this.now() }
            : { kind: 'rejected', reason: outcome.reason, at: this.now() };
        this.persist();
        this.transition('cleaning');
        await this.runCleanup(this.record.prepareContext);
        return this.status();
      }
      case 'cleaning': {
        await this.runCleanup(this.record.prepareContext);
        return this.status();
      }
      default:
        return this.status();
    }
  }

  // ---- Stage 3: cleanup (failure-tolerant) ----------------------------
  private async runCleanup(context: unknown): Promise<void> {
    if (this.record.phase !== 'cleaning') this.transition('cleaning');
    await this.boundary('cleanup');
    try {
      await this.backend.cleanup(context, this.snapshot());
      this.record.cleanup = { attempted: true };
    } catch (err) {
      // Cleanup failed: record the error but DO NOT touch the
      // submission state. A submitted job stays submitted.
      this.record.cleanup = { attempted: true, error: errMessage(err) };
    }
    this.persist();
    await this.boundary('after-cleanup');
    this.transition('completed');
  }

  private async boundary(name: Boundary): Promise<void> {
    this.onBoundary?.(name, this.snapshot());
    // Yield so that effects triggered by the hook (e.g. cancel()) are
    // visible before the next real await, making boundaries deterministic.
    await Promise.resolve();
  }

  private transition(to: Phase): void {
    assertTransition(this.record.phase, to);
    this.record.phase = to;
    this.persist();
  }

  private persist(): void {
    this.record.updatedAt = this.now();
    this.store.save(this.record);
  }
}

function errMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
