import { test } from 'node:test';
import assert from 'node:assert/strict';

import { SharedWorkerProxy } from '../src/client-proxy.ts';
import { TenantWorkerAgent } from '../src/worker-agent.ts';
import { DemoTenantRouter } from '../src/demo-router.ts';
import { deriveWorkerName } from '../src/worker-name.ts';
import { issueHandshakeToken } from '../src/token.ts';
import { InMemoryReplayCache } from '../src/replay-cache.ts';
import { WorkerProxyError } from '../src/errors.ts';
import {
  HANDSHAKE_PROTOCOL,
  type PortLike,
  type TenantCredentials,
} from '../src/protocol.ts';
import {
  FakeMessagePort,
  FakeSharedWorkerRuntime,
  fakeMessageChannelFactory,
} from '../src/testing/fake-runtime.ts';

const ORIGIN = 'https://app.example.com';
const SCRIPT_URL_A = `${ORIGIN}/workers/tenant-worker.js`;
const SCRIPT_URL_B = `${ORIGIN}/workers/other-worker.js`;
const FIXED_NOW = 1_700_000_000_000;

const TENANT_A: TenantCredentials = {
  tenantId: 'tenant-a',
  secret: 'secret-a',
};
const TENANT_B: TenantCredentials = {
  tenantId: 'tenant-b',
  secret: 'secret-b',
};

interface Harness {
  proxy: SharedWorkerProxy;
  runtime: FakeSharedWorkerRuntime;
  router: DemoTenantRouter;
  replayCache: InMemoryReplayCache;
  connect(
    credentials: TenantCredentials,
    scriptUrl?: string,
    overrides?: { now?: number; nonce?: string; timeoutMs?: number },
  ): ReturnType<SharedWorkerProxy['connect']>;
}

function createHarness(scriptUrl = SCRIPT_URL_A, now = FIXED_NOW): Harness {
  const replayCache = new InMemoryReplayCache(4096, () => now);
  const router = new DemoTenantRouter();
  const secrets: Record<string, string> = {
    [TENANT_A.tenantId]: TENANT_A.secret,
    [TENANT_B.tenantId]: TENANT_B.secret,
  };
  const agent = new TenantWorkerAgent({
    scriptUrl,
    resolveSecret: (tenantId) => secrets[tenantId],
    onChannel: router.onChannel,
    createMessageChannel: fakeMessageChannelFactory,
    replayCache,
    now: () => now,
    handshakeTimeoutMs: 30,
  });
  const runtime = new FakeSharedWorkerRuntime({ scriptUrl, agent });
  const proxy = new SharedWorkerProxy(runtime.createWorker);
  return {
    proxy,
    runtime,
    router,
    replayCache,
    connect: (credentials, targetScriptUrl = scriptUrl, overrides = {}) =>
      proxy.connect({
        credentials,
        scriptUrl: targetScriptUrl,
        now: overrides.now ?? now,
        nonce: overrides.nonce,
        timeoutMs: overrides.timeoutMs ?? 5_000,
      }),
  };
}

function nextMessage(port: PortLike): Promise<{ data: unknown }> {
  return new Promise((resolve) => {
    port.addEventListener('message', function handler(event) {
      port.removeEventListener('message', handler);
      resolve(event as { data: unknown });
    });
    port.start();
  });
}

function waitForMessage(
  port: PortLike,
  predicate: (data: unknown) => boolean,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error('expected message was not received')),
      100,
    );
    const handler = (event: { data: unknown }) => {
      if (predicate(event.data)) {
        clearTimeout(timer);
        port.removeEventListener('message', handler);
        resolve(event.data);
      }
    };
    port.addEventListener('message', handler);
    port.start();
  });
}

async function expectReject(promise: Promise<unknown>, code: string): Promise<void> {
  await assert.rejects(
    promise,
    (error: unknown) =>
      error instanceof WorkerProxyError && error.code === code,
    `expected rejection with code ${code}`,
  );
}

