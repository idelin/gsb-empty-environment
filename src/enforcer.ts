import {
  FenceAdjudicator,
  FenceViolationError,
  type FenceContext,
  type FenceDecision,
} from './adjudicator.ts';
import { type FencePolicy } from './policy.ts';

/**
 * Structural DOM shapes. The library never imports a DOM lib so it stays
 * usable from plain Node tests with hand-written mock windows.
 */

export interface AncestorOriginsLike {
  readonly length: number;
  item(index: number): string | null;
  contains(origin: string): boolean;
  [index: number]: string;
}

export interface FenceDocument {
  referrer: string;
  ancestorOrigins?: AncestorOriginsLike;
}

export interface XhrLike {
 open(method: string, url: string, ...rest: unknown[]): void;
}

export interface FenceWindow {
  location: { href: string; origin: string };
  document: FenceDocument;
  postMessage(...args: unknown[]): unknown;
  fetch?(input: string | { url?: string; href?: string }, init?: unknown): Promise<unknown>;
  navigator?: { sendBeacon?(url: string, data?: unknown): boolean };
  XMLHttpRequest?: { prototype: XhrLike };
  WebSocket?: new (url: string, protocols?: unknown) => unknown;
  EventSource?: new (url: string, config?: unknown) => unknown;
  [key: string]: unknown;
}

export interface InstallOptions {
  /** Pre-built adjudicator; a policy can be passed to {@link installFence} instead. */
  adjudicator?: FenceAdjudicator;
  /** Receives every denied decision (telemetry / audit logging). */
  onViolation?: (decision: FenceDecision, context: FenceContext) => void;
}

export interface FenceEnforcement {
  adjudicator: FenceAdjudicator;
  /** Remove every trap and restore the original globals. */
  uninstall(): void;
}

const INSTALL_MARKER: unique symbol = Symbol.for('fence.enforcement.installed');

function emptyAncestorOrigins(): AncestorOriginsLike {
  return {
    length: 0,
    item: () => null,
    contains: () => false,
  } as AncestorOriginsLike;
}

/**
 * Install fence enforcement on the given window.
 *
 * The same object is used for both the global and the simulated fenced frame
 * in tests; in production the embedding shell passes the fenced frame's
 * content window. Calling twice on one window throws to avoid double traps.
 */
