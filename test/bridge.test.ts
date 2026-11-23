import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  ChannelRegistry,
  SenderRejectedError,
  UnregisteredChannelError,
  UnsafeArgumentError,
  ValidationError,
  createBridgeApi,
  prepareHandlerArgument,
  registerBridgeHandlers,
  safeClone,
  scanValue,
  validate,
  type IpcMainEventLike,
  type IpcMainLike,
  type IpcRendererLike,
  type SenderFrameLike,
} from '../src/index.js';

const ALLOWED_ORIGIN = 'https://app.example.com';

const profileSchema = {
  type: 'object',
  properties: {
    name: { type: 'string' },
    age: { type: 'integer', optional: true },
    tags: { type: 'array', items: { type: 'string' }, maxItems: 3 },
  },
  additionalProperties: false,
} as const;

function buildRegistry(): ChannelRegistry {
  return new ChannelRegistry([
    { channel: 'app:save-profile', exposedName: 'saveProfile', schema: profileSchema },
    {
      channel: 'app:ping',
      exposedName: 'ping',
      schema: { type: 'object', properties: { echo: { type: 'string' } } },
    },
  ]);
}

function makeFrame(url: string, isMainFrame = true): SenderFrameLike {
  const senderFrame: SenderFrameLike = { url, origin: new URL(url).origin, top: null };
  senderFrame.top = isMainFrame ? senderFrame : null;
  return senderFrame;
}

function makeEvent(url = ALLOWED_ORIGIN, isMainFrame = true): IpcMainEventLike {
  return { senderFrame: makeFrame(url, isMainFrame) };
}

type MainListener = (event: IpcMainEventLike, ...args: unknown[]) => unknown;

function createIpcPair() {
  const listeners = new Map<string, MainListener>();

  const ipcMain: IpcMainLike = {
    handle(channel: string, listener: MainListener): void {
      listeners.set(channel, listener);
    },
    removeHandler(channel: string): void {
      listeners.delete(channel);
    },
  };

  const ipcRenderer: IpcRendererLike = {
    async invoke(channel: string, ...args: unknown[]): Promise<unknown> {
      const listener = listeners.get(channel);
      if (listener === undefined) {
        throw new UnregisteredChannelError(channel);
      }
      return listener(makeEvent(), ...args);
    },
  };

  return {
    ipcMain,
    ipcRenderer,
    listeners,
    hasHandler(channel: string): boolean {
      return listeners.has(channel);
    },
  };
}

describe('ChannelRegistry', () => {
  it('rejects unregistered channel lookups', () => {
    const registry = buildRegistry();
    assert.equal(registry.has('app:save-profile'), true);
    assert.equal(registry.has('system:shell'), false);
    assert.throws(() => registry.get('system:shell'), UnregisteredChannelError);
  });

  it('rejects duplicate and reserved registrations', () => {
    const registry = buildRegistry();
    assert.throws(
      () =>
        registry.register({
          channel: 'app:save-profile',
          exposedName: 'other',
          schema: { type: 'any' },
        }),
      /already registered/,
    );
    assert.throws(
      () =>
        new ChannelRegistry([
          { channel: 'evil:__proto__', exposedName: 'x', schema: { type: 'any' } },
        ]),
      /invalid channel name/,
    );
    assert.throws(
      () =>
        new ChannelRegistry([
          { channel: 'evil', exposedName: 'constructor', schema: { type: 'any' } },
        ]),
      /invalid exposed method name/,
    );
  });
});

describe('schema validation', () => {
  it('accepts conforming plain data', () => {
    validate({ name: 'alice', tags: ['a', 'b'] }, profileSchema);
  });

  it('rejects wrong types, missing fields and unknown keys', () => {
    assert.throws(() => validate({ name: 42, tags: [] }, profileSchema), ValidationError);
    assert.throws(() => validate({ tags: [] }, profileSchema), /missing required property/);
    assert.throws(
      () => validate({ name: 'alice', tags: [], injected: true }, profileSchema),
      /unknown property/,
    );
    assert.throws(
      () => validate({ name: 'alice', tags: ['x', 'y', 'z', 'w'] }, profileSchema),
      /at most 3 items/,
    );
  });
});

