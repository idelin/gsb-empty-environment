import type { GateEnvironment, WindowLocationLike } from './types.ts';

interface BrowserGlobal {
  window?: BrowserWindow;
  navigator?: {
    credentials?: GateEnvironment['credentials'];
    permissions?: GateEnvironment['permissions'];
  };
  AbortController?: GateEnvironment['AbortController'];
}

interface BrowserWindow {
  isSecureContext?: boolean;
  location: WindowLocationLike['location'] & {
    ancestorOrigins?: ReadonlyArray<string>;
  };
  self: BrowserWindow;
  top: BrowserWindow;
  addEventListener(type: string, listener: () => void): void;
}

function toFrame(window: BrowserWindow): GateEnvironment['frame'] {
  const selfOrigin = window.location.origin;

  const selfWindow: WindowLocationLike = {
    location: { href: window.location.href, origin: selfOrigin },
  };
  const topWindow: WindowLocationLike = {
    get location() {
      // Reading a cross-origin top frame's location throws a SecurityError,
      // exactly like the native `window.top.location` access.
      const href = window.top.location.href;
      const origin = window.top.location.origin;
      return { href, origin };
    },
  };

  return {
    self: selfWindow,
    top: topWindow,
    ancestorOrigins: window.location.ancestorOrigins
      ? [...window.location.ancestorOrigins]
      : undefined,
    isSecureContext: window.isSecureContext,
  };
}

/**
 * Builds a {@link GateEnvironment} backed by the real browser globals.
 * Throws only when `AbortController` is missing, which no supported browser
 * lacks; missing optional surfaces become `null`/`undefined` and the gate
 * fails closed on them.
 */
export function createBrowserEnvironment(): GateEnvironment {
  const globalScope = globalThis as unknown as BrowserGlobal;
  const window = globalScope.window;
  if (window === undefined) {
    throw new Error('No browser window is available; pass a test environment.');
  }
  if (globalScope.AbortController === undefined) {
    throw new Error('AbortController is required but not available.');
  }

  return {
    frame: toFrame(window),
    permissions: globalScope.navigator?.permissions ?? null,
    credentials: globalScope.navigator?.credentials ?? null,
    AbortController: globalScope.AbortController,
    navigationEvents: window,
  };
}
