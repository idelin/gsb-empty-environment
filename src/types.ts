/**
 * Shared domain types for the durable action-authorization package.
 *
 * The protocol is deliberately explicit about durable state: every value
 * below that is suffixed with `Record` is what gets persisted through the
 * per-write compare-and-swap storage and can be read back independently.
 */

/** A field path expressed as canonical segments (no alias tokens). */
export type FieldPath = readonly string[];

/** Path aliases map a single token used by a client to a canonical path. */
export type AliasMap = Readonly<Record<string, readonly string[]>>;

/** Canonicalized field binding: canonical path paired with its stable value. */
export interface FieldBinding {
  readonly path: FieldPath;
  readonly value: unknown;
}

/** Signed authorization for one confirmation action. */
export interface GrantedAction {
  /** Stable action identifier, unique within the grant. */
  readonly actionId: string;
  /** Human/machine readable meaning of what the action is allowed to do. */
  readonly meaning: string;
  /** Authorized maximum number of action-level consumptions (>= 1). */
  readonly maxActionConsumptions: number;
  /** Ordered, potentially interdependent sub-operations. */
  readonly subOperations: readonly SubOperationSpec[];
}

/** One business sub-operation declared inside an action. */
export interface SubOperationSpec {
  /**
   * Sub-operation identifier. It is ONLY unique within its parent action;
   * two different actions may legitimately use the same id.
   */
  readonly subId: string;
  /** Stable meaning of the business effect. */
  readonly meaning: string;
  /**
   * References to sibling `subId` values that must finish first.
   * The IDs are scoped to the parent action, never globally.
   */
  readonly dependsOn: readonly string[];
  /** Per sub-operation maximum number of business effects (>= 1). */
  readonly maxSubConsumptions: number;
}

/** Full signed grant. The signature covers every binding below. */
export interface SignedGrant {
  readonly grantId: string;
  /** Authorized acting principal. */
  readonly subject: string;
  /** Window/session the grant is bound to. */
  readonly windowSessionId: string;
  /** Optimistic object/version token the form was read at. */
  readonly objectVersion: string;
  /** Canonical digest of the bound field paths and values. */
  readonly formDigest: string;
  readonly actions: readonly GrantedAction[];
  /** Absolute epoch-millisecond expiry. */
  readonly expiresAt: number;
  /** Signature key identifier. */
  readonly kid: string;
  /** HMAC-SHA256 over the canonical grant payload, base64url. */
  readonly signature: string;
}

/** A sub-operation requested at confirmation time (reordering is allowed). */
export interface SubOperationRequest {
  readonly subId: string;
  /** Caller-supplied business argument handed to the business gateway. */
  readonly payload: unknown;
}

/** A confirmation submission. */
export interface SubmitRequest {
  readonly grant: SignedGrant;
  /** Must equal the grant subject; windows cannot switch principals. */
  readonly subject: string;
  /** Must equal the grant window session. */
  readonly windowSessionId: string;
  /** Must equal the grant object version. */
  readonly objectVersion: string;
  /** Raw form that will be canonicalized and compared with the grant digest. */
  readonly form: Readonly<Record<string, unknown>>;
  readonly actionId: string;
  /**
   * Client-generated idempotency key for one confirmation attempt.
   * A network proxy retrying the same logical confirmation must keep it;
   * genuinely separate confirmations use fresh keys.
   */
  readonly idemKey: string;
  /** May arrive in any order; dependencies are resolved server-side. */
  readonly subOperations: readonly SubOperationRequest[];
}

/** Result statuses a retry caller must distinguish. */
export const SubmitStatus = {
  /** The full action and every required sub-operation are durably done. */
  Success: "SUCCESS",
  /**
   * Some required sub-operations executed and are proven, but at least one
   * mandatory sub-operation has not executed. Never claim full success.
   */
  Partial: "PARTIAL",
  /** A permanent rejection: policy, grant, quota or validation error. */
  Failed: "FAILED",
  /**
   * Another window/attempt currently owns the preparation record for the
   * same authorization and action; this attempt never wrote business data.
   */
  Conflict: "CONFLICT",
  /**
   * A crash/time-out happened after a write point whose outcome is unknown.
   * Reconcile before retrying; do not assume the effect happened or not.
   */
  Unknown: "UNKNOWN",
  /** A transient storage fault before any durable decision; safe to retry. */
  Retryable: "RETRYABLE",
} as const;
export type SubmitStatus = (typeof SubmitStatus)[keyof typeof SubmitStatus];