test('worker 名称由租户与脚本 URL 摘要派生，不暴露裸名称', async () => {
  const nameA = await deriveWorkerName(TENANT_A.tenantId, SCRIPT_URL_A);
  const nameADuplicate = await deriveWorkerName(TENANT_A.tenantId, SCRIPT_URL_A);
  const nameB = await deriveWorkerName(TENANT_B.tenantId, SCRIPT_URL_A);
  const nameOtherScript = await deriveWorkerName(
    TENANT_A.tenantId,
    SCRIPT_URL_B,
  );

  assert.match(nameA, /^tsw-v1-[0-9a-f]{64}$/);
  assert.equal(nameA, nameADuplicate);
  assert.notEqual(nameA, nameB);
  assert.notEqual(nameA, nameOtherScript);
  assert.ok(!nameA.includes(TENANT_A.tenantId));
  assert.ok(!nameA.includes(SCRIPT_URL_A));
});

test('两个租户在同源下获得隔离通道，且同租户名称复用同一 worker', async () => {
  const harness = createHarness();

  const channelA1 = await harness.connect(TENANT_A);
  const channelA2 = await harness.connect(TENANT_A);
  const channelB1 = await harness.connect(TENANT_B);

  assert.equal(channelA1.workerName, channelA2.workerName);
  assert.notEqual(channelA1.workerName, channelB1.workerName);
  assert.equal(harness.runtime.registeredNames().length, 2);
  assert.equal(harness.router.channelsFor(TENANT_A.tenantId), 2);
  assert.equal(harness.router.channelsFor(TENANT_B.tenantId), 1);

  channelA1.port.postMessage({ id: 1, kind: 'broadcast', payload: 'a-only' });
  channelA1.port.postMessage({ id: 2, kind: 'echo', payload: 'ping' });

  const echo = await waitForMessage(
    channelA1.port,
    (data) => (data as { kind?: string }).kind === 'echo-result',
  );
  assert.deepEqual(echo, {
    id: 2,
    kind: 'echo-result',
    tenantId: TENANT_A.tenantId,
    payload: 'ping',
  });

  const bMessage = await new Promise((resolve) => {
    const timer = setTimeout(() => resolve('silent'), 50);
    channelB1.port.addEventListener('message', () => {
      clearTimeout(timer);
      resolve('received');
    });
    channelB1.port.start();
  });
  assert.equal(bMessage, 'silent');
});

test('握手前发送的业务消息不会分配通道，端口被拒绝并关闭', async () => {
  const harness = createHarness();
  const workerName = await deriveWorkerName(TENANT_A.tenantId, SCRIPT_URL_A);
  const worker = harness.runtime.createWorker(SCRIPT_URL_A, workerName);
  const pagePort = worker.port as FakeMessagePort;

  pagePort.postMessage({ kind: 'echo', payload: 'before-handshake' });

  const rejection = await nextMessage(pagePort);
  assert.deepEqual(rejection.data, {
    protocol: HANDSHAKE_PROTOCOL,
    type: 'tenant-handshake-result',
    ok: false,
    tenantId: '',
    code: 'invalid_message',
    message: 'expected a handshake request',
  });
  assert.equal(harness.router.channelsFor(TENANT_A.tenantId), 0);
});

test('同名不同脚本 URL 的握手请求被拒绝', async () => {
  const harness = createHarness(SCRIPT_URL_A);

  await expectReject(
    harness.connect(TENANT_A, SCRIPT_URL_B),
    'script_url_mismatch',
  );
  assert.equal(harness.router.channelsFor(TENANT_A.tenantId), 0);
});

test('令牌重放：同一令牌的第二次连接被拒绝', async () => {
  const harness = createHarness();
  const nonce = 'one-time-nonce';

  const first = await harness.connect(TENANT_A, SCRIPT_URL_A, { nonce });
  assert.equal(harness.router.channelsFor(TENANT_A.tenantId), 1);
  first.close();

  await expectReject(
    harness.connect(TENANT_A, SCRIPT_URL_A, { nonce }),
    'token_replayed',
  );
  assert.equal(harness.router.channelsFor(TENANT_A.tenantId), 1);
});

