import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ConditionalMediationGate } from '../src/gate.ts';
import { GateError } from '../src/types.ts';
import {
  buildFakeEnv,
  type BuiltFakeEnv,
} from './fakeEnv.ts';

const PUBLIC_KEY = { challenge: 'c2VjdXJlLWNoYWxsZW5nZQ', rpId: 'login.example' };

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

async function assertRejectedWithCode(
  promise: Promise<unknown>,
  code: string,
): Promise<void> {
  await assert.rejects(
    promise,
    (error: unknown) =>
      error instanceof GateError && error.code === code,
    `expected GateError with code ${code}`,
  );
}

function assertBrowserInterfaceUntouched(fake: BuiltFakeEnv): void {
  assert.equal(
    fake.credentials.calls.length,
    0,
    'navigator.credentials.get must never be reached on a blocked path',
  );
}

test('top-level secure frame with granted permission reaches credentials.get', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const result = { id: 'cred-1', type: 'public-key' };
  const pending = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  assert.equal(fake.credentials.calls.length, 1);

  const call = fake.credentials.calls[0];
  assert.equal(call.mediation, 'conditional');
  assert.deepEqual(call.publicKey.allowCredentials, []);
  assert.equal(call.publicKey.rpId, 'login.example');

  fake.credentials.pending[0].resolve(result);
  assert.equal(await pending, result);
});

test('cross-site iframe is blocked before touching the browser interface', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://ads.third-party.test',
    topOrigin: 'throw',
    ancestorOrigins: ['https://login.example'],
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'cross-site-frame',
  );
  assertBrowserInterfaceUntouched(fake);
});

test('cross-site iframe denied even when the permission policy is granted', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://ads.third-party.test',
    topOrigin: 'throw',
    ancestorOrigins: ['https://shop.example'],
    permission: 'granted',
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'cross-site-frame',
  );
  assert.equal(fake.credentials.calls.length, 0);
});

test('same-site nested frame with same-site ancestor is allowed', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://auth.example.com',
    topOrigin: 'throw',
    ancestorOrigins: ['https://www.example.com'],
  });
  const gate = new ConditionalMediationGate(fake.env);

  const pending = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  assert.equal(fake.credentials.calls.length, 1);
  fake.credentials.pending[0].resolve({ id: 'ok' });
  await pending;
});

test('one cross-site ancestor in the chain blocks the whole frame', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://auth.example',
    topOrigin: 'throw',
    ancestorOrigins: ['https://auth.example', 'https://evil.other.test'],
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'cross-site-frame',
  );
  assert.equal(fake.credentials.calls.length, 0);
});

test('embedded frame without verifiable ancestry fails closed', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://auth.example',
    topOrigin: 'throw',
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'cross-site-frame',
  );
  assert.equal(fake.credentials.calls.length, 0);
});

test('insecure context is blocked', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'http://login.example',
    topOrigin: 'http://login.example',
    isSecureContext: false,
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'insecure-context',
  );
  assertBrowserInterfaceUntouched(fake);
});

test('opaque origin is blocked', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'null',
    topOrigin: 'null',
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'opaque-origin',
  );
  assertBrowserInterfaceUntouched(fake);
});

test('denied permissions policy blocks the request', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
    permission: 'denied',
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'permission-policy-blocked',
  );
  assert.equal(fake.credentials.calls.length, 0);
});

test('prompt permissions policy state is treated as blocked', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
    permission: 'prompt',
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'permission-policy-blocked',
  );
});

test('missing permissions query interface fails closed', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
    permission: 'unavailable',
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'permission-query-unavailable',
  );
  assert.equal(fake.credentials.calls.length, 0);
});

test('non-empty allowCredentials is rejected before the browser call', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet({
      ...PUBLIC_KEY,
      allowCredentials: [{ id: 'probe-specific-credential-id' }],
    }),
    'non-empty-allow-credentials',
  );
  assert.equal(fake.credentials.calls.length, 0);
  assert.equal(
    fake.permissionQueries.length,
    1,
    'the permissions policy is still checked before the options are rejected',
  );
});

test('allowCredentials is forced to empty on the wire even if provided', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const pending = gate.conditionalGet({ ...PUBLIC_KEY, allowCredentials: [] });
  await settle();
  assert.deepEqual(
    fake.credentials.calls[0].publicKey.allowCredentials,
    [],
  );
  fake.credentials.pending[0].resolve({ id: 'ok' });
  await pending;
});

test('a second request aborts the first and keeps only one in flight', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const first = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  const firstRejection = first.then(
    () => assert.fail('superseded request must not resolve'),
    (error: unknown) => error,
  );
  const second = gate.conditionalGet(PUBLIC_KEY);
  await settle();

  assert.equal(fake.credentials.calls.length, 2);
  assert.equal(
    fake.credentials.calls[0].signal.aborted,
    true,
    'the superseded request must be aborted',
  );
  assert.equal(
    fake.credentials.calls[1].signal.aborted,
    false,
    'the new request stays active',
  );

  const firstError = await firstRejection;
  assert.ok(firstError instanceof DOMException);
  assert.equal(firstError.name, 'AbortError');

  fake.credentials.pending[1].resolve({ id: 'second-cred' });
  assert.deepEqual(await second, { id: 'second-cred' });
});