describe('scanValue rejects hostile values without invoking accessors', () => {
  it('rejects functions', () => {
    assert.throws(() => scanValue(() => 1), UnsafeArgumentError);
    assert.throws(() => scanValue({ run: () => 1 }), UnsafeArgumentError);
  });

  it('rejects getter accessors and never executes them', () => {
    let invoked = false;
    const hostile = {};
    Object.defineProperty(hostile, 'boom', {
      enumerable: true,
      get() {
        invoked = true;
        return 'fired';
      },
    });
    assert.throws(() => scanValue(hostile), /accessor properties/);
    assert.equal(invoked, false);
  });

  it('rejects setter accessors', () => {
    const hostile = {};
    Object.defineProperty(hostile, 'boom', {
      enumerable: true,
      set() {
        // must never run
      },
    });
    assert.throws(() => scanValue(hostile), /accessor properties/);
  });

  it('rejects pollution keys __proto__, constructor and prototype', () => {
    assert.throws(() => scanValue(JSON.parse('{"__proto__": {"x": 1}}')), /forbidden/);
    assert.throws(() => scanValue({ constructor: { name: 'x' } }), /forbidden/);
    assert.throws(() => scanValue({ prototype: { x: 1 } }), /forbidden/);
  });

  it('rejects non-plain objects such as class instances and dates', () => {
    assert.throws(() => scanValue(new Date()), /only plain objects/);
    class Counter {
      count = 0;
    }
    assert.throws(() => scanValue(new Counter()), /only plain objects/);
  });

  it('rejects circular references and symbol keys', () => {
    const cyclic: Record<string, unknown> = { name: 'a' };
    cyclic.self = cyclic;
    assert.throws(() => scanValue(cyclic), /circular/);

    const symbolKey = { [Symbol.for('k')]: 1 };
    assert.throws(() => scanValue(symbolKey), /symbol keys/);
  });

  it('accepts Object.create(null) objects', () => {
    const nullProto = Object.create(null);
    nullProto.name = 'ok';
    scanValue(nullProto);
  });
});

describe('safeClone', () => {
  it('snapshots benign proxies into independent plain data', () => {
    const proxy = new Proxy({ name: 'bob', tags: ['t1'] }, {});
    const clone = safeClone(proxy) as { name: string; tags: string[] };
    assert.deepEqual(clone, { name: 'bob', tags: ['t1'] });
    assert.notEqual(clone, proxy);
  });

  it('rejects proxies whose traps return functions without invoking the trap', () => {
    let trapCalled = false;
    const hostile = new Proxy(
      {},
      {
        ownKeys(target) {
          trapCalled = true;
          return [...Reflect.ownKeys(target), 'payload'];
        },
        getOwnPropertyDescriptor(target, key) {
          if (key === 'payload') {
            return {
              enumerable: true,
              configurable: true,
              writable: true,
              value: () => 'pwn',
            };
          }
          return Reflect.getOwnPropertyDescriptor(target, key);
        },
        get(_target, key) {
          if (key === 'payload') {
            trapCalled = true;
            return () => 'pwn';
          }
          return undefined;
        },
      },
    );
    assert.throws(() => safeClone(hostile), UnsafeArgumentError);
    assert.ok(trapCalled);
  });

  it('rejects proxies that throw from traps', () => {
    const hostile = new Proxy(
      {},
      {
        ownKeys() {
          throw new Error('trap boom');
        },
      },
    );
    assert.throws(() => safeClone(hostile), UnsafeArgumentError);

    const throwingProto = new Proxy(
      {},
      {
        getPrototypeOf() {
          throw new Error('proto trap boom');
        },
      },
    );
    assert.throws(() => safeClone(throwingProto), UnsafeArgumentError);
  });
});

