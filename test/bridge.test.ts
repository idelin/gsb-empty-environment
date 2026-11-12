import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  attachBridge,
  BridgeDispatcher,
  type BridgeMessageEvent,
  type BridgeResponse,
  type WindowLike,
} from '../index.ts';

interface CallRecord {
  options: unknown;
}

function buildDispatcher(): {
  dispatcher: BridgeDispatcher;
  fsCalls: CallRecord[];
  geolocationCalls: CallRecord[];
} {
  const fsCalls: CallRecord[] = [];
  const geolocationCalls: CallRecord[] = [];

  const dispatcher = new BridgeDispatcher({
    allowNavigation: ['*.example.com', 'trusted.example.org'],
    plugins: {
      Filesystem: {
        methods: {
          readFile: (options) => {
            fsCalls.push({ options });
            return { path: (options as { path?: string } | undefined)?.path, data: 'hello' };
          },
        },
      },
      Geolocation: {
        methods: {
          getPosition: (options) => {
            geolocationCalls.push({ options });
            return { lat: 1, lng: 2 };
          },
        },
      },
      Boom: {
        methods: {
          explode: () => {
            throw new Error('native failure');
          },
        },
      },
    },
  });

  return { dispatcher, fsCalls, geolocationCalls };
}

const HOST_WINDOW = Symbol('host-window');
const IFRAME_WINDOW = Symbol('iframe-window');

function event(
  data: unknown,
  origin = 'https://app.example.com',
  source: unknown = HOST_WINDOW,
): BridgeMessageEvent {
  return { origin, source, data };
}

function message(overrides: Partial<Record<'callbackId' | 'pluginId' | 'methodName', string>> & {
  options?: unknown;
} = {}): Record<string, unknown> {
  return {
    callbackId: 'cb1',
    pluginId: 'Filesystem',
    methodName: 'readFile',
    options: { path: '/a.txt' },
    ...overrides,
  };
}

test('prototype keys as pluginId are denied via own-property lookup', async () => {
  const { dispatcher, fsCalls } = buildDispatcher();

  const protoPayload = JSON.parse('{"callbackId":"cb1","pluginId":"__proto__","methodName":"readFile"}');
  const protoResult = await dispatcher.dispatch(event(protoPayload), true);
  assert.equal(protoResult.ok, false);
  assert.equal((protoResult as { error: { code: string } }).error.code, 'INVALID_MESSAGE');

  const constructorPayload = message({ callbackId: 'cb2', pluginId: 'constructor' });
  const constructorResult = await dispatcher.dispatch(event(constructorPayload), true);
  assert.equal(constructorResult.ok, false);
  assert.equal((constructorResult as { error: { code: string } }).error.code, 'INVALID_MESSAGE');

  // Even when the identifier check is bypassed, a null-prototype registry
  // plus hasOwn lookup means inherited names never resolve to a handler.
  const objectProto = Object.create(null) as Record<string, unknown>;
  objectProto.callbackId = 'cb3';
  objectProto.pluginId = 'toString';
  objectProto.methodName = 'readFile';
  const inherited = await dispatcher.dispatch(event(objectProto), true);
  assert.equal(inherited.ok, false);
  assert.equal((inherited as { error: { code: string } }).error.code, 'PLUGIN_NOT_ALLOWED');

  assert.equal(fsCalls.length, 0);
  assert.equal((Object.prototype as Record<string, unknown>).polluted, undefined);
});

test('non-allow-listed plugins and methods are rejected', async () => {
  const { dispatcher } = buildDispatcher();

  const plugin = await dispatcher.dispatch(event(message({ callbackId: 'c1', pluginId: 'Camera' })), true);
  assert.equal(plugin.ok, false);
  assert.equal((plugin as { error: { code: string } }).error.code, 'PLUGIN_NOT_ALLOWED');

  const method = await dispatcher.dispatch(
    event(message({ callbackId: 'c2', methodName: 'writeFile' })),
    true,
  );
  assert.equal(method.ok, false);
  assert.equal((method as { error: { code: string } }).error.code, 'METHOD_NOT_ALLOWED');
});

