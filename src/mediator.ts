import {
  assertEmptyAllowCredentials,
  assertPolicyAllowsGet,
  assertTrustedFrame,
  GateError,
} from './gate.ts';
import type {
  AbortControllerConstructor,
  BrowserEnvironment,
  ConditionalGetInput,
  CredentialGetRequest,
  PublicKeyCredentialLike,
  PublicKeyCredentialRequestOptionsLike,
} from './types.ts';

interface PendingRequest {
  readonly generation: number;
  readonly controller: AbortController;
}

const NAVIGATION_EVENTS = ['pagehide', 'beforeunload'] as const;

/**
 * Owns the page-wide conditional (passkey autofill) request.
 *
 * Invariants enforced here:
 *  - the environment gate (trusted frame + permissions policy + empty
 *    allowCredentials) runs before any platform call;
 *  - at most one conditional request is in flight at any time;
 *  - starting a new request aborts the previous one;
 *  - responses that arrive after a navigation or replacement are discarded,
 *    so late platform answers can never resolve a stale caller.
 */
export class ConditionalPasskeyMediator {
  private readonly env: BrowserEnvironment;
  private pending: PendingRequest | null = null;
  private generation = 0;
  private readonly onNavigation = (): void => {
    this.handleNavigation();
  };
  private navigationListenersBound = false;

  constructor(env: BrowserEnvironment) {
    this.env = env;
  }

  get inFlight(): boolean {
    return this.pending !== null;
  }

  get currentGeneration(): number {
    return this.generation;
  }

  /**
   * Launches a conditional navigator.credentials.get() after the environment
   * gate passes. Rejects with a NotAllowedError DOMException if the gate
   * blocks the call, or an AbortError when superseded/aborted/navigated away.
   */
  async get(input: ConditionalGetInput): Promise<PublicKeyCredentialLike> {
    if (!input || typeof input !== 'object' || !('publicKey' in input)) {
      throw new GateError('TypeError', 'A "publicKey" options object is required');
    }
    if (!this.env.navigator.credentials?.get) {
      throw new GateError(
        'NotAllowedError',
        'WebAuthn conditional mediation is not supported in this environment',
      );
    }

    assertTrustedFrame(this.env);
    await assertPolicyAllowsGet(this.env);
    assertEmptyAllowCredentials(input.publicKey);

    const previous = this.pending;
    if (previous) {
      this.abortPending(previous, 'Superseded by a new conditional mediation request');
    }

    const controller = new this.env.AbortController();
    const generation = (this.generation += 1);

    const request: PendingRequest = {
      generation,
      controller,
    };
    this.pending = request;
    this.bindNavigationListeners();

    if (input.signal) {
      if (input.signal.aborted) {
        this.clearIfCurrent(request);
        throw abortError('The conditional mediation request was aborted');
      }
      input.signal.addEventListener('abort', () => {
        if (this.pending === request) {
          controller.abort(input.signal?.reason);
        }
      });
    }

    const publicKey: PublicKeyCredentialRequestOptionsLike = {
      ...input.publicKey,
      allowCredentials: [],
    };
    const platformRequest: CredentialGetRequest = {
      mediation: 'conditional',
      publicKey,
      signal: controller.signal,
    };

    try {
      const credential = await this.env.navigator.credentials.get(platformRequest);
      if (generation !== this.generation || this.pending !== request) {
        // A late response: navigation or a newer request already invalidated
        // this call. Never hand a stale credential to a stale caller.
        throw abortError('Discarding a late conditional mediation response after navigation');
      }
      return credential;
    }
    finally {
      this.clearIfCurrent(request);
    }
  }

  /** Aborts the in-flight request, if any. */
  abort(): void {
    if (this.pending) {
      this.abortPending(this.pending, 'The conditional mediation request was aborted');
    }
  }

  /**
   * Called on navigation (pagehide/beforeunload). The platform request is
   * aborted and the generation advances, so any response that still lands
   * afterwards is treated as stale rather than delivered to page code.
   */
  handleNavigation(): void {
    if (this.pending) {
      this.abortPending(this.pending, 'Navigation aborted the conditional mediation request');
    }
  }

  dispose(): void {
    this.abort();
    this.unbindNavigationListeners();
  }

  private abortPending(request: PendingRequest, reason: string): void {
    request.controller.abort(abortError(reason));
    this.generation += 1;
    if (this.pending === request) {
      this.pending = null;
    }
  }

  private clearIfCurrent(request: PendingRequest): void {
    if (this.pending === request) {
      this.pending = null;
    }
  }

  private bindNavigationListeners(): void {
    if (this.navigationListenersBound) {
      return;
    }
    const add = this.env.window.addEventListener;
    if (add) {
      for (const event of NAVIGATION_EVENTS) {
        add.call(this.env.window, event, this.onNavigation);
      }
      this.navigationListenersBound = true;
    }
  }

  private unbindNavigationListeners(): void {
    if (!this.navigationListenersBound) {
      return;
    }
    const remove = this.env.window.removeEventListener;
    if (remove) {
      for (const event of NAVIGATION_EVENTS) {
        remove.call(this.env.window, event, this.onNavigation);
      }
    }
    this.navigationListenersBound = false;
  }
}

export function abortError(message: string): DOMException {
  return new DOMException(message, 'AbortError');
}

export function defaultEnvironment(): BrowserEnvironment {
  const global = globalThis as unknown as {
    window?: BrowserEnvironment['window'];
    document?: BrowserEnvironment['document'];
    navigator?: BrowserEnvironment['navigator'];
    AbortController: AbortControllerConstructor;
  };
  return {
    window: global.window as BrowserEnvironment['window'],
    document: global.document as BrowserEnvironment['document'],
    navigator: global.navigator as BrowserEnvironment['navigator'],
    AbortController: global.AbortController,
  };
}

let defaultMediator: ConditionalPasskeyMediator | null = null;

/** Page-script entry point: one process-wide conditional mediator. */
export function getConditionalPasskeyMediator(): ConditionalPasskeyMediator {
  if (!defaultMediator) {
    defaultMediator = new ConditionalPasskeyMediator(defaultEnvironment());
  }
  return defaultMediator;
}