/** Stable machine codes behind {@link SubmitResult.code}. */
export const SubmitCode = {
  Ok: "OK",
  BadSignature: "BAD_SIGNATURE",
  GrantExpired: "GRANT_EXPIRED",
  SubjectMismatch: "SUBJECT_MISMATCH",
  WindowMismatch: "WINDOW_MISMATCH",
  VersionMismatch: "VERSION_MISMATCH",
  FormDigestMismatch: "FORM_DIGEST_MISMATCH",
  UnknownPath: "UNKNOWN_PATH",
  UnknownAction: "UNKNOWN_ACTION",
  UnknownSubOperation: "UNKNOWN_SUB_OPERATION",
  DuplicateSubOperation: "DUPLICATE_SUB_OPERATION",
  DependencyCycle: "DEPENDENCY_CYCLE",
  FieldRevoked: "FIELD_REVOKED",
  ActionLimitExhausted: "ACTION_LIMIT_EXHAUSTED",
  SubLimitExhausted: "SUB_LIMIT_EXHAUSTED",
  ClaimConflict: "CLAIM_CONFLICT",
  ReconcileRequired: "RECONCILE_REQUIRED",
  TransientFault: "TRANSIENT_FAULT",
} as const;
export type SubmitCode = (typeof SubmitCode)[keyof typeof SubmitCode];

/**
 * Durable authorization snapshot proof. Written before any business write,
 * independently readable at `proof/snapshot/<grantId>/<actionId>/<idemKey>`.
 */
export interface SnapshotRecord {
  readonly kind: "snapshot";
  readonly grantId: string;
  readonly actionId: string;
  readonly idemKey: string;
  readonly subject: string;
  readonly windowSessionId: string;
  readonly objectVersion: string;
  readonly formDigest: string;
  /** Canonical field bindings actually authorized (post alias resolution). */
  readonly bindings: readonly FieldBinding[];
  /** Policy version used for the decision-time re-validation. */
  readonly policyVersion: number;
  /** Effective action cap after policy shrinking was applied. */
  readonly effectiveActionCap: number;
  readonly createdAt: number;
}

/**
 * Preparation/claim record. Its atomic insert is the single point that
 * serializes competing windows on the same (grant, action) authorization.
 */
export interface ClaimRecord {
  readonly kind: "claim";
  readonly grantId: string;
  readonly actionId: string;
  readonly idemKey: string;
  readonly owner: string;
  readonly policyVersion: number;
  readonly createdAt: number;
  readonly updatedAt: number;
}

/** Proof that one sub-operation business effect actually happened. */
export interface SubOperationRecord {
  readonly kind: "sub";
  readonly grantId: string;
  readonly actionId: string;
  readonly idemKey: string;
  readonly subId: string;
  /** Monotonic consumption ordinal for this (grant, action, subId). */
  readonly subConsumptionNo: number;
  /** Idempotency token handed to the business gateway for this effect. */
  readonly effectId: string;
  /** Gateway-returned opaque result of the actual write. */
  readonly result: unknown;
  readonly executedAt: number;
}

/** The final durable decision; written once via compare-and-swap. */
export interface DecisionRecord {
  readonly kind: "decision";
  readonly grantId: string;
  readonly actionId: string;
  readonly idemKey: string;
  readonly outcome: "SUCCESS" | "FAILED";
  readonly code: SubmitCode;
  /** Every required sub-operation id, in dependency order. */
  readonly requiredSubIds: readonly string[];
  /** Sub-operations proven executed at decision time. */
  readonly executedSubIds: readonly string[];
  /** Individually readable consumption proofs referenced by the decision. */
  readonly subProofKeys: readonly string[];
  readonly snapshotKey: string;
  readonly decidedAt: number;
}

/** Result returned by a submission or reconciliation call. */
export interface SubmitResult {
  readonly status: SubmitStatus;
  readonly code: SubmitCode;
  readonly message: string;
  /** Durable proof keys available for independent read-back (may be partial). */
  readonly proofKeys: readonly string[];
  readonly decision?: DecisionRecord;
  /** Present when at least one sub-effect is proven but the action is not whole. */
  readonly executedSubIds?: readonly string[];
  /** Present when status is PARTIAL and tells what is still mandatory. */
  readonly pendingSubIds?: readonly string[];
}

/** Read model of how much of an authorization has actually been consumed. */
export interface ConsumptionReport {
  readonly grantId: string;
  readonly actionId: string;
  readonly actionConsumptions: number;
  readonly activeClaim?: { readonly idemKey: string; readonly owner: string };
  readonly subConsumptions: Readonly<Record<string, number>>;
  readonly decisions: readonly DecisionRecord[];
}
