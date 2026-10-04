/** A single publish request: target path, expected old version, new digest. */
export interface PublishRequest {
  path: string;
  expectedVersion: number;
  digest: string;
  /** Optional caller-supplied key; resubmits with the same key are idempotent. */
  idempotencyKey?: string;
}

export interface PublishReceipt {
  path: string;
  digest: string;
  /** Version after the commit (expectedVersion + 1). */
  version: number;
  committedAt: number;
  idempotencyKey?: string;
}

export type PublishOutcome =
  | { kind: 'committed'; receipt: PublishReceipt }
  | { kind: 'cancelled' }
  | { kind: 'conflict'; currentVersion: number | undefined }
  | { kind: 'failed'; error: Error };

export type CancelResult =
  | { ok: true; status: 'cancelled' }
  /** Past the confirmation point but not yet settled; will never be cancelled. */
  | { ok: false; status: 'committing' }
  | { ok: false; status: 'committed'; receipt: PublishReceipt }
  | { ok: false; status: 'settled'; outcome: PublishOutcome };

export interface PublishHandle {
  readonly request: PublishRequest;
  readonly done: Promise<PublishOutcome>;
  /**
   * Effective only before the confirmation point (the atomic commit). Once the
   * commit has begun or completed, cancellation is refused and the committed
   * result is reported — a confirmed publish is never claimed to be revoked.
   */
  cancel(): CancelResult;
}

export class VersionConflictError extends Error {
  readonly currentVersion: number | undefined;
  constructor(path: string, currentVersion: number | undefined) {
    super(`version conflict on ${path}: current=${currentVersion}`);
    this.name = 'VersionConflictError';
    this.currentVersion = currentVersion;
  }
}

/** Commit was applied to the store, but the acknowledgement was lost in transit. */
export class ReceiptLostError extends Error {
  constructor(path: string) {
    super(`confirmation receipt lost for ${path}`);
    this.name = 'ReceiptLostError';
  }
}
