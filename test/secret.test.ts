import assert from 'node:assert/strict';
import test from 'node:test';
import { inspect } from 'node:util';
import { mergeConfig } from '../src/merge.ts';
import { SecretValue } from '../src/secret.ts';
import { fromArgv, fromDefaults, fromEnv } from '../src/sources.ts';

const schema = { db: { password: 'secret:string' }, retries: 'number' };

function build(passwordEntries: ReturnType<typeof fromDefaults>[]) {
  return mergeConfig(
    [
      ...fromDefaults({ db: { password: 'default-pw' }, retries: 3 }),
      ...passwordEntries.flat(),
    ],
    { schema },
  );
}

test('secret values are masked in display but revealable', () => {
  const config = build([fromEnv({ APP_DB__PASSWORD: 's3cret' }, { prefix: 'APP_' })]);
  const value = config.get<SecretValue>('db.password');
  assert.ok(value instanceof SecretValue);
  assert.equal(String(value), '********');
  assert.equal(JSON.stringify(value), '"********"');
  assert.equal(inspect(value), 'Secret(********)');
  assert.equal(value.reveal(), 's3cret');
});

test('toJSON masks secrets while keeping other values readable', () => {
  const config = build([fromEnv({ APP_DB__PASSWORD: 's3cret' }, { prefix: 'APP_' })]);
  assert.deepEqual(config.toJSON(), { db: { password: '********' }, retries: 3 });
  assert.ok(!JSON.stringify(config.toJSON()).includes('s3cret'));
});

test('secrets stay comparable for change detection', () => {
  const same = build([fromEnv({ APP_DB__PASSWORD: 's3cret' }, { prefix: 'APP_' })]);
  const changed = build([fromArgv(['--db.password=other'])]);
  const baseline = build([]);

  assert.deepEqual(same.diff(changed), [{ path: 'db.password', change: 'changed' }]);
  assert.deepEqual(
    same.diff(build([fromEnv({ APP_DB__PASSWORD: 's3cret' }, { prefix: 'APP_' })])),
    [],
  );
  // Baseline differs from both, and the diff never exposes the raw secret.
  const diff = baseline.diff(same);
  assert.deepEqual(diff, [{ path: 'db.password', change: 'changed' }]);
  assert.ok(!JSON.stringify(diff).includes('s3cret'));
});

test('secret equality works across wrapper and raw values', () => {
  const a = new SecretValue('pw');
  assert.ok(a.equals(new SecretValue('pw')));
  assert.ok(a.equals('pw'));
  assert.ok(!a.equals(new SecretValue('nope')));
});

test('explain masks secret values in the override chain', () => {
  const config = build([fromEnv({ APP_DB__PASSWORD: 's3cret' }, { prefix: 'APP_' })]);
  const text = config.explainText('db.password');
  assert.ok(!text.includes('s3cret'));
  assert.ok(!text.includes('default-pw'));
  assert.match(text, /\*\*\*\*\*\*\*\*/);
});