test('a blocked duplicate leaves the in-flight request untouched', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const first = gate.conditionalGet(PUBLIC_KEY);
  await settle();

  await assertRejectedWithCode(
    gate.conditionalGet({
      ...PUBLIC_KEY,
      allowCredentials: [{ id: 'probe' }],
    }),
    'non-empty-allow-credentials',
  );

  assert.equal(
    fake.credentials.calls[0].signal.aborted,
    false,
    'gate failures must not cancel an existing valid request',
  );
  fake.credentials.pending[0].resolve({ id: 'still-ok' });
  assert.deepEqual(await first, { id: 'still-ok' });
});

test('page navigation aborts the in-flight conditional request', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const pending = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  fake.emitter.emit('pagehide');

  assert.equal(fake.credentials.calls[0].signal.aborted, true);
  await assert.rejects(pending, (error: unknown) =>
    error instanceof DOMException && error.name === 'AbortError',
  );
});

test('a response arriving after navigation is never delivered to the page', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const pending = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  fake.emitter.emit('pagehide');

  // Even if the platform resolves instead of rejecting (late response), the
  // gate must swallow it so no stale credential reaches the old page.
  const lateResult = { id: 'stale-credential' };
  fake.credentials.pending[0].resolve(lateResult);

  await assert.rejects(
    pending,
    (error: unknown) =>
      error instanceof DOMException && error.name === 'AbortError',
    'late credentials must be discarded, never delivered to the page',
  );

  // A subsequent request starts from a clean slate: no stale active slot.
  const next = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  assert.equal(fake.credentials.calls.length, 2);
  assert.equal(fake.credentials.calls[1].signal.aborted, false);
  fake.credentials.pending[1].resolve({ id: 'fresh' });
  assert.deepEqual(await next, { id: 'fresh' });
});

test('a request starting after navigation still gets abort protection', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const first = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  fake.emitter.emit('pagehide');
  await assert.rejects(first, (error: unknown) =>
    error instanceof DOMException && error.name === 'AbortError',
  );

  // The listener is attached once and must keep referencing the active slot.
  const second = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  assert.equal(fake.credentials.calls.length, 2);
  fake.emitter.emit('pagehide');
  assert.equal(fake.credentials.calls[1].signal.aborted, true);
  await assert.rejects(second, (error: unknown) =>
    error instanceof DOMException && error.name === 'AbortError',
  );
});

test('explicit abort cancels the current request', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const pending = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  gate.abort('teardown');
  await assert.rejects(pending, (error: unknown) =>
    error instanceof DOMException && error.name === 'AbortError',
  );
});

test('missing credentials container is reported without a call', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example',
    topOrigin: 'https://login.example',
    credentialsAvailable: false,
  });
  const gate = new ConditionalMediationGate(fake.env);

  await assertRejectedWithCode(
    gate.conditionalGet(PUBLIC_KEY),
    'credentials-unavailable',
  );
});

test('abort with no in-flight request is a no-op', () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example.com',
    topOrigin: 'https://login.example.com',
  });
  const gate = new ConditionalMediationGate(fake.env);
  assert.doesNotThrow(() => gate.abort());
});

test('the permission query names publickey-credentials-get exactly', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example.com',
    topOrigin: 'https://login.example.com',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const pending = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  assert.deepEqual(fake.permissionQueries, [
    { name: 'publickey-credentials-get' },
  ]);
  fake.credentials.pending[0].resolve({ id: 'ok' });
  await pending;
});

test('a third request supersedes the second without resurrecting the first', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example.com',
    topOrigin: 'https://login.example.com',
  });
  const gate = new ConditionalMediationGate(fake.env);

  const first = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  const firstError = first.then(
    () => assert.fail('first must reject'),
    (error: unknown) => error,
  );
  const second = gate.conditionalGet(PUBLIC_KEY);
  await settle();
  const secondError = second.then(
    () => assert.fail('second must reject'),
    (error: unknown) => error,
  );
  const third = gate.conditionalGet(PUBLIC_KEY);
  await settle();

  assert.equal(fake.credentials.calls.length, 3);
  assert.equal(fake.credentials.calls[0].signal.aborted, true);
  assert.equal(fake.credentials.calls[1].signal.aborted, true);
  assert.equal(fake.credentials.calls[2].signal.aborted, false);

  assert.equal((await firstError) instanceof DOMException, true);
  assert.equal((await secondError) instanceof DOMException, true);

  fake.credentials.pending[2].resolve({ id: 'third-cred' });
  assert.deepEqual(await third, { id: 'third-cred' });
});
