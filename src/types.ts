/**
 * Public data model for the BFCache freeze/restore state machine.
 *
 * All identifiers are deliberately opaque strings.  The host never derives
 * security decisions from URL parsing; callers must supply the already
 * normalized top-level site and origin chosen by the browser process.
 */

export type MaybePromise<T> = T | Promise<T>;

export type AuthEpochState = "Active" | "Revoked";

/**
 * An event signed by the account authentication service with Ed25519.
 *
 * `signature` is base64 (standard or URL-safe).  The signature covers the
 * canonical payload produced by `canonicalAuthEpochPayload`, which contains
 * exactly the four security-relevant fields plus a domain separator and
 * version.  `alg` and `keyID` are routing metadata and are not trusted for
 * authorization decisions.
 */
export interface AuthEpochEvent {
  readonly type?: "AuthEpoch";
  readonly accountID: string;
  readonly authSessionID: string;
  readonly epoch: number;
  readonly state: AuthEpochState;
  /** Optional routing metadata.  Only Ed25519 is accepted by this host. */
  readonly alg?: "Ed25519";
  readonly keyID?: string;
  readonly signature: string;
  /**
   * Optional diagnostic only.  It is intentionally not part of the signed
   * payload and must never influence a state transition.
   */
  readonly reason?: string;
}

/**
 * The host's view of the authentication authority.
 *
 * `readCurrent` is called again for every BFCache restore.  A cached answer is
 * never sufficient to unfreeze a document.
 */
export interface AuthEpochAuthority {
  verify(event: AuthEpochEvent): MaybePromise<boolean>;
  readCurrent(
    authSessionID: string,
  ): MaybePromise<AuthEpochEvent | undefined>;
}

/** The partition pieces covered by a document's identity. */
export interface Partition {
  readonly topLevelSite: string;
  readonly origin: string;
  readonly browserSessionID: string;
}

/**
 * Capability policy for one partition.
 *
 * A policy change must allocate a new generation.  The fingerprint is also
 * compared so a buggy authority which mutates permissions without bumping the
 * generation cannot silently restore a stricter document.
 */
export interface PolicySnapshot {
  readonly generation: number;
  readonly allowSensitiveDom: boolean;
  readonly allowNetwork: boolean;
  readonly allowFormSubmission: boolean;
  readonly allowMessagePort: boolean;
  readonly label?: string;
}

export interface PolicyAuthority {
  read(partition: Partition): MaybePromise<PolicySnapshot>;
}

/**
 * Stable identity of one document instance.
 *
 * `documentGeneration` is monotonic for the lifetime of the browser session.
 * It is the fencing token attached to every delayed response, message and
 * promise callback.
 */
export interface DocumentIdentity extends Partition {
  readonly documentGeneration: number;
  readonly authSessionID: string;
  readonly accountID: string;
  readonly authEpoch: number;
  readonly policyGeneration: number;
}

export type DocumentState =
  | "live"
  | "frozen"
  | "restoring"
  | "discarded"
  | "disposed"
  /**
   * The session was revoked while the document was visible.  The document is
   * still on screen, but every sensitive capability and every queued task is
   * denied until a fresh navigation replaces it.
   */
  | "locked";

export type DocumentCapability =
  | "sensitive-dom"
  | "network"
  | "form-submission"
  | "message-port"
  | "timer"
  | "promise-callback";

export type FreezeKind =
  | "network-response"
  | "message"
  | "promise-callback"
  | "timer";

export type DiscardReason =
  | "auth-session-revoked"
  | "auth-session-missing"
  | "auth-epoch-mismatch"
  | "auth-epoch-rollback"
  | "auth-state-not-active"
  | "account-mismatch"
  | "signature-invalid"
  | "policy-generation-mismatch"
  | "policy-fingerprint-mismatch"
  | "top-level-site-mismatch"
  | "origin-mismatch"
  | "browser-session-mismatch"
  | "document-generation-mismatch"
  | "authorization-lost"
  | "no-frozen-document"
  | "authority-read-failed";

