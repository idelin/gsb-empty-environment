import { test } from 'node:test';
import assert from 'node:assert/strict';
import { conditionalGet, GateError } from '../index.ts';
import { buildFakeEnv } from './fakeEnv.ts';

test('package entry runs the full gate and forwards the credential', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://login.example.com',
    topOrigin: 'https://login.example.com',
  });

  const pending = conditionalGet(
    { challenge: 'c2VjdXJl', rpId: 'login.example.com' },
    fake.env,
  );
  await new Promise((resolve) => setTimeout(resolve, 0));

  assert.equal(fake.credentials.calls.length, 1);
  assert.deepEqual(
    fake.credentials.calls[0].publicKey.allowCredentials,
    [],
  );
  assert.equal(fake.credentials.calls[0].mediation, 'conditional');

  fake.credentials.pending[0].resolve({ id: 'entry-cred' });
  assert.deepEqual(await pending, { id: 'entry-cred' });
});

test('package entry rejects cross-site frames with a GateError code', async () => {
  const fake = buildFakeEnv({
    selfOrigin: 'https://ads.third-party.test',
    topOrigin: 'throw',
    ancestorOrigins: ['https://login.example.com'],
  });

  await assert.rejects(
    conditionalGet({ challenge: 'c2VjdXJl' }, fake.env),
    (error: unknown) =>
      error instanceof GateError && error.code === 'cross-site-frame',
  );
  assert.equal(fake.credentials.calls.length, 0);
});