test('spoofed domains are denied while legitimate sub-domains pass', async () => {
  const { dispatcher, fsCalls } = buildDispatcher();

  for (const [idx, origin] of [
    'https://example.com',
    'https://evilexample.com',
    'https://example.com.evil.net',
    'https://app.example.com.evil.net',
  ].entries()) {
    const denied = await dispatcher.dispatch(event(message({ callbackId: `cbspoof${idx}` }), origin), true);
    assert.equal(denied.ok, false);
    assert.equal((denied as { error: { code: string } }).error.code, 'ORIGIN_DENIED');
  }

  const allowed = await dispatcher.dispatch(
    event(message({ callbackId: 'cbok' }), 'https://deep.app.example.com'),
    true,
  );
  assert.equal(allowed.ok, true);
  assert.equal(fsCalls.length, 1);
});

test('iframe and cross-window sources have no bridge access', async () => {
  const { dispatcher, fsCalls } = buildDispatcher();

  const fromIframe = await dispatcher.dispatch(event(message({ callbackId: 'cbframe' }), 'https://app.example.com', IFRAME_WINDOW), false);
  assert.equal(fromIframe.ok, false);
  assert.equal((fromIframe as { error: { code: string } }).error.code, 'IFRAME_FORBIDDEN');

  const nullSource = await dispatcher.dispatch(
    { origin: 'https://app.example.com', source: null, data: message({ callbackId: 'cbnull' }) },
    false,
  );
  assert.equal(nullSource.ok, false);
  assert.equal((nullSource as { error: { code: string } }).error.code, 'IFRAME_FORBIDDEN');

  assert.equal(fsCalls.length, 0);
});

test('options containing __proto__ are rejected and never reach the plugin', async () => {
  const { dispatcher, fsCalls } = buildDispatcher();

  const polluted = JSON.parse(
    '{"callbackId":"cb1","pluginId":"Filesystem","methodName":"readFile","options":{"__proto__":{"polluted":true},"path":"/x"}}',
  );
  const result = await dispatcher.dispatch(event(polluted), true);
  assert.equal(result.ok, false);
  assert.equal((result as { error: { code: string } }).error.code, 'OPTIONS_REJECTED');
  assert.equal((Object.prototype as Record<string, unknown>).polluted, undefined);
  assert.equal(fsCalls.length, 0);
});

test('callbackId must be short alphanumeric', async () => {
  const { dispatcher } = buildDispatcher();

  for (const badId of ['', 'cb-1', 'cb 1', 'cb.1', 'a#b', 'x'.repeat(33)]) {
    const result = await dispatcher.dispatch(event(message({ callbackId: badId })), true);
    assert.equal(result.ok, false, `expected rejection for ${JSON.stringify(badId)}`);
    assert.equal((result as { error: { code: string } }).error.code, 'INVALID_MESSAGE');
    assert.equal(result.respond, false);
  }

  const result = await dispatcher.dispatch(event(message({ callbackId: 'AbC123' })), true);
  assert.equal(result.ok, true);
});

test('the same callbackId completes only once', async () => {
  const { dispatcher, fsCalls } = buildDispatcher();

  const first = await dispatcher.dispatch(event(message({ callbackId: 'once' })), true);
  assert.equal(first.ok, true);
  assert.equal(dispatcher.isCallbackSettled('once'), true);

  const second = await dispatcher.dispatch(event(message({ callbackId: 'once' })), true);
  assert.equal(second.ok, false);
  assert.equal((second as { error: { code: string } }).error.code, 'DUPLICATE_CALLBACK');
  assert.equal(second.respond, false);

  // Concurrent re-use is blocked as well: the id is reserved before await.
  const concurrent = await Promise.all([
    dispatcher.dispatch(event(message({ callbackId: 'race' })), true),
    dispatcher.dispatch(event(message({ callbackId: 'race' })), true),
  ]);
  assert.equal(concurrent.filter((r) => r.ok).length, 1);
  assert.equal(fsCalls.length, 2);
});