export interface AuditEvent {
  readonly sequence: number;
  readonly time: number;
  readonly type: string;
  readonly result: "allowed" | "denied" | "dropped" | "info";
  readonly documentGeneration?: number;
  readonly historyEntryID?: string;
  readonly authSessionID?: string;
  readonly authEpoch?: number;
  /** True when the effect happened while the bound session was not Active. */
  readonly afterRevocation?: boolean;
  /** A successful sensitive effect after revocation, or a stale-gen effect. */
  readonly violation?: boolean;
  readonly detail?: Readonly<Record<string, unknown>>;
}

export interface AuthEpochResult {
  readonly accepted: boolean;
  readonly duplicate?: boolean;
  readonly reason?: string;
  readonly authSessionID: string;
  readonly epoch?: number;
  readonly state?: AuthEpochState;
}

export interface PolicyUpdateResult {
  readonly accepted: boolean;
  readonly generation: number;
  readonly reason?: string;
}

export interface PagehideOptions {
  /** Defaults to the currently visible history entry. */
  readonly historyEntryID?: string;
  /** `true` asks the host to put the document in the in-memory BFCache. */
  readonly persisted?: boolean;
}

export interface PagehideResult {
  readonly accepted: boolean;
  readonly historyEntryID: string;
  readonly documentGeneration: number;
  readonly persisted: boolean;
  readonly state: DocumentState;
  readonly snapshotBytes?: number;
  readonly reason?: string;
}

export interface PageshowRequest {
  readonly historyEntryID: string;
  /** Defaults to `true`.  A non-persisted show is a normal fresh load. */
  readonly persisted?: boolean;
  /**
   * The partition in which the traversal is being shown.  The browser process
   * must supply this; it is not inferred from the old document.
   */
  readonly currentPartition: Partition;
}

export interface ResumeResult {
  readonly outcome:
    | "resumed"
    | "discarded"
    | "fresh-navigation"
    | "ignored";
  readonly historyEntryID: string;
  readonly previousDocumentGeneration?: number;
  readonly documentGeneration?: number;
  readonly reason?: DiscardReason | string;
  readonly details?: readonly string[];
  readonly droppedDeferred?: number;
}

export interface DocumentView {
  readonly identity: DocumentIdentity;
  readonly state: DocumentState;
  readonly historyEntryID: string;
  readonly authorized: boolean;
  readonly authorizationLost: boolean;
  readonly policyStale: boolean;
  readonly hasInMemorySnapshot: boolean;
  readonly deferredEvents: number;
  readonly deferredKinds: readonly string[];
  readonly pendingRequests: number;
  readonly openPorts: number;
  readonly activeTimers: number;
}

export interface NavigationRequest {
  readonly historyEntryID: string;
  readonly topLevelSite: string;
  readonly origin: string;
  /** Defaults to the host's browserSessionID. */
  readonly browserSessionID?: string;
  readonly authSessionID: string;
  /**
   * Test/bootstrap shortcut.  When omitted the authority is read.  When
   * supplied it still must agree with the signed authority answer.
   */
  readonly authEpoch?: number;
  /** Optional initial sensitive DOM content. */
  readonly sensitiveDom?: Readonly<Record<string, string>>;
}

export interface ReadResult {
  readonly ok: boolean;
  readonly value?: string;
  readonly reason?: string;
}

export interface FormSubmitResult {
  readonly ok: boolean;
  readonly reason?: string;
}

export interface NetworkRequestView {
  readonly id: string;
  readonly documentGeneration: number;
  readonly url: string;
  readonly state: "pending" | "completed" | "delivered" | "dropped";
}

export interface DeliveryResult {
  readonly accepted: boolean;
  readonly queued: boolean;
  readonly deferred: boolean;
  readonly reason?: string;
}

export interface RunOptions {
  readonly maxTasks?: number;
}