test('伪造或使用其他租户密钥签发的令牌被拒绝', async () => {
  const harness = createHarness();

  await expectReject(
    harness.proxy.connect({
      credentials: { tenantId: TENANT_A.tenantId, secret: 'wrong-secret' },
      scriptUrl: SCRIPT_URL_A,
      now: FIXED_NOW,
      nonce: 'forged-1',
    }),
    'token_rejected',
  );

  const crossTenantToken = await issueHandshakeToken(
    TENANT_B,
    SCRIPT_URL_A,
    { now: FIXED_NOW, nonce: 'cross-tenant' },
  );
  const workerName = await deriveWorkerName(TENANT_A.tenantId, SCRIPT_URL_A);
  const pagePort = harness.runtime
    .createWorker(SCRIPT_URL_A, workerName)
    .port as FakeMessagePort;

  pagePort.postMessage({
    protocol: HANDSHAKE_PROTOCOL,
    type: 'tenant-handshake-request',
    tenantId: TENANT_A.tenantId,
    token: crossTenantToken.token,
    scriptUrl: SCRIPT_URL_A,
  });
  const result = await nextMessage(pagePort);
  assert.equal((result.data as { ok: boolean }).ok, false);
  assert.equal((result.data as { code: string }).code, 'token_rejected');
});

test('未知租户与过期令牌均无法完成握手', async () => {
  const harness = createHarness();

  await expectReject(
    harness.proxy.connect({
      credentials: { tenantId: 'tenant-unknown', secret: 'whatever' },
      scriptUrl: SCRIPT_URL_A,
      now: FIXED_NOW,
      nonce: 'unknown-1',
    }),
    'unknown_tenant',
  );

  const expired = await issueHandshakeToken(TENANT_A, SCRIPT_URL_A, {
    now: FIXED_NOW - 120_000,
    ttlMs: 60_000,
    nonce: 'expired-1',
  });
  const workerName = await deriveWorkerName(TENANT_A.tenantId, SCRIPT_URL_A);
  const pagePort = harness.runtime
    .createWorker(SCRIPT_URL_A, workerName)
    .port as FakeMessagePort;
  pagePort.postMessage({
    protocol: HANDSHAKE_PROTOCOL,
    type: 'tenant-handshake-request',
    tenantId: TENANT_A.tenantId,
    token: expired.token,
    scriptUrl: SCRIPT_URL_A,
  });
  const result = await nextMessage(pagePort);
  assert.equal((result.data as { code: string }).code, 'token_expired');
});

test('握手超时后端口关闭且不分配通道', async () => {
  const replayCache = new InMemoryReplayCache();
  const router = new DemoTenantRouter();
  const agent = new TenantWorkerAgent({
    scriptUrl: SCRIPT_URL_A,
    resolveSecret: () => TENANT_A.secret,
    onChannel: router.onChannel,
    createMessageChannel: fakeMessageChannelFactory,
    replayCache,
    handshakeTimeoutMs: 10,
  });
  const [pagePort, workerPort] = FakeMessagePort.pair();
  agent.handleConnectPort(workerPort);

  const result = await nextMessage(pagePort);
  assert.equal((result.data as { code: string }).code, 'handshake_timeout');
  assert.equal(router.channelsFor(TENANT_A.tenantId), 0);
});

test('同名绑定不同脚本 URL 的 SharedWorker 复用请求被底层工厂拒绝', async () => {
  const harness = createHarness(SCRIPT_URL_A);
  const reusedName = await deriveWorkerName(TENANT_A.tenantId, SCRIPT_URL_A);
  const first = harness.runtime.createWorker(SCRIPT_URL_A, reusedName);
  assert.ok(first);
  assert.throws(
    () => harness.runtime.createWorker(SCRIPT_URL_B, reusedName),
    /already bound/,
  );
});
