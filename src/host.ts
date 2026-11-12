import { VirtualClock } from './clock.ts';
import {
  UserActivationService,
} from './activation.ts';
import type {
  ActivationToken,
  TrustedUserEvent,
  UserEventType,
} from './activation.ts';

export interface HostWindow {
  readonly windowId: string;
  documentId: string;
  readonly openedFrom: string | null;
}

export interface OpenWindowResult {
  readonly window: HostWindow | null;
  readonly blockedReason: string | null;
  readonly tokenId: string | null;
}

let eventSequence = 0;
let windowSequence = 0;

/**
 * Minimal browser-like host built on top of the activation state machine.
 * `window.open` is the canonical consuming operation: the first call after a
 * real user gesture wins the token atomically, every further call without a
 * new gesture is blocked as a popup.
 */
export class SimulatedBrowserHost {
  readonly clock: VirtualClock;
  readonly activation: UserActivationService;
  readonly #windows = new Map<string, HostWindow>();

  constructor(ttlMs: number) {
    this.clock = new VirtualClock();
    this.activation = new UserActivationService({ ttlMs, clock: this.clock });
  }

  createWindow(documentId: string): HostWindow {
    this.activation.registerDocument(documentId);
    const windowId = `window-${++windowSequence}`;
    const windowObject: HostWindow = {
      windowId,
      documentId,
      openedFrom: null,
    };
    this.#windows.set(windowId, windowObject);
    return windowObject;
  }

  getWindow(windowId: string): HostWindow {
    const windowObject = this.#windows.get(windowId);
    if (!windowObject) {
      throw new Error(`unknown window "${windowId}"`);
    }
    return windowObject;
  }

  dispatchEvent(
    documentId: string,
    type: UserEventType,
    isTrusted: boolean,
  ): TrustedUserEvent {
    const event: TrustedUserEvent = {
      type,
      isTrusted,
      eventId: `event-${++eventSequence}`,
      timestamp: this.clock.now(),
    };
    this.activation.notifyUserEvent(documentId, event);
    return event;
  }

  /** Consuming operation: at most one window per activation token. */
  openWindowFrom(openerWindowId: string): OpenWindowResult {
    const opener = this.getWindow(openerWindowId);
    const result = this.activation.tryConsume(opener.documentId);
    if (!result.ok) {
      return {
        window: null,
        blockedReason: result.reason,
        tokenId: result.token?.id ?? null,
      };
    }
    const newDocumentId = `doc-${++windowSequence}`;
    this.activation.registerDocument(newDocumentId);
    const newWindowId = `window-${++windowSequence}`;
    const newWindow: HostWindow = {
      windowId: newWindowId,
      documentId: newDocumentId,
      openedFrom: openerWindowId,
    };
    this.#windows.set(newWindowId, newWindow);
    return {
      window: newWindow,
      blockedReason: null,
      tokenId: result.token.id,
    };
  }

  /** Non-consuming check, e.g. gating UI affordances without spending a gesture. */
  canOpenPopup(windowId: string): boolean {
    return this.activation.hasTransientActivation(this.getWindow(windowId).documentId);
  }

  navigate(windowId: string, newDocumentId: string): void {
    const windowObject = this.getWindow(windowId);
    this.activation.navigate(windowObject.documentId);
    this.activation.registerDocument(newDocumentId);
    windowObject.documentId = newDocumentId;
  }

  transferAttempt(
    callerDocumentId: string,
    token: ActivationToken,
  ): boolean {
    return this.activation.tryConsumeToken(callerDocumentId, token).ok;
  }
}
