/**
 * Minimal DOM-independent type surface used by the conditional mediation
 * gate. The real browser satisfies every member; tests provide fakes.
 */

export const CONDITIONAL_MEDIATION = 'conditional' as const;

export const PUBLICKEY_CREDENTIALS_GET_PERMISSION =
  'publickey-credentials-get' as const;

export interface AbortSignalLike {
  readonly aborted: boolean;
}

export interface AbortControllerLike {
  readonly signal: AbortSignalLike;
  abort(reason?: unknown): void;
}

export interface PublicKeyCredentialRequestOptionsLike {
  challenge: BufferSource | string;
  rpId?: string;
  userVerification?: string;
  timeout?: number;
  /**
   * Conditional mediation must always run against an empty allow list so the
   * browser surfaces all passkeys that match the RP instead of probing a
   * caller-supplied credential id.
   */
  allowCredentials?: ReadonlyArray<{ id: BufferSource | string }>;
  extensions?: Record<string, unknown>;
}

/** Shape actually handed to `navigator.credentials.get`. */
export interface ConditionalCredentialRequest {
  publicKey: PublicKeyCredentialRequestOptionsLike;
  mediation: typeof CONDITIONAL_MEDIATION;
  signal: AbortSignalLike;
}

export interface CredentialsContainerLike {
  get(options: ConditionalCredentialRequest): Promise<unknown>;
}

export interface PermissionsLike {
  query(descriptor: {
    name: string;
  }): Promise<{ state: 'granted' | 'denied' | 'prompt' }>;
}

/**
 * Window-like frame surface. `top` may throw on cross-origin access, mirroring
 * real `window.top.location` behavior.
 */
export interface FrameLike {
  self: WindowLocationLike;
  top: WindowLocationLike;
  /** Exposed via `window.location.ancestorOrigins` (Chromium). */
  ancestorOrigins?: ReadonlyArray<string>;
  /** Exposed via `window.isSecureContext`. */
  isSecureContext?: boolean;
}

export interface WindowLocationLike {
  location: { href: string; origin: string };
}

export interface EventTargetLike {
  addEventListener(type: string, listener: () => void): void;
}

export interface GateEnvironment {
  frame: FrameLike;
  permissions: PermissionsLike | null;
  credentials: CredentialsContainerLike | null;
  AbortController: new () => AbortControllerLike;
  /** Defaults to the real `window` when present; tests pass a fake emitter. */
  navigationEvents?: EventTargetLike | null;
}

export type GateErrorCode =
  | 'insecure-context'
  | 'cross-site-frame'
  | 'opaque-origin'
  | 'permission-policy-blocked'
  | 'permission-query-unavailable'
  | 'credentials-unavailable'
  | 'non-empty-allow-credentials';

export class GateError extends Error {
  readonly code: GateErrorCode;

  constructor(code: GateErrorCode, message: string) {
    super(message);
    this.name = 'ConditionalMediationGateError';
    this.code = code;
  }
}
