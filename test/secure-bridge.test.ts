import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  BRIDGE_CHANNEL,
  type BridgeEvent,
  exposeBridge,
  registerBridge,
} from '../index.ts';
import { BridgeError } from '../src/errors.ts';
import { s } from '../src/schema.ts';
import type { IpcMainLike } from '../src/bridge.ts';

const APP_ORIGIN = 'https://app.example';
const ADMIN_ORIGIN = 'https://admin.example';

const profileSchema = s.object({
  name: s.field(s.string()),
  age: s.optional(s.number()),
});

class MockIpcMain implements IpcMainLike {
  readonly #handlers = new Map<
    string,
    (event: unknown, payload: unknown) => unknown
  >();

  handle(
    channel: string,
    listener: (event: unknown, payload: unknown) => unknown,
  ): void {
    if (this.#handlers.has(channel)) {
      throw new Error(`handler already registered for ${channel}`);
    }
    this.#handlers.set(channel, listener);
  }

  removeHandler(channel: string): void {
    this.#handlers.delete(channel);
  }

  dispatch(event: BridgeEvent, payload: unknown): unknown {
    const handler = this.#handlers.get(BRIDGE_CHANNEL);
    if (!handler) {
      throw new Error('bridge handler is not registered');
    }
    return handler(event, payload);
  }
}

class MockIpcRenderer {
  readonly calls: Array<{ channel: string; payload: unknown }> = [];

  invoke(channel: string, payload: unknown): Promise<unknown> {
    this.calls.push({ channel, payload });
    return Promise.resolve({ ok: true });
  }
}

class MockContextBridge {
  exposed: { apiKey: string; api: unknown } | undefined;

  exposeInMainWorld(apiKey: string, api: unknown): void {
    this.exposed = { apiKey, api };
  }
}

function frameEvent(origin: string): BridgeEvent {
  return { senderFrame: { url: `${origin}/index.html` } };
}

function createRegistry() {
  const ipcMain = new MockIpcMain();
  let handlerCalls = 0;

  registerBridge(
    ipcMain,
    {
      echo: {
        params: [s.string()],
        handler: (_event, message: string) => {
          handlerCalls += 1;
          return message;
        },
      },
      saveProfile: {
        params: [profileSchema],
        handler: (_event, profile: unknown) => {
          handlerCalls += 1;
          return { saved: true, profile };
        },
      },
      adminAction: {
        params: [],
        allow: ADMIN_ORIGIN,
        handler: () => {
          handlerCalls += 1;
          return 'admin-ok';
        },
      },
    },
    APP_ORIGIN,
  );

  return {
    ipcMain,
    handlerCalls: () => handlerCalls,
  };
}

async function rejectsWithCode(
  promise: Promise<unknown>,
  code: string,
): Promise<void> {
  await assert.rejects(promise, (error: unknown) => {
    return error instanceof BridgeError && error.code === code;
  });
}

test('valid call reaches the handler with validated arguments', async () => {
  const { ipcMain } = createRegistry();
  const result = await ipcMain.dispatch(frameEvent(APP_ORIGIN), {
    method: 'echo',
    args: ['hello'],
  });
  assert.equal(result, 'hello');
});

test('unregistered channel name is rejected before the handler runs', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'doEvil',
      args: [],
    }) as Promise<unknown>,
    'UNREGISTERED_METHOD',
  );

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'constructor',
      args: [],
    }) as Promise<unknown>,
    'UNREGISTERED_METHOD',
  );

  assert.equal(handlerCalls(), 0);
});

test('malformed envelopes are rejected', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'echo',
      args: 'not-an-array',
    }) as Promise<unknown>,
    'SCHEMA_MISMATCH',
  );

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), null) as Promise<unknown>,
    'SCHEMA_MISMATCH',
  );

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'echo',
      args: [],
    }) as Promise<unknown>,
    'SCHEMA_MISMATCH',
  );

  assert.equal(handlerCalls(), 0);
});

test('function arguments are rejected', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'echo',
      args: [() => 'malicious'],
    }) as Promise<unknown>,
    'NON_CLONEABLE_ARGUMENT',
  );

  assert.equal(handlerCalls(), 0);
});

test('getter properties are rejected without ever invoking the getter', async () => {
  const { ipcMain, handlerCalls } = createRegistry();
  let getterInvoked = false;

  const evil = {
    name: 'ok',
    get age(): number {
      getterInvoked = true;
      return 99;
    },
  };

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'saveProfile',
      args: [evil],
    }) as Promise<unknown>,
    'ACCESSOR_PROPERTY',
  );

  assert.equal(getterInvoked, false);
  assert.equal(handlerCalls(), 0);
});

test('nested getter inside an array is rejected', async () => {
  const { ipcMain } = createRegistry();
  const evil = [{ get trap() {
    return 1;
  } }];

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'echo',
      args: [evil],
    }) as Promise<unknown>,
    'ACCESSOR_PROPERTY',
  );
});

test('__proto__ and constructor pollution keys are rejected', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  const polluted = JSON.parse('{"__proto__":{"isAdmin":true},"name":"x"}');
  assert.equal(Object.getPrototypeOf(polluted) === Object.prototype, true);

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'saveProfile',
      args: [polluted],
    }) as Promise<unknown>,
    'FORBIDDEN_KEY',
  );

  const nested = {
    name: 'x',
    extra: JSON.parse('{"constructor":{"prototype":{"pwned":true}}}'),
  };

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'saveProfile',
      args: [nested],
    }) as Promise<unknown>,
    'FORBIDDEN_KEY',
  );

  assert.equal(handlerCalls(), 0);
  assert.equal(({} as Record<string, unknown>).pwned, undefined);
});

