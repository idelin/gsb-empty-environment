import {
  Executed,
  PublicStatus,
  RecoveryRecord,
  SubmissionState,
} from './phases';

export function initialRecord(jobId: string, now: number): RecoveryRecord {
  return {
    version: 1,
    jobId,
    phase: 'idle',
    cancelRequested: false,
    submission: { kind: 'not-started' },
    cleanup: { attempted: false },
    prepareContext: null,
    updatedAt: now,
  };
}

/**
 * Derives the externally visible "did it execute?" answer from the
 * submission state. This is the single place where the invariant lives:
 *
 *   accepted    -> true        (a submitted job is NEVER "not executed")
 *   in-doubt    -> 'unknown'   (never optimistically false)
 *   rejected    -> false       (definitively did not run)
 *   not-started -> false
 */
export function executedOf(submission: SubmissionState): Executed {
  switch (submission.kind) {
    case 'accepted':
      return true;
    case 'in-doubt':
      return 'unknown';
    case 'rejected':
    case 'not-started':
      return false;
  }
}

export function toPublicStatus(record: RecoveryRecord): PublicStatus {
  const status: PublicStatus = {
    jobId: record.jobId,
    phase: record.phase,
    executed: executedOf(record.submission),
    cancelRequested: record.cancelRequested,
  };
  if (record.submission.kind === 'accepted') {
    status.receipt = record.submission.receipt;
  }
  if (record.cleanup.error !== undefined) {
    status.cleanupError = record.cleanup.error;
  }
  if (record.failure !== undefined) {
    status.failure = record.failure;
  }
  return status;
}

/** Serialize a recovery record to a JSON string. */
export function serializeRecord(record: RecoveryRecord): string {
  return JSON.stringify(record);
}

/** Parse and validate a recovery record produced by `serializeRecord`. */
export function deserializeRecord(json: string): RecoveryRecord {
  const parsed = JSON.parse(json) as RecoveryRecord;
  if (parsed.version !== 1) {
    throw new Error(`unsupported recovery record version: ${parsed.version}`);
  }
  if (typeof parsed.jobId !== 'string' || typeof parsed.phase !== 'string') {
    throw new Error('malformed recovery record');
  }
  return parsed;
}

/** Pluggable persistence for recovery records (one synchronous slot per job). */
export interface RecoveryStore {
  save(record: RecoveryRecord): void;
  load(jobId: string): RecoveryRecord | undefined;
}

export class InMemoryRecoveryStore implements RecoveryStore {
  private readonly slots = new Map<string, string>();

  save(record: RecoveryRecord): void {
    // Round-trip through JSON so callers cannot mutate persisted state
    // by holding a reference, mirroring a real disk/DB store.
    this.slots.set(record.jobId, serializeRecord(record));
  }

  load(jobId: string): RecoveryRecord | undefined {
    const json = this.slots.get(jobId);
    return json === undefined ? undefined : deserializeRecord(json);
  }
}