describe('preload bridge surface', () => {
  it('exposes only registered, frozen method names', () => {
    const registry = buildRegistry();
    const { ipcRenderer } = createIpcPair();
    const api = createBridgeApi(registry, ipcRenderer);

    assert.deepEqual(Object.getOwnPropertyNames(api).sort(), ['ping', 'saveProfile']);
    assert.equal(Object.isExtensible(api), false);
    assert.equal(Object.getPrototypeOf(api), null);

    const descriptor = Object.getOwnPropertyDescriptor(api, 'saveProfile');
    assert.ok(descriptor);
    assert.equal(descriptor.writable, false);
    assert.equal(descriptor.configurable, false);
    assert.equal(typeof descriptor.value, 'function');
    assert.throws(() => {
      (api as Record<string, unknown>).extra = () => 1;
    }, TypeError);
  });

  it('blocks hostile arguments before IPC and never invokes getters', async () => {
    const registry = buildRegistry();
    const { ipcRenderer } = createIpcPair();
    const api = createBridgeApi(registry, ipcRenderer);

    let invoked = false;
    const hostileGetter = {};
    Object.defineProperty(hostileGetter, 'boom', {
      enumerable: true,
      get() {
        invoked = true;
        return 1;
      },
    });

    await assert.rejects(() => api.saveProfile(hostileGetter), UnsafeArgumentError);
    assert.equal(invoked, false);
    await assert.rejects(() => api.saveProfile({ fn: () => 1 }), UnsafeArgumentError);
  });
});

describe('main process handlers', () => {
  it('registers handlers only for registered channels', () => {
    const registry = buildRegistry();
    const pair = createIpcPair();
    registerBridgeHandlers(
      pair.ipcMain,
      registry,
      { allowedOrigins: [ALLOWED_ORIGIN] },
      () => 'ok',
    );

    assert.equal(pair.hasHandler('app:save-profile'), true);
    assert.equal(pair.hasHandler('app:ping'), true);
    assert.equal(pair.hasHandler('system:shell'), false);
    assert.equal(pair.hasHandler('app:unknown'), false);
  });

  it('clones, scans and validates arguments before the privileged handler', () => {
    const registry = buildRegistry();
    const proxy = new Proxy({ name: 'carol', tags: ['x'] }, {});

    const cloned = prepareHandlerArgument(
      proxy,
      registry.get('app:save-profile').schema,
    ) as { name: string };
    assert.deepEqual(cloned, { name: 'carol', tags: ['x'] });
    assert.notEqual(cloned, proxy);

    assert.throws(
      () =>
        prepareHandlerArgument(
          { name: 'x', tags: [], evil: 1 },
          registry.get('app:save-profile').schema,
        ),
      ValidationError,
    );
    assert.throws(
      () =>
        prepareHandlerArgument(
          { name: 'x', tags: [() => 1] },
          registry.get('app:save-profile').schema,
        ),
      UnsafeArgumentError,
    );
  });

  it('verifies sender frame origin and main-frame status before running', async () => {
    const registry = buildRegistry();
    const pair = createIpcPair();
    const calls: string[] = [];
    registerBridgeHandlers(
      pair.ipcMain,
      registry,
      { allowedOrigins: [ALLOWED_ORIGIN] },
      () => {
        calls.push('ran');
        return 'done';
      },
    );

    const saveListener = pair.listeners.get('app:save-profile');
    assert.ok(saveListener);

    assert.equal(await saveListener(makeEvent(), { name: 'ok', tags: [] }), 'done');

    await assert.rejects(
      () => saveListener(makeEvent('https://evil.example'), { name: 'ok', tags: [] }),
      SenderRejectedError,
    );
    await assert.rejects(
      () => saveListener(makeEvent('file:///tmp/rogue.html'), { name: 'ok', tags: [] }),
      SenderRejectedError,
    );
    await assert.rejects(
      () => saveListener(makeEvent(ALLOWED_ORIGIN, false), { name: 'ok', tags: [] }),
      SenderRejectedError,
    );
    await assert.rejects(
      () => saveListener({ senderFrame: undefined as unknown as SenderFrameLike }, {}),
      SenderRejectedError,
    );
    assert.deepEqual(calls, ['ran']);
  });

  it('supports custom frame verification', async () => {
    const registry = buildRegistry();
    const pair = createIpcPair();
    registerBridgeHandlers(
      pair.ipcMain,
      registry,
      {
        allowedOrigins: [ALLOWED_ORIGIN],
        verify: (senderFrame) => senderFrame.url.startsWith(`${ALLOWED_ORIGIN}/trusted/`),
      },
      () => 'ok',
    );

    const listener = pair.listeners.get('app:save-profile');
    assert.ok(listener);
    await assert.rejects(
      () => listener(makeEvent(`${ALLOWED_ORIGIN}/untrusted`), { name: 'a', tags: [] }),
      SenderRejectedError,
    );
    assert.equal(
      await listener(makeEvent(`${ALLOWED_ORIGIN}/trusted/page`), { name: 'a', tags: [] }),
      'ok',
    );
  });
});

