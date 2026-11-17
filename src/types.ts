/**
 * A snapshot of the document that initiated a clipboard action.
 *
 * `tabId` identifies the browsing context, `documentId` identifies the
 * concrete document object, and `generation` is a monotonic counter owned by
 * the host. All three are part of a permission binding.
 */
export interface DocumentBinding {
  readonly tabId: string;
  readonly documentId: string;
  readonly generation: number;
}

/**
 * A snapshot of host focus. `generation` must change whenever focus moves,
 * including when it moves away and then back to the same tab.
 */
export interface FocusBinding {
  readonly generation: number;
  readonly tabId: string | null;
}

/**
 * One transient user activation. `activationId` is opaque to this module;
 * `expiresAt` is an epoch-millisecond deadline.
 */
export interface ActivationBinding {
  readonly activationId: string;
  readonly expiresAt: number;
}

/**
 * The host side of the clipboard action component.
 *
 * Implementations must return fresh snapshots. A host that only exposes a
 * boolean "is focused" or "has activation" flag cannot provide the binding
 * guarantees required by this module.
 */
export interface ClipboardEnvironment {
  getDocument(): DocumentBinding;
  getFocus(): FocusBinding;
  getUserActivation(): ActivationBinding | null;
  now(): number;
}

export type ClipboardOperation = "read" | "write";

/**
 * A permission request is an immutable record of everything that authorized
 * the action at request time. It intentionally contains no clipboard payload.
 */
export interface ClipboardPermissionRequest {
  readonly requestId: string;
  /**
   * Opaque, single-use value that the permission granter must echo back. It
   * prevents a callback for one request from satisfying another one.
   */
  readonly token: string;
  readonly operation: ClipboardOperation;
  readonly tabId: string;
  readonly documentId: string;
  readonly documentGeneration: number;
  readonly focusGeneration: number;
  readonly activationId: string;
  readonly activationExpiresAt: number;
  readonly requestedAt: number;
}

export type ClipboardPermissionDecision =
  | {
      readonly status: "granted";
      readonly requestId: string;
      readonly token: string;
    }
  | {
      readonly status: "denied";
      readonly requestId: string;
      readonly token?: string;
      readonly reason?: string;
    }
  | {
      readonly status: "cancelled";
      readonly requestId: string;
      readonly token?: string;
      readonly reason?: string;
    };

/**
 * The permission prompt / host grantor. A decision may be delivered
 * asynchronously and in any order relative to other requests.
 */
export interface ClipboardPermissionGranter {
  request(
    request: ClipboardPermissionRequest,
  ): Promise<ClipboardPermissionDecision> | ClipboardPermissionDecision;

  /**
   * Optional notification for an action cancelled while the prompt is open.
   * The implementation must only affect `requestId`; it must never clear a
   * global or another tab's permission.
   */
  cancel?(requestId: string, token: string): void;
}

/**
 * Structural subset of `navigator.clipboard`. Both methods may be synchronous
 * or asynchronous; invocation is always gated by the permission checks.
 */
export interface SystemClipboard {
  readText(): string | Promise<string>;
  writeText(text: string): void | Promise<void>;
}

/** Structural subset of `AbortSignal`, usable in browsers and Node. */
export interface AbortSignalLike {
  readonly aborted: boolean;
  addEventListener?(
    type: "abort",
    listener: () => void,
    options?: { once?: boolean },
  ): void;
  removeEventListener?(type: "abort", listener: () => void): void;
}

export type ClipboardFailureReason =
  | "cancelled"
  | "inactive"
  | "activation-expired"
  | "activation-changed"
  | "navigation"
  | "focus-change"
  | "not-focused"
  | "permission-denied"
  | "permission-mismatch"
  | "permission-error"
  | "environment-error"
  | "clipboard-error";

export interface ClipboardActionSuccess<T> {
  readonly ok: true;
  readonly value: T;
  readonly requestId: string;
}

export interface ClipboardActionFailure {
  readonly ok: false;
  readonly reason: ClipboardFailureReason;
  readonly requestId?: string;
  readonly error?: unknown;
}

export type ClipboardActionResult<T> =
  | ClipboardActionSuccess<T>
  | ClipboardActionFailure;

export interface ClipboardActionDependencies {
  readonly environment: ClipboardEnvironment;
  readonly permissions: ClipboardPermissionGranter;
  readonly systemClipboard: SystemClipboard;
  /** Defaults to `environment.now`. */
  readonly now?: () => number;
  /** Defaults to a collision-resistant host-independent identifier. */
  readonly createRequestId?: () => string;
}

export interface ClipboardActionOptions {
  readonly signal?: AbortSignalLike | null;
}