export function installFence(
  win: FenceWindow,
  policy: FencePolicy | FenceAdjudicator,
  options: InstallOptions = {},
): FenceEnforcement {
  if ((win as Record<symbol, unknown>)[INSTALL_MARKER]) {
    throw new Error('Fence enforcement is already installed on this window');
  }

  const adjudicator =
    options.adjudicator ??
    (policy instanceof FenceAdjudicator ? policy : new FenceAdjudicator(policy));
  const onViolation = options.onViolation;
  const restores: Array<() => void> = [];

  const enforce = (context: FenceContext): FenceDecision => {
    const decision = adjudicator.adjudicate(context);
    if (!decision.allowed) {
      onViolation?.(decision, context);
      throw new FenceViolationError(decision, context);
    }
    return decision;
  };

  const defineTrap = (
    target: object,
    key: string | symbol,
    descriptor: PropertyDescriptor,
  ): void => {
    const original = Object.getOwnPropertyDescriptor(target, key);
    Object.defineProperty(target, key, descriptor);
    restores.push(() => {
      if (original) {
        Object.defineProperty(target, key, original);
      } else {
        delete (target as Record<string | symbol, unknown>)[key];
      }
    });
  };

  // --- Outer address: top/parent never disclose the embedder. -----------
  // They resolve to a self-like proxy: reading navigation info yields the
  // fence itself, and postMessage is routed through the adjudicator which
  // denies top/parent communication.
  const outerWindowProxy: unknown = new Proxy(win, {
    get(target, prop, receiver) {
      if (prop === 'postMessage') {
        return (...args: unknown[]) => {
          enforce({
            kind: 'cross-context-message',
            targetContext: 'top',
            targetUrl: typeof args[1] === 'string' ? args[1] : undefined,
          });
          return Reflect.get(target, 'postMessage', receiver).apply(target, args);
        };
      }
      const value = Reflect.get(target, prop, receiver);
      return typeof value === 'function' ? value.bind(target) : value;
    },
  });

  const outerDescriptor: PropertyDescriptor = {
    configurable: true,
    get: () => outerWindowProxy,
  };
  defineTrap(win, 'top', outerDescriptor);
  defineTrap(win, 'parent', outerDescriptor);
  defineTrap(win, 'frameElement', { configurable: true, get: () => null });

  // --- document.referrer / ancestorOrigins must not leak outer URL. -----
  defineTrap(win.document, 'referrer', {
    configurable: true,
    get: () => '',
  });
  defineTrap(win.document, 'ancestorOrigins', {
    configurable: true,
    get: () => emptyAncestorOrigins(),
  });

  // --- postMessage to top/parent via explicit target arguments. ---------
  const originalPostMessage = win.postMessage.bind(win);
  win.postMessage = ((...args: unknown[]) => {
    enforce({ kind: 'cross-context-message', targetContext: 'top' });
    return originalPostMessage(...args);
  }) as FenceWindow['postMessage'];
  restores.push(() => {
    win.postMessage = originalPostMessage;
  });

  const requestUrl = (input: unknown): string => {
    if (typeof input === 'string') {
      return input;
    }
    if (input && typeof input === 'object') {
      const record = input as { url?: string; href?: string };
      return record.url ?? record.href ?? '';
    }
    return '';
  };

  // --- fetch. -----------------------------------------------------------
  if (typeof win.fetch === 'function') {
    const originalFetch = win.fetch.bind(win);
    win.fetch = ((input: string | { url?: string; href?: string }, init?: unknown) => {
      enforce({
        kind: 'network-request',
        channel: 'fetch',
        targetUrl: requestUrl(input),
      });
      return originalFetch(input, init);
    }) as FenceWindow['fetch'];
    restores.push(() => {
      win.fetch = originalFetch;
    });
  }

  // --- XMLHttpRequest. --------------------------------------------------
  if (win.XMLHttpRequest) {
    const proto = win.XMLHttpRequest.prototype;
    const originalOpen = proto.open;
    proto.open = function open(this: XhrLike, method: string, url: string, ...rest: unknown[]) {
      enforce({ kind: 'network-request', channel: 'xmlhttprequest', targetUrl: url });
      return originalOpen.call(this, method, url, ...rest);
    };
    restores.push(() => {
      proto.open = originalOpen;
    });
  }

  // --- navigator.sendBeacon. -------------------------------------------
  if (win.navigator && typeof win.navigator.sendBeacon === 'function') {
    const navigator = win.navigator;
    const originalBeacon = navigator.sendBeacon.bind(navigator);
    navigator.sendBeacon = (url: string, data?: unknown) => {
      enforce({ kind: 'network-request', channel: 'sendbeacon', targetUrl: url });
      return originalBeacon(url, data);
    };
    restores.push(() => {
      navigator.sendBeacon = originalBeacon;
    });
  }

  // --- WebSocket / EventSource constructors. ----------------------------
  const wrapConstructor = (
    key: 'WebSocket' | 'EventSource',
    channel: string,
  ): void => {
    const Original = win[key];
    if (typeof Original !== 'function') {
      return;
    }
    const Wrapped = function (this: unknown, url: string, extra?: unknown) {
      enforce({ kind: 'network-request', channel, targetUrl: url });
      return new (Original as new (u: string, p?: unknown) => unknown)(url, extra);
    } as unknown as typeof Original;
    win[key] = Wrapped as FenceWindow[typeof key];
    restores.push(() => {
      win[key] = Original;
    });
  };
  wrapConstructor('WebSocket', 'websocket');
  wrapConstructor('EventSource', 'eventsource');

  Object.defineProperty(win, INSTALL_MARKER, {
    configurable: true,
    value: true,
  });
  restores.push(() => {
    delete (win as Record<symbol, unknown>)[INSTALL_MARKER];
  });

  return {
    adjudicator,
    uninstall: () => {
      for (const restore of restores.reverse()) {
        restore();
      }
    },
  };
}