describe('end-to-end bridge over a fake IPC pair', () => {
  it('delivers validated snapshots and rejects unregistered channels', async () => {
    const registry = buildRegistry();
    const pair = createIpcPair();
    const seenArgs: unknown[] = [];

    registerBridgeHandlers(
      pair.ipcMain,
      registry,
      { allowedOrigins: [ALLOWED_ORIGIN] },
      (_event, _channel, safeArg) => {
        seenArgs.push(safeArg);
        return { ok: true };
      },
    );

    const api = createBridgeApi(registry, pair.ipcRenderer);
    const result = (await api.saveProfile({ name: 'dora', tags: ['one'] })) as {
      ok: boolean;
    };
    assert.deepEqual(result, { ok: true });
    assert.deepEqual(seenArgs, [{ name: 'dora', tags: ['one'] }]);

    assert.equal(
      Object.prototype.hasOwnProperty.call(api, 'systemShell'),
      false,
    );
    await assert.rejects(
      () => pair.ipcRenderer.invoke('system:shell', { cmd: 'rm -rf /' }),
      UnregisteredChannelError,
    );
    assert.equal(pair.hasHandler('system:shell'), false);
  });

  it('neutralizes prototype pollution payloads across the full pipeline', async () => {
    const registry = buildRegistry();
    const pair = createIpcPair();
    registerBridgeHandlers(
      pair.ipcMain,
      registry,
      { allowedOrigins: [ALLOWED_ORIGIN] },
      () => 'unreachable',
    );
    const api = createBridgeApi(registry, pair.ipcRenderer);

    const pollution = JSON.parse('{"name":"x","tags":[],"__proto__":{"polluted":true}}');
    await assert.rejects(() => api.saveProfile(pollution), UnsafeArgumentError);
    assert.equal(
      Object.prototype.hasOwnProperty.call(Object.prototype, 'polluted'),
      false,
    );

    const nested = { name: 'x', tags: [{ __proto__: { y: 1 } }] };
    await assert.rejects(() => api.saveProfile(nested), UnsafeArgumentError);
  });

  it('rejects getter and proxy payloads end to end without executing traps', async () => {
    const registry = buildRegistry();
    const pair = createIpcPair();
    let handlerRan = false;
    registerBridgeHandlers(
      pair.ipcMain,
      registry,
      { allowedOrigins: [ALLOWED_ORIGIN] },
      () => {
        handlerRan = true;
        return 'unreachable';
      },
    );
    const api = createBridgeApi(registry, pair.ipcRenderer);

    let getterFired = false;
    const withGetter = { name: 'x', tags: [] };
    Object.defineProperty(withGetter, 'secret', {
      enumerable: true,
      get() {
        getterFired = true;
        return () => 1;
      },
    });
    await assert.rejects(() => api.saveProfile(withGetter), UnsafeArgumentError);

    const evilProxy = new Proxy(
      { name: 'x', tags: [] },
      {
        ownKeys(target) {
          return [...Reflect.ownKeys(target), 'exploit'];
        },
        getOwnPropertyDescriptor(target, key) {
          if (key === 'exploit') {
            return {
              enumerable: true,
              configurable: true,
              writable: true,
              value: () => 'pwn',
            };
          }
          return Reflect.getOwnPropertyDescriptor(target, key);
        },
      },
    );
    await assert.rejects(() => api.saveProfile(evilProxy), UnsafeArgumentError);

    assert.equal(getterFired, false);
    assert.equal(handlerRan, false);
  });
});
