/** A message with causal dependencies (predecessor ids). */
export interface CausalMessage<T = unknown> {
  readonly id: string;
  /** Ids of predecessor messages that must be delivered before this one. */
  readonly deps: readonly string[];
  /** Lamport-style logical clock; must exceed every dep's clock. */
  readonly clock: number;
  readonly payload: T;
}

export type GapReason = "expired" | "deleted";

/**
 * Evidence that a message is permanently gone (expired by TTL or explicitly
 * deleted) rather than merely in flight. Recorded in the causal history at
 * the moment of removal so it can be shipped inside a reset boundary.
 */
export interface GapProof {
  readonly messageId: string;
  readonly reason: GapReason;
  /** Logical clock of the removed message. */
  readonly clock: number;
  /** Logical time at which the removal was recorded. */
  readonly removedAt: number;
}

/**
 * A reset boundary snapshot. Once installed, pending messages may skip
 * predecessors that are (a) proven gone by an included gap proof and
 * (b) older than the boundary watermark. Nothing else may be skipped.
 */
export interface ResetBoundary {
  readonly id: string;
  /** Logical time at which the boundary was taken. */
  readonly watermark: number;
  /** Ids delivered (or otherwise known-integrated) before the watermark. */
  readonly delivered: readonly string[];
  /** Gap proofs for predecessors that expired/were deleted before the watermark. */
  readonly gapProofs: readonly GapProof[];
}

export type PendingStatus = "pending" | "released" | "delivered";

export interface PendingRecord {
  readonly message: CausalMessage;
  /** Deps not yet delivered and not provably skippable. */
  readonly missing: ReadonlySet<string>;
  status: PendingStatus;
}

export type RecoveryAction = "released" | "still-pending" | "delivered";

/** One affected message plus the recovery step taken for it. */
export interface RecoveryStep {
  readonly messageId: string;
  readonly action: RecoveryAction;
  readonly skippedDeps: readonly string[];
  readonly remainingDeps: readonly string[];
  readonly detail: string;
}

/** Audit report: which messages were affected and what to do about them. */
export interface RecoveryReport {
  readonly boundaryId: string | null;
  readonly steps: readonly RecoveryStep[];
}

export interface ReceiveResult {
  readonly status: "delivered" | "pending" | "duplicate";
  readonly missing: readonly string[];
}

export interface BoundaryCheck {
  readonly ok: boolean;
  readonly errors: readonly string[];
}

export interface GapHandlerOptions {
  /** Logical clock source; defaults to 0 and is advanced by observe(). */
  now?: () => number;
}
