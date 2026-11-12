import {
  CONDITIONAL_MEDIATION,
  PUBLICKEY_CREDENTIALS_GET_PERMISSION,
  type ConditionalCredentialRequest,
  type GateEnvironment,
  GateError,
  type PublicKeyCredentialRequestOptionsLike,
} from './types.ts';
import { isSameSite } from './sameSite.ts';

interface ActiveRequest {
  generation: number;
  controller: { abort(reason?: unknown): void };
  navigated: boolean;
}

function abortError(reason: string): DOMException {
  return new DOMException(reason, 'AbortError');
}

/**
 * Environment gate that must run before any conditional (autofill)
 * `navigator.credentials.get()` call.
 *
 * Rules enforced:
 *  1. secure context, top-level frame or same-site nested frame;
 *  2. the `publickey-credentials-get` permissions policy grants access;
 *  3. `allowCredentials` is forced to an empty list;
 *  4. at most one conditional request per page — a new call aborts the old;
 *  5. a page navigation aborts the in-flight request and discards any
 *     response that settles afterwards, reporting an AbortError instead.
 */
export class ConditionalMediationGate {
  readonly #env: GateEnvironment;
  #active: ActiveRequest | null = null;
  #generation = 0;
  #navigationListenerAttached = false;

  constructor(env: GateEnvironment) {
    this.#env = env;
  }

  /**
   * Runs every gate check and starts a single conditional mediation request.
   * Rejects with {@link GateError} when the environment is not allowed; in
   * that case `navigator.credentials.get` is never touched.
   */
  async conditionalGet(
    publicKey: PublicKeyCredentialRequestOptionsLike,
  ): Promise<unknown> {
    this.#assertFrameTrust();
    await this.#assertPermissionsPolicy();

    const credentials = this.#env.credentials;
    if (credentials === null) {
      throw new GateError(
        'credentials-unavailable',
        'navigator.credentials is not available in this environment.',
      );
    }
    this.#assertEmptyAllowCredentials(publicKey);

    this.#abortActiveRequest('superseded by a newer conditional request');

    const controller = new this.#env.AbortController();
    const request: ActiveRequest = {
      generation: ++this.#generation,
      controller,
      navigated: false,
    };
    this.#active = request;
    this.#attachNavigationListener();

    const credentialRequest: ConditionalCredentialRequest = {
      publicKey: { ...publicKey, allowCredentials: [] },
      mediation: CONDITIONAL_MEDIATION,
      signal: controller.signal,
    };

    let result: unknown;
    try {
      result = await credentials.get(credentialRequest);
    } catch (error) {
      if (request.navigated) {
        // The page is navigating: surface a clean abort instead of a platform
        // error that arrives after teardown began.
        throw abortError('conditional request aborted by page navigation');
      }
      throw error;
    }

    if (request.navigated) {
      // Late response after navigation: discard the credential rather than
      // handing stale material to the page being torn down.
      throw abortError('conditional response discarded after navigation');
    }

    if (this.#active?.generation === request.generation) {
      this.#active = null;
    }
    return result;
  }

  /**
   * Aborts the currently in-flight conditional request, if any. Safe to call
   * from page teardown logic.
   */
  abort(reason = 'aborted by page'): void {
    this.#abortActiveRequest(reason);
  }

  #assertFrameTrust(): void {
    const { frame } = this.#env;

    if (frame.isSecureContext === false) {
      throw new GateError(
        'insecure-context',
        'Conditional mediation requires a secure context.',
      );
    }

    const selfOrigin = frame.self.location.origin;
    if (selfOrigin === 'null' || selfOrigin === '') {
      throw new GateError(
        'opaque-origin',
        'Frames with an opaque origin cannot run conditional mediation.',
      );
    }

    let topOrigin: string;
    try {
      topOrigin = frame.top.location.origin;
    } catch {
      // Cross-origin `window.top.location` access throws: this is an embedded
      // frame, so the answer has to come from the ancestor list.
      this.#assertSameSiteAncestors(selfOrigin);
      return;
    }

    if (topOrigin === selfOrigin) {
      // Top-level document, or a same-origin nested frame whose top is
      // readable.
      return;
    }

    this.#assertSameSiteAncestors(selfOrigin);
  }

  #assertSameSiteAncestors(selfOrigin: string): void {
    const ancestors = this.#env.frame.ancestorOrigins;
    if (ancestors === undefined || ancestors.length === 0) {
      throw new GateError(
        'cross-site-frame',
        'Embedding frame ancestry cannot be verified; denying by default.',
      );
    }

    for (const ancestorOrigin of ancestors) {
      if (!isSameSite(selfOrigin, ancestorOrigin)) {
        throw new GateError(
          'cross-site-frame',
          `Cross-site ancestor ${ancestorOrigin} may not invoke conditional mediation.`,
        );
      }
    }
  }

  async #assertPermissionsPolicy(): Promise<void> {
    const { permissions } = this.#env;
    if (permissions === null) {
      // Fail closed: without a way to observe the Permissions Policy state we
      // cannot prove `publickey-credentials-get` is allowed.
      throw new GateError(
        'permission-query-unavailable',
        'Permissions query interface is unavailable; denying by default.',
      );
    }

    let state: 'granted' | 'denied' | 'prompt';
    try {
      ({ state } = await permissions.query({
        name: PUBLICKEY_CREDENTIALS_GET_PERMISSION,
      }));
    } catch {
      throw new GateError(
        'permission-policy-blocked',
        'publickey-credentials-get permission query failed.',
      );
    }

    if (state !== 'granted') {
      throw new GateError(
        'permission-policy-blocked',
        `publickey-credentials-get is not granted (state: ${state}).`,
      );
    }
  }

  #assertEmptyAllowCredentials(
    publicKey: PublicKeyCredentialRequestOptionsLike,
  ): void {
    const allowCredentials = publicKey.allowCredentials;
    if (allowCredentials !== undefined && allowCredentials.length > 0) {
      throw new GateError(
        'non-empty-allow-credentials',
        'Conditional mediation requires an empty allowCredentials list.',
      );
    }
  }

  #abortActiveRequest(reason: string): void {
    const active = this.#active;
    if (active === null) {
      return;
    }
    this.#active = null;
    active.controller.abort(reason);
  }

  #attachNavigationListener(): void {
    if (this.#navigationListenerAttached) {
      return;
    }
    const emitter = this.#env.navigationEvents ?? null;
    if (emitter === null) {
      return;
    }
    this.#navigationListenerAttached = true;
    emitter.addEventListener('pagehide', () => {
      if (this.#active !== null) {
        this.#active.navigated = true;
        this.#active.controller.abort('page navigation');
      }
    });
  }
}
