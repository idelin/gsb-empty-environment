import assert from 'node:assert/strict';
import type {
  AncestorOriginList,
  BrowserEnvironment,
  CredentialGetRequest,
  LocationLike,
  PublicKeyCredentialLike,
  WindowLike,
} from '../src/types.ts';

export interface RecordedCall {
  readonly request: CredentialGetRequest;
  resolve(credential: PublicKeyCredentialLike): void;
  reject(error: Error): void;
}

export interface FakeOptions {
  readonly origin?: string;
  readonly topOrigin?: string;
  readonly ancestors?: readonly string[];
  readonly policyAllows?: boolean | undefined;
  readonly permissionState?: 'granted' | 'denied' | 'prompt' | undefined;
  readonly includeCredentialsApi?: boolean;
  readonly includePermissionsApi?: boolean;
  readonly includePermissionsPolicy?: boolean;
  readonly includeAncestorOrigins?: boolean;
}

class FakeAncestorOrigins implements AncestorOriginList {
  private readonly origins: readonly string[];

  constructor(origins: readonly string[]) {
    this.origins = origins;
  }

  get length(): number {
    return this.origins.length;
  }

  item(index: number): string | null {
    return this.origins[index] ?? null;
  }

  contains(origin: string): boolean {
    return this.origins.includes(origin);
  }

  get [Symbol.toStringTag](): string {
    return 'DOMStringList';
  }
}

function indexedOrigins(origins: readonly string[]): AncestorOriginList {
  const list = new FakeAncestorOrigins(origins);
  return new Proxy(list, {
    get(target, prop, receiver) {
      if (typeof prop === 'string') {
        const index = Number(prop);
        if (Number.isInteger(index)) {
          return target.item(index);
        }
      }
      return Reflect.get(target, prop, receiver);
    },
  }) as AncestorOriginList;
}

/**
 * Builds a BrowserEnvironment fake. Every navigator.credentials.get() call is
 * parked on `calls` so tests can resolve/reject it late, inspect the signal
 * state, and assert which paths actually reached the browser interface.
 */
export function buildFakeEnv(options: FakeOptions = {}): {
  env: BrowserEnvironment;
  calls: RecordedCall[];
  listeners: Map<string, Set<() => void>>;
  emit(event: string): void;
} {
  const origin = options.origin ?? 'https://login.example.com';
  const topOrigin = options.topOrigin ?? origin;
  const ancestors = options.ancestors ?? (origin === topOrigin ? [] : [topOrigin]);
  const isTopLevel = ancestors.length === 0;
  const includeAncestorOrigins = options.includeAncestorOrigins ?? true;
  const calls: RecordedCall[] = [];
  const listeners = new Map<string, Set<() => void>>();

  const location: LocationLike = {
    origin,
    hostname: new URL(origin).hostname,
    ...(includeAncestorOrigins ? { ancestorOrigins: indexedOrigins(ancestors) } : {}),
  };

  const topWindow = {
    self: null as unknown as WindowLike,
    top: null as unknown as WindowLike,
    parent: null as unknown as WindowLike,
    location: {
      origin: topOrigin,
      hostname: new URL(topOrigin).hostname,
      ancestorOrigins: indexedOrigins([]),
    },
  };
  topWindow.self = topWindow;
  topWindow.top = topWindow;
  topWindow.parent = topWindow;

  const frame: WindowLike = isTopLevel
    ? topWindow
    : {
        self: null as unknown as WindowLike,
        top: topWindow,
        parent: topWindow,
        location,
      };
  if (!isTopLevel) {
    (frame as { self: WindowLike }).self = frame;
  }

  frame.addEventListener = (type: string, listener: () => void): void => {
    const set = listeners.get(type) ?? new Set<() => void>();
    set.add(listener);
    listeners.set(type, set);
  };
  frame.removeEventListener = (type: string, listener: () => void): void => {
    listeners.get(type)?.delete(listener);
  };

  const includePolicy = options.includePermissionsPolicy ?? true;
  const document = {
    location,
    ...(includePolicy
      ? {
          permissionsPolicy: {
            allowsFeature: (feature: string): boolean => {
              assert.equal(feature, 'publickey-credentials-get');
              return options.policyAllows ?? true;
            },
          },
        }
      : {}),
  };

  const credentials = {
    get: (request: CredentialGetRequest): Promise<PublicKeyCredentialLike> =>
      new Promise<PublicKeyCredentialLike>((resolve, reject) => {
        const recorded: RecordedCall = { request, resolve, reject };
        calls.push(recorded);
        // Mirror the platform: an aborted signal rejects the get() promise
        // with an AbortError, even while the request is still pending.
        if (request.signal.aborted) {
          reject(new DOMException('The operation was aborted.', 'AbortError'));
          return;
        }
        request.signal.addEventListener(
          'abort',
          () => {
            reject(new DOMException('The operation was aborted.', 'AbortError'));
          },
          { once: true },
        );
      }),
  };

  const includePermissions = options.includePermissionsApi ?? false;
  const navigator = {
    ...(options.includeCredentialsApi === false ? {} : { credentials }),
    ...(includePermissions
      ? {
          permissions: {
            query: async (): Promise<{ state: string }> => ({
              state: options.permissionState ?? 'granted',
            }),
          },
        }
      : {}),
  };

  const env: BrowserEnvironment = {
    window: frame,
    document: document as BrowserEnvironment['document'],
    navigator: navigator as BrowserEnvironment['navigator'],
    AbortController,
  };

  return {
    env,
    calls,
    listeners,
    emit(event: string): void {
      listeners.get(event)?.forEach((listener) => listener());
    },
  };
}

export function challenge(): Uint8Array {
  return new Uint8Array([1, 2, 3, 4]);
}

export function publicKey(overrides: Record<string, unknown> = {}) {
  return {
    challenge: challenge(),
    rpId: 'example.com',
    allowCredentials: [],
    ...overrides,
  };
}

export function settleMicrotasks(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}
