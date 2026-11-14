import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  NativeBridge,
  type BridgeManifest,
  type CallerContext,
} from './index.ts';

const EXTENSION_ID = 'abcdefghijklmnopabcdefghijklmnop';
const EXTENSION_ORIGIN = `chrome-extension://${EXTENSION_ID}`;
const OTHER_EXTENSION_ID = 'ponmlkjihgfedcbaponmlkjihgfedcba';
const HOST_SCRIPT = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures', 'fake-host.mjs');

function hostSpec(mode: string, extra: Record<string, unknown> = {}) {
  return {
    command: process.execPath,
    args: [HOST_SCRIPT, mode],
    allowedCallers: ['extension-page', 'content-script'] as Array<'extension-page' | 'content-script'>,
    ...extra,
  };
}

function makeManifest(): BridgeManifest {
  return {
    extensionId: EXTENSION_ID,
    contentScripts: ['content/main'],
    hosts: {
      'com.test.echo': hostSpec('echo'),
      'com.test.big': hostSpec('big', { maxResponseBytes: 1024 }),
      'com.test.slow': hostSpec('slow', { timeoutMs: 300 }),
      'com.test.garbage': hostSpec('garbage'),
      'com.test.pageonly': hostSpec('echo', { allowedCallers: ['extension-page'] }),
    },
  };
}

function extensionPageCaller(): CallerContext {
  return { kind: 'extension-page', extensionId: EXTENSION_ID, origin: EXTENSION_ORIGIN };
}

function makeRequest(app: string, payload: unknown = { hello: 'world' }) {
  return { id: 'req-1', app, payload };
}

test('legitimate extension-page request reaches an allowed host', async () => {
  const bridge = new NativeBridge(makeManifest());
  const res = await bridge.handleMessage(makeRequest('com.test.echo'), extensionPageCaller());
  assert.equal(res.ok, true);
  if (res.ok) {
    assert.equal(res.id, 'req-1');
    assert.deepEqual(res.result, { echo: { id: 'req-1', payload: { hello: 'world' } } });
  }
});

test('web page forging an application name is rejected and spawns nothing', async () => {
  const bridge = new NativeBridge(makeManifest());
  const webCaller: CallerContext = {
    kind: 'web-page',
    origin: 'https://evil.example',
  };
  const res = await bridge.handleMessage(makeRequest('com.test.echo'), webCaller);
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_FORBIDDEN');
});

test('web page cannot reach a host even with a manifest-listed app name', async () => {
  const bridge = new NativeBridge(makeManifest());
  const res = await bridge.handleMessage(
    makeRequest('com.test.pageonly'),
    { kind: 'web-page', extensionId: EXTENSION_ID, origin: EXTENSION_ORIGIN },
  );
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_FORBIDDEN');
});

test('cross-extension calls are rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const foreign: CallerContext = {
    kind: 'extension-page',
    extensionId: OTHER_EXTENSION_ID,
    origin: `chrome-extension://${OTHER_EXTENSION_ID}`,
  };
  const res = await bridge.handleMessage(makeRequest('com.test.echo'), foreign);
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_FORBIDDEN');
});

test('extension page with spoofed origin is rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const caller: CallerContext = {
    kind: 'extension-page',
    extensionId: EXTENSION_ID,
    origin: 'https://evil.example',
  };
  const res = await bridge.handleMessage(makeRequest('com.test.echo'), caller);
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_FORBIDDEN');
});

test('content script requires a one-time session and replay is rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const scriptCaller = (token?: string): CallerContext => ({
    kind: 'content-script',
    extensionId: EXTENSION_ID,
    scriptId: 'content/main',
    sessionToken: token,
  });

  const noToken = await bridge.handleMessage(makeRequest('com.test.echo'), scriptCaller());
  assert.equal(noToken.ok, false);
  if (!noToken.ok) assert.equal(noToken.error.code, 'ERR_SESSION');

  const { token } = bridge.issueContentScriptSession('content/main');
  const first = await bridge.handleMessage(makeRequest('com.test.echo'), scriptCaller(token));
  assert.equal(first.ok, true);

  const replay = await bridge.handleMessage(makeRequest('com.test.echo'), scriptCaller(token));
  assert.equal(replay.ok, false);
  if (!replay.ok) assert.equal(replay.error.code, 'ERR_SESSION');
});