test('plugin exceptions are reported as PLUGIN_ERROR and still settle the id', async () => {
  const { dispatcher } = buildDispatcher();

  const failed = await dispatcher.dispatch(
    event(message({ callbackId: 'boom', pluginId: 'Boom', methodName: 'explode', options: undefined })),
    true,
  );
  assert.equal(failed.ok, false);
  assert.equal((failed as { error: { code: string } }).error.code, 'PLUGIN_ERROR');
  assert.equal(dispatcher.isCallbackSettled('boom'), true);

  const retry = await dispatcher.dispatch(
    event(message({ callbackId: 'boom', pluginId: 'Boom', methodName: 'explode' })),
    true,
  );
  assert.equal((retry as { error: { code: string } }).error.code, 'DUPLICATE_CALLBACK');
});

test('malformed payloads are dropped without a response', async () => {
  const { dispatcher } = buildDispatcher();

  for (const data of [null, [], 'text', 42, { pluginId: 'Filesystem' }]) {
    const result = await dispatcher.dispatch(event(data), true);
    assert.equal(result.ok, false);
    assert.equal(result.respond, false);
  }
});

function fakeWindow(): WindowLike & {
  deliver: (event: BridgeMessageEvent) => void;
  responses: BridgeResponse[];
} {
  let listener: ((event: BridgeMessageEvent) => void) | null = null;
  const responses: BridgeResponse[] = [];
  const win: WindowLike & {
    self: unknown;
    deliver: (event: BridgeMessageEvent) => void;
    responses: BridgeResponse[];
  } = {
    self: undefined,
    responses,
    addEventListener: (_type, fn) => {
      listener = fn;
    },
    removeEventListener: () => {
      listener = null;
    },
    postMessage: (msg) => {
      responses.push(msg);
    },
    deliver: (ev) => listener?.(ev),
  };
  win.self = win;
  return win;
}

test('attachBridge wires a real window end to end', async () => {
  const { dispatcher } = buildDispatcher();
  const win = fakeWindow();
  const detach = attachBridge(dispatcher, win);

  win.deliver(event(message({ callbackId: 'win1' }), 'https://app.example.com', win));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(win.responses.length, 1);
  assert.equal(win.responses[0].success, true);
  assert.equal(win.responses[0].callbackId, 'win1');

  // iframe source: denied, but an error response is still delivered
  win.deliver(event(message({ callbackId: 'win2' }), 'https://app.example.com', IFRAME_WINDOW));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(win.responses[1].success, false);
  assert.equal(win.responses[1].error?.code, 'IFRAME_FORBIDDEN');

  // spoofed origin
  win.deliver(event(message({ callbackId: 'win3' }), 'https://evilexample.com', win));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(win.responses[2].error?.code, 'ORIGIN_DENIED');

  // duplicate id is silently swallowed: no additional response
  win.deliver(event(message({ callbackId: 'win1' }), 'https://app.example.com', win));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(win.responses.length, 3);

  detach();
  win.deliver(event(message({ callbackId: 'win4' }), 'https://app.example.com', win));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(win.responses.length, 3);
});

test('dispatcher construction rejects prototype-keyed whitelist entries', () => {
  const protoPlugin: Record<string, { methods: Record<string, () => number> }> = {};
  Object.defineProperty(protoPlugin, '__proto__', {
    value: { methods: { x: () => 1 } },
    enumerable: true,
    configurable: true,
    writable: true,
  });
  assert.throws(
    () =>
      new BridgeDispatcher({
        allowNavigation: ['app.example.com'],
        plugins: protoPlugin,
      }),
  );
  const constructorMethod: Record<string, () => number> = {};
  constructorMethod['constructor'] = () => 1;
  assert.throws(
    () =>
      new BridgeDispatcher({
        allowNavigation: ['app.example.com'],
        plugins: { Safe: { methods: constructorMethod } },
      }),
  );
});