test('hostile proxies cannot smuggle functions or pollution keys', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  const functionProxy = new Proxy(
    { name: 'x' },
    {
      get(target, prop) {
        if (prop === 'name') {
          return () => 'smuggled function';
        }
        return Reflect.get(target, prop);
      },
    },
  );

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'saveProfile',
      args: [functionProxy],
    }) as Promise<unknown>,
    'NON_CLONEABLE_ARGUMENT',
  );

  const pollutionProxy = new Proxy(
    {},
    {
      ownKeys: () => ['__proto__'],
      getOwnPropertyDescriptor: () => ({
        enumerable: true,
        configurable: true,
        writable: true,
        value: { isAdmin: true },
      }),
    },
  );

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'saveProfile',
      args: [pollutionProxy],
    }) as Promise<unknown>,
    'FORBIDDEN_KEY',
  );

  assert.equal(handlerCalls(), 0);
});

test('unknown object fields fail schema validation', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  await rejectsWithCode(
    ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'saveProfile',
      args: [{ name: 'x', isAdmin: true }],
    }) as Promise<unknown>,
    'SCHEMA_MISMATCH',
  );

  assert.equal(handlerCalls(), 0);
});

test('senderFrame URL is checked against the default origin policy', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  await rejectsWithCode(
    ipcMain.dispatch({ senderFrame: { url: 'https://evil.example/page' } }, {
      method: 'echo',
      args: ['x'],
    }) as Promise<unknown>,
    'FORBIDDEN_ORIGIN',
  );

  await rejectsWithCode(
    ipcMain.dispatch({}, {
      method: 'echo',
      args: ['x'],
    }) as Promise<unknown>,
    'FORBIDDEN_ORIGIN',
  );

  await rejectsWithCode(
    ipcMain.dispatch({ senderFrame: { url: 'not a url' } }, {
      method: 'echo',
      args: ['x'],
    }) as Promise<unknown>,
    'FORBIDDEN_ORIGIN',
  );

  assert.equal(handlerCalls(), 0);
});

test('per-method origin policy is enforced', async () => {
  const { ipcMain, handlerCalls } = createRegistry();

  const forbidden = ipcMain.dispatch(frameEvent(APP_ORIGIN), {
      method: 'adminAction',
      args: [],
    });
  assert.equal(forbidden instanceof Promise, true);
  await rejectsWithCode(forbidden as Promise<unknown>, 'FORBIDDEN_ORIGIN');

  const result = await ipcMain.dispatch(frameEvent(ADMIN_ORIGIN), {
    method: 'adminAction',
    args: [],
  });
  assert.equal(result, 'admin-ok');
  assert.equal(handlerCalls(), 1);
});

test('handler receives a detached clone, never the renderer object', async () => {
  const { ipcMain } = createRegistry();
  const input = { name: 'before' };

  const result = (await ipcMain.dispatch(frameEvent(APP_ORIGIN), {
    method: 'saveProfile',
    args: [input],
  })) as { profile: { name: string } };

  assert.notEqual(result.profile, input);
  assert.deepEqual(result.profile, { name: 'before' });

  result.profile.name = 'mutated';
  assert.equal(input.name, 'before');
});

test('preload exposes only registered methods on a frozen API', () => {
  const contextBridge = new MockContextBridge();
  const ipcRenderer = new MockIpcRenderer();

  const api = exposeBridge(
    contextBridge,
    ipcRenderer,
    {
      echo: { params: [s.string()], handler: () => undefined },
      saveProfile: { params: [profileSchema], handler: () => undefined },
    },
  );

  assert.deepEqual(Object.keys(api).sort(), ['echo', 'saveProfile']);
  assert.equal(Object.isFrozen(api), true);
  assert.equal(contextBridge.exposed?.apiKey, 'secureBridge');
  assert.equal(contextBridge.exposed?.api, api);
  assert.equal(typeof (api as Record<string, unknown>).invoke, 'undefined');
  assert.equal(
    Object.prototype.hasOwnProperty.call(api, 'constructor'),
    false,
  );

  const callResult = api.echo('hi');
  assert.equal(ipcRenderer.calls.length, 1);
  assert.equal(ipcRenderer.calls[0]?.channel, BRIDGE_CHANNEL);
  assert.deepEqual(ipcRenderer.calls[0]?.payload, {
    method: 'echo',
    args: ['hi'],
  });
  return callResult;
});

test('registering a reserved method name fails fast', () => {
  const ipcMain = new MockIpcMain();
  assert.throws(
    () =>
      registerBridge(ipcMain, {
        ['__proto__']: { handler: () => undefined },
      }),
    (error: unknown) =>
      error instanceof BridgeError && error.code === 'FORBIDDEN_KEY',
  );
  assert.throws(
    () =>
      registerBridge(ipcMain, {
        constructor: { handler: () => undefined },
      }),
    (error: unknown) =>
      error instanceof BridgeError && error.code === 'FORBIDDEN_KEY',
  );
});
