import type {
  AbortControllerLike,
  ConditionalCredentialRequest,
  GateEnvironment,
  PermissionsLike,
} from '../src/types.ts';

export class Deferred<T> {
  readonly promise: Promise<T>;
  #resolve!: (value: T) => void;
  #reject!: (error: unknown) => void;
  #settled = false;

  constructor() {
    this.promise = new Promise<T>((resolve, reject) => {
      this.#resolve = resolve;
      this.#reject = reject;
    });
  }

  resolve(value: T): void {
    if (this.#settled) {
      return;
    }
    this.#settled = true;
    this.#resolve(value);
  }

  reject(error: unknown): void {
    if (this.#settled) {
      return;
    }
    this.#settled = true;
    this.#reject(error);
  }
}

class FakeAbortController implements AbortControllerLike {
  readonly signal: {
    aborted: boolean;
    reason?: unknown;
    onabort?: () => void;
  } = { aborted: false };

  abort(reason?: unknown): void {
    if (this.signal.aborted) {
      return;
    }
    this.signal.aborted = true;
    this.signal.reason = reason;
    this.signal.onabort?.();
  }
}

export class FakeEmitter {
  readonly #listeners = new Map<string, Array<() => void>>();

  addEventListener(type: string, listener: () => void): void {
    const bucket = this.#listeners.get(type);
    if (bucket === undefined) {
      this.#listeners.set(type, [listener]);
    } else {
      bucket.push(listener);
    }
  }

  emit(type: string): void {
    this.#listeners.get(type)?.forEach((listener) => listener());
  }
}

/**
 * Mimics the browser credentials container: every get() stays pending until
 * the test resolves it, and aborting the signal rejects with AbortError,
 * matching the real WebAuthn contract.
 */
export class FakeCredentialsContainer {
  readonly calls: ConditionalCredentialRequest[] = [];
  readonly pending: Array<Deferred<unknown>> = [];

  get(options: ConditionalCredentialRequest): Promise<unknown> {
    this.calls.push(options);
    const deferred = new Deferred<unknown>();
    this.pending.push(deferred);

    const signal = options.signal as FakeAbortController['signal'];
    if (signal.aborted) {
      deferred.reject(
        new DOMException('The operation was aborted.', 'AbortError'),
      );
    } else {
      signal.onabort = () => {
        deferred.reject(
          new DOMException('The operation was aborted.', 'AbortError'),
        );
      };
    }

    return deferred.promise;
  }
}

export type PermissionMode =
  | 'granted'
  | 'denied'
  | 'prompt'
  | 'throws'
  | 'unavailable';

export interface FakeEnvOptions {
  selfOrigin: string;
  /** `'throw'` simulates a SecurityError on cross-origin top access. */
  topOrigin?: string | 'throw';
  ancestorOrigins?: string[];
  isSecureContext?: boolean;
  permission?: PermissionMode;
  credentialsAvailable?: boolean;
}

export interface BuiltFakeEnv {
  env: GateEnvironment;
  credentials: FakeCredentialsContainer;
  emitter: FakeEmitter;
  permissionQueries: Array<{ name: string }>;
}

export function buildFakeEnv(options: FakeEnvOptions): BuiltFakeEnv {
  const emitter = new FakeEmitter();
  const credentials = new FakeCredentialsContainer();
  const permissionQueries: Array<{ name: string }> = [];

  const frame = {
    self: {
      location: { href: options.selfOrigin + '/login', origin: options.selfOrigin },
    },
    top: {
      get location() {
        if (options.topOrigin === 'throw') {
          throw new DOMException(
            'Blocked a frame with origin from accessing a cross-origin frame.',
            'SecurityError',
          );
        }
        const origin = options.topOrigin ?? options.selfOrigin;
        return { href: origin + '/', origin };
      },
    },
    ancestorOrigins: options.ancestorOrigins,
    isSecureContext: options.isSecureContext ?? true,
  };

  let permissions: PermissionsLike | null = null;
  const permission = options.permission ?? 'granted';
  if (permission !== 'unavailable') {
    permissions = {
      async query(descriptor: { name: string }) {
        permissionQueries.push(descriptor);
        if (permission === 'throws') {
          throw new DOMException('permission query failed', 'NotAllowedError');
        }
        return { state: permission as 'granted' | 'denied' | 'prompt' };
      },
    };
  }

  const env: GateEnvironment = {
    frame,
    permissions,
    credentials:
      options.credentialsAvailable === false ? null : credentials,
    AbortController: FakeAbortController,
    navigationEvents: emitter,
  };

  return { env, credentials, emitter, permissionQueries };
}
