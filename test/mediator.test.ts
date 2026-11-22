import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ConditionalPasskeyMediator, GateError } from '../src/index.ts';
import { buildFakeEnv, publicKey, settleMicrotasks, type RecordedCall } from './fakeEnv.ts';

function assertRejectsWith(
  promise: Promise<unknown>,
  name: string,
  messageIncludes?: string,
): Promise<void> {
  return assert.rejects(
    promise,
    (error: Error) => {
      assert.equal(error.name, name);
      if (messageIncludes) {
        assert.match(error.message, new RegExp(messageIncludes, 'i'));
      }
      return true;
    },
  );
}

test('trusted top-level frame with allowing policy reaches the browser interface', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const promise = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();

  assert.equal(calls.length, 1);
  assert.equal(calls[0].request.mediation, 'conditional');
  assert.deepEqual(calls[0].request.publicKey.allowCredentials, []);
  assert.equal(calls[0].request.signal.aborted, false);
  assert.equal(mediator.inFlight, true);

  const credential = { id: 'cred-1', type: 'public-key' as const, rawId: new ArrayBuffer(0) };
  calls[0].resolve(credential);
  assert.equal(await promise, credential);
  assert.equal(mediator.inFlight, false);
});

test('cross-site iframe is rejected and never calls navigator.credentials.get', async () => {
  const { env, calls } = buildFakeEnv({
    origin: 'https://login.example.com',
    topOrigin: 'https://ads.third-party.test',
    ancestors: ['https://ads.third-party.test'],
  });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(
    mediator.get({ publicKey: publicKey() }),
    'NotAllowedError',
    'trusted',
  );
  assert.equal(calls.length, 0);
  assert.equal(mediator.inFlight, false);
});

test('same-site iframe (subdomain embedder) passes the frame gate', async () => {
  const { env, calls } = buildFakeEnv({
    origin: 'https://login.example.com',
    topOrigin: 'https://www.example.com',
    ancestors: ['https://www.example.com'],
  });
  const mediator = new ConditionalPasskeyMediator(env);

  const promise = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  assert.equal(calls.length, 1);

  calls[0].resolve({ id: 'ok', type: 'public-key', rawId: new ArrayBuffer(0) });
  await promise;
});

test('cross-site ancestor deeper in the chain is rejected', async () => {
  const { env, calls } = buildFakeEnv({
    origin: 'https://login.example.com',
    topOrigin: 'https://www.example.com',
    ancestors: ['https://www.example.com', 'https://evil.attacker.test'],
  });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(mediator.get({ publicKey: publicKey() }), 'NotAllowedError');
  assert.equal(calls.length, 0);
});

test('schemeful cross-site iframe (http vs https) is rejected', async () => {
  const { env, calls } = buildFakeEnv({
    origin: 'https://login.example.com',
    topOrigin: 'http://www.example.com',
    ancestors: ['http://www.example.com'],
  });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(mediator.get({ publicKey: publicKey() }), 'NotAllowedError');
  assert.equal(calls.length, 0);
});

test('denied permissions policy blocks the call before the browser interface', async () => {
  const { env, calls } = buildFakeEnv({ policyAllows: false });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(
    mediator.get({ publicKey: publicKey() }),
    'NotAllowedError',
    'permissions policy',
  );
  assert.equal(calls.length, 0);
});

test('Permissions API denial (no policy object) blocks the call', async () => {
  const { env, calls } = buildFakeEnv({
    includePermissionsPolicy: false,
    includePermissionsApi: true,
    permissionState: 'denied',
  });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(mediator.get({ publicKey: publicKey() }), 'NotAllowedError');
  assert.equal(calls.length, 0);
});

test('embedded frame fails closed when neither policy nor Permissions API exists', async () => {
  const { env, calls } = buildFakeEnv({
    topOrigin: 'https://www.example.com',
    ancestors: ['https://www.example.com'],
    includePermissionsPolicy: false,
    includePermissionsApi: false,
  });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(mediator.get({ publicKey: publicKey() }), 'NotAllowedError');
  assert.equal(calls.length, 0);
});

test('populated allowCredentials is rejected as InvalidStateError', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(
    mediator.get({
      publicKey: publicKey({ allowCredentials: [{ id: new ArrayBuffer(8), type: 'public-key' }] }),
    }),
    'InvalidStateError',
    'allowCredentials',
  );
  assert.equal(calls.length, 0);
});

test('missing challenge is rejected as TypeError before the interface', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(
    mediator.get({ publicKey: { ...publicKey(), challenge: undefined } }),
    'TypeError',
    'challenge',
  );
  assert.equal(calls.length, 0);
});

test('a second get() aborts the first and only one request stays in flight', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const first = mediator.get({ publicKey: publicKey() });
  // Attach the rejection handler before the abort so the platform fake's
  // rejection is never momentarily unhandled.
  const firstSettled = assertRejectsWith(first, 'AbortError');
  await settleMicrotasks();
  const second = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();

  assert.equal(calls.length, 2);
  assert.equal(calls[0].request.signal.aborted, true);
  assert.equal(calls[1].request.signal.aborted, false);
  assert.equal(mediator.inFlight, true);

  calls[1].resolve({ id: 'cred-2', type: 'public-key', rawId: new ArrayBuffer(0) });
  await second;
  assert.equal(mediator.inFlight, false);
  await firstSettled;
});

