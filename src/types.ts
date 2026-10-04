/** Shared domain types for the hot/cold migration simulator. */

export type Bytes = string;

/** A receipt issued by the target after an upload is applied. */
export interface Receipt {
  objectId: string;
  version: number;
  digest: string;
  issuedAt: number;
}

/**
 * A migration decision. Must be durably persisted BEFORE the local
 * copy is reclaimed; it is the proof that the target holds a verified
 * copy of this exact version.
 */
export interface Decision {
  objectId: string;
  version: number;
  digest: string;
  decidedAt: number;
}

/** Migration state machine states. */
export const MigState = {
  /** Object exists only locally. */
  LocalOnly: 'LOCAL_ONLY',
  /** Upload of the migrating version is in flight. */
  Uploading: 'UPLOADING',
  /** Upload applied at target; waiting for the receipt (may be lost). */
  AwaitingReceipt: 'AWAITING_RECEIPT',
  /** Reading the object back from the target to compare digests. */
  Verifying: 'VERIFYING',
  /** Read-back verified; persisting the migration decision. */
  PersistingDecision: 'PERSISTING_DECISION',
  /** Decision persisted, but local handles are still open; deletion deferred. */
  ReclaimBlocked: 'RECLAIM_BLOCKED',
  /** Local copy reclaimed; the target holds the only copy. */
  Reclaimed: 'RECLAIMED',
} as const;

export type MigState = (typeof MigState)[keyof typeof MigState];