test('sessions cannot be issued to undeclared content scripts', async () => {
  const bridge = new NativeBridge(makeManifest());
  assert.throws(() => bridge.issueContentScriptSession('content/evil'), /not declared/);
});

test('expired session token is rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const { token } = bridge.issueContentScriptSession('content/main', -1);
  const res = await bridge.handleMessage(makeRequest('com.test.echo'), {
    kind: 'content-script',
    extensionId: EXTENSION_ID,
    scriptId: 'content/main',
    sessionToken: token,
  });
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_SESSION');
});

test('application not present in the manifest allowlist is rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const res = await bridge.handleMessage(makeRequest('com.evil.notlisted'), extensionPageCaller());
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_UNKNOWN_APP');
});

test('content script cannot invoke a page-only host', async () => {
  const bridge = new NativeBridge(makeManifest());
  const { token } = bridge.issueContentScriptSession('content/main');
  const res = await bridge.handleMessage(makeRequest('com.test.pageonly'), {
    kind: 'content-script',
    extensionId: EXTENSION_ID,
    scriptId: 'content/main',
    sessionToken: token,
  });
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_FORBIDDEN');
});

test('oversized host response is rejected without leaking the host path', async () => {
  const bridge = new NativeBridge(makeManifest());
  const res = await bridge.handleMessage(makeRequest('com.test.big'), extensionPageCaller());
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.error.code, 'ERR_RESPONSE_TOO_LARGE');
    assert.ok(!JSON.stringify(res).includes(HOST_SCRIPT));
    assert.ok(!JSON.stringify(res).includes(process.execPath));
  }
});

test('slow host is killed on timeout with a sanitized error', async () => {
  const bridge = new NativeBridge(makeManifest());
  const res = await bridge.handleMessage(makeRequest('com.test.slow'), extensionPageCaller());
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.error.code, 'ERR_TIMEOUT');
    assert.ok(!JSON.stringify(res).includes(HOST_SCRIPT));
  }
});

test('invalid JSON from the host is rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const res = await bridge.handleMessage(makeRequest('com.test.garbage'), extensionPageCaller());
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_BAD_RESPONSE');
});

test('oversized request payload is rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const res = await bridge.handleMessage(
    makeRequest('com.test.echo', { blob: 'y'.repeat(256 * 1024) }),
    extensionPageCaller(),
  );
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.error.code, 'ERR_REQUEST_TOO_LARGE');
});

test('malformed request fields are rejected', async () => {
  const bridge = new NativeBridge(makeManifest());
  const cases: unknown[] = [
    null,
    'com.test.echo',
    { app: 'com.test.echo', payload: {} },
    { id: 'bad id!', app: 'com.test.echo', payload: {} },
    { id: 'req-1', app: 'com.test.echo' },
    { id: 'req-1', app: 'com.test.echo', payload: {}, extra: true },
  ];
  for (const bad of cases) {
    const res = await bridge.handleMessage(bad, extensionPageCaller());
    assert.equal(res.ok, false, `expected rejection for ${JSON.stringify(bad)}`);
    if (!res.ok) assert.equal(res.error.code, 'ERR_BAD_REQUEST');
  }
});

test('missing host binary fails with a sanitized error', async () => {
  const manifest = makeManifest();
  manifest.hosts['com.test.missing'] = {
    command: '/nonexistent/path/to/secret-host-binary',
    allowedCallers: ['extension-page'],
  };
  const bridge = new NativeBridge(manifest);
  const res = await bridge.handleMessage(makeRequest('com.test.missing'), extensionPageCaller());
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.error.code, 'ERR_HOST_UNAVAILABLE');
    assert.ok(!JSON.stringify(res).includes('secret-host-binary'));
  }
});
