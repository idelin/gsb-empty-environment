import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createBrowserEnvironment } from '../src/browserEnv.ts';
import { ConditionalMediationGate } from '../src/gate.ts';

interface FakeWindowOptions {
  origin: string;
  topOrigin?: string | 'throw';
  ancestorOrigins?: string[];
  isSecureContext?: boolean;
  permissionState?: 'granted' | 'denied';
}

function installBrowserGlobals(options: FakeWindowOptions): {
  remove(): void;
  calls: Array<{ mediation: string; signal: { aborted: boolean } }>;
} {
  const calls: Array<{ mediation: string; signal: { aborted: boolean } }> = [];

  const location = {
    href: options.origin + '/login',
    origin: options.origin,
    ancestorOrigins: options.ancestorOrigins,
  };

  const fakeWindow = {
    isSecureContext: options.isSecureContext ?? true,
    location,
    self: null as unknown,
    top: null as unknown,
    addEventListener(): void {},
  };

  fakeWindow.self = fakeWindow;
  if (options.topOrigin === 'throw') {
    fakeWindow.top = {
      get location(): never {
        throw new DOMException('cross-origin access', 'SecurityError');
      },
    };
  } else {
    const topOrigin = options.topOrigin ?? options.origin;
    fakeWindow.top = {
      location: { href: topOrigin + '/', origin: topOrigin },
    };
  }

  const globalScope = globalThis as Record<string, unknown>;
  const hadWindow = Object.prototype.hasOwnProperty.call(
    globalScope,
    'window',
  );
  const hadNavigator = Object.prototype.hasOwnProperty.call(
    globalScope,
    'navigator',
  );
  const previousWindow = globalScope.window;
  const previousNavigatorDescriptor = Object.getOwnPropertyDescriptor(
    globalScope,
    'navigator',
  );

  Object.defineProperty(globalScope, 'window', {
    value: fakeWindow,
    configurable: true,
    writable: true,
  });
  Object.defineProperty(globalScope, 'navigator', {
    value: {
      permissions: {
        async query() {
          return { state: options.permissionState ?? 'granted' };
        },
      },
      credentials: {
        async get(request: {
          mediation: string;
          signal: { aborted: boolean };
        }) {
          calls.push(request);
          return { id: 'from-browser-env' };
        },
      },
    },
    configurable: true,
    writable: true,
  });

  return {
    calls,
    remove(): void {
      if (hadWindow) {
        Object.defineProperty(globalScope, 'window', {
          value: previousWindow,
          configurable: true,
          writable: true,
        });
      } else {
        delete globalScope.window;
      }
      if (hadNavigator && previousNavigatorDescriptor !== undefined) {
        Object.defineProperty(
          globalScope,
          'navigator',
          previousNavigatorDescriptor,
        );
      } else {
        delete globalScope.navigator;
      }
    },
  };
}

test('browser environment drives a real conditional get on a top frame', async () => {
  const installed = installBrowserGlobals({
    origin: 'https://login.example.com',
    topOrigin: 'https://login.example.com',
  });
  try {
    const env = createBrowserEnvironment();
    const gate = new ConditionalMediationGate(env);
    const result = await gate.conditionalGet({ challenge: 'c' });

    assert.equal(installed.calls.length, 1);
    assert.equal(installed.calls[0].mediation, 'conditional');
    assert.deepEqual(result, { id: 'from-browser-env' });
  } finally {
    installed.remove();
  }
});

test('browser environment maps a cross-origin top SecurityError to ancestor checks', async () => {
  const installed = installBrowserGlobals({
    origin: 'https://ads.third-party.test',
    topOrigin: 'throw',
    ancestorOrigins: ['https://login.example.com'],
  });

  const env = createBrowserEnvironment();
  const gate = new ConditionalMediationGate(env);
  await assert.rejects(
    gate.conditionalGet({ challenge: 'c' }),
    (error: unknown) =>
      error instanceof Error && error.message.includes('Cross-site ancestor'),
  );
  assert.equal(installed.calls.length, 0);
  installed.remove();
});

test('browser environment accepts a same-site embedded frame via ancestorOrigins', async () => {
  const installed = installBrowserGlobals({
    origin: 'https://auth.example.com',
    topOrigin: 'throw',
    ancestorOrigins: ['https://www.example.com'],
  });

  const env = createBrowserEnvironment();
  const gate = new ConditionalMediationGate(env);
  await gate.conditionalGet({ challenge: 'c' });
  assert.equal(installed.calls.length, 1);
  installed.remove();
});

test('browser environment factory throws outside a browser', () => {
  const globalScope = globalThis as Record<string, unknown>;
  const hadWindow = Object.prototype.hasOwnProperty.call(
    globalScope,
    'window',
  );
  const previousWindow = globalScope.window;
  delete globalScope.window;
  try {
    assert.throws(() => createBrowserEnvironment(), /No browser window/);
  } finally {
    if (hadWindow) {
      globalScope.window = previousWindow;
    }
  }
});