test('a rejected duplicate does not stop a later legal request', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const first = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  const blocked = mediator.get({
    publicKey: publicKey({ allowCredentials: [{ id: new ArrayBuffer(4), type: 'public-key' }] }),
  });
  await assertRejectsWith(blocked, 'InvalidStateError');
  assert.equal(calls.length, 1);
  assert.equal(calls[0].request.signal.aborted, false);

  calls[0].resolve({ id: 'cred-1', type: 'public-key', rawId: new ArrayBuffer(0) });
  await first;

  const third = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  assert.equal(calls.length, 2);
  calls[1].resolve({ id: 'cred-3', type: 'public-key', rawId: new ArrayBuffer(0) });
  await third;
});

test('navigation aborts the request and discards its late response', async () => {
  const { env, calls, emit } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const promise = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  assert.equal(calls.length, 1);

  emit('pagehide');
  assert.equal(calls[0].request.signal.aborted, true);
  await assertRejectsWith(promise, 'AbortError');
  assert.equal(mediator.inFlight, false);

  // The platform answers anyway, long after navigation started.
  const lateCredential = { id: 'late', type: 'public-key' as const, rawId: new ArrayBuffer(0) };
  calls[0].resolve(lateCredential);
  await settleMicrotasks();
});

test('a late rejection after navigation surfaces as AbortError, never the platform error', async () => {
  const { env, calls, emit } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const promise = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  emit('beforeunload');

  calls[0].reject(new DOMException('platform late failure', 'NotAllowedError'));
  await assertRejectsWith(promise, 'AbortError');
});

test('after navigation, a new legal request is served independently', async () => {
  const { env, calls, emit } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const old = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  emit('pagehide');
  await assertRejectsWith(old, 'AbortError');

  const next = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  assert.equal(calls.length, 2);
  assert.equal(calls[1].request.signal.aborted, false);
  calls[1].resolve({ id: 'fresh', type: 'public-key', rawId: new ArrayBuffer(0) });
  await next;
});

test('external abort signal cancels the conditional request', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);
  const external = new AbortController();

  const promise = mediator.get({ publicKey: publicKey(), signal: external.signal });
  await settleMicrotasks();
  external.abort();
  assert.equal(calls[0].request.signal.aborted, true);
  await assertRejectsWith(promise, 'AbortError');
  assert.equal(mediator.inFlight, false);
});

test('abort() cancels the in-flight request and is safe when idle', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const promise = mediator.get({ publicKey: publicKey() });
  await settleMicrotasks();
  mediator.abort();
  assert.equal(calls[0].request.signal.aborted, true);
  await assertRejectsWith(promise, 'AbortError');

  mediator.abort();
  mediator.handleNavigation();
  mediator.dispose();
});

test('missing WebAuthn API is reported as NotAllowedError', async () => {
  const { env, calls } = buildFakeEnv({ includeCredentialsApi: false });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(mediator.get({ publicKey: publicKey() }), 'NotAllowedError');
  assert.equal(calls.length, 0);
});

test('frame-chain fallback rejects a cross-origin parent without ancestorOrigins', async () => {
  const { env, calls } = buildFakeEnv({
    topOrigin: 'https://ads.third-party.test',
    ancestors: ['https://ads.third-party.test'],
    includeAncestorOrigins: false,
  });
  // Simulate a cross-origin parent whose location is inaccessible.
  Object.defineProperty(env.window.parent, 'location', {
    get(): never {
      throw new DOMException('cross-origin', 'SecurityError');
    },
    configurable: true,
  });
  const mediator = new ConditionalPasskeyMediator(env);

  await assertRejectsWith(mediator.get({ publicKey: publicKey() }), 'NotAllowedError');
  assert.equal(calls.length, 0);
});

test('GateError carries DOMException-compatible names', () => {
  assert.equal(new GateError('TypeError', 'x').name, 'TypeError');
  assert.equal(new GateError('InvalidStateError', 'x').name, 'InvalidStateError');
  assert.equal(new GateError('NotAllowedError', 'x').name, 'NotAllowedError');
});

test('recorded calls always carry an empty allowCredentials and conditional mediation', async () => {
  const { env, calls } = buildFakeEnv();
  const mediator = new ConditionalPasskeyMediator(env);

  const promise = mediator.get({
    publicKey: publicKey({ allowCredentials: undefined, timeout: 1000 }),
  });
  await settleMicrotasks();
  const call: RecordedCall = calls[0];
  assert.deepEqual(call.request.publicKey.allowCredentials, []);
  assert.equal(call.request.mediation, 'conditional');
  assert.equal(call.request.publicKey.timeout, 1000);
  calls[0].resolve({ id: 'ok', type: 'public-key', rawId: new ArrayBuffer(0) });
  await promise;
});
