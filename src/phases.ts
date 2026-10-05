/**
 * Lifecycle phases of a staged job.
 *
 *   idle ──▶ preparing ──▶ submitting ──▶ cleaning ──▶ completed
 *              │  │             │
 *              │  │             └─▶ in-doubt ──▶ cleaning
 *              │  └─▶ failed (prepare blew up, nothing submitted)
 *              └─▶ cancelled (only reachable BEFORE the submit commit point)
 *
 * `cancelled` is intentionally unreachable once `submitting` has been
 * entered: after the commit point the executor owes the caller a
 * definitive submission outcome, not an abort.
 */
export type Phase =
  | 'idle'
  | 'preparing'
  | 'submitting'
  | 'in-doubt'
  | 'cleaning'
  | 'completed'
  | 'cancelled'
  | 'failed';

/**
 * Submission outcome tracked independently of the phase, so that a
 * cleanup failure (or a crash) can never erase the fact that a job
 * was submitted.
 *
 * - `not-started`: submit was never invoked; the job definitely did not run.
 * - `in-doubt`:    submit was invoked but the outcome is unknown (crash or
 *                  inconclusive error). The job MAY be running remotely.
 *                  Must never be reported externally as "not executed".
 * - `accepted`:    the remote side definitively accepted the job.
 * - `rejected`:    the remote side definitively refused the job; it did
 *                  not run.
 */
export type SubmissionState =
  | { kind: 'not-started' }
  | { kind: 'in-doubt'; startedAt: number }
  | { kind: 'accepted'; receipt: string; at: number }
  | { kind: 'rejected'; reason: string; at: number };

export interface CleanupState {
  attempted: boolean;
  /** Preserved for diagnostics; never alters the submission state. */
  error?: string;
}

/**
 * Serializable recovery record. Everything in here must survive
 * `JSON.parse(JSON.stringify(record))` unchanged.
 */
export interface RecoveryRecord {
  version: 1;
  jobId: string;
  phase: Phase;
  cancelRequested: boolean;
  submission: SubmissionState;
  cleanup: CleanupState;
  /** Opaque, JSON-serializable context produced by `prepare`. */
  prepareContext: unknown;
  /** Set when `prepare` itself failed. */
  failure?: string;
  updatedAt: number;
}

/** Tri-state answer to "did the job execute?". */
export type Executed = boolean | 'unknown';

/**
 * Externally visible status. The critical invariant:
 * `executed` is `true` whenever the submission was accepted, and is
 * `'unknown'` (never `false`) while the outcome is in doubt.
 */
export interface PublicStatus {
  jobId: string;
  phase: Phase;
  executed: Executed;
  receipt?: string;
  cancelRequested: boolean;
  cleanupError?: string;
  failure?: string;
}
