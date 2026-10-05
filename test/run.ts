import assert from 'node:assert';
import {
  merge,
  MigrationConflictError,
  Migrator,
  rename,
  split,
  transform,
} from '../src';

let passed = 0;
let failed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL ${name}`);
    console.error(err instanceof Error ? err.stack : err);
  }
}

// --- rename: basic migration, provenance, unconsumed -----------------------

test('rename migrates a key and reports provenance + unconsumed', () => {
  const m = new Migrator([rename('user-name', 'userName', 'user.name')]);
  const r = m.migrate({ userName: 'ada', legacyFlag: true });
  assert.deepStrictEqual(r.config, { 'user.name': 'ada' });
  assert.deepStrictEqual(r.provenance['user.name'], { rule: 'user-name', from: ['userName'] });
  assert.deepStrictEqual(r.unconsumed, ['legacyFlag']);
  assert.deepStrictEqual(r.losses, []);
  assert.deepStrictEqual(r.appliedRules, ['user-name']);
});

test('carryUnknown copies unconsumed keys into the output', () => {
  const m = new Migrator([rename('user-name', 'userName', 'user.name')]);
  const r = m.migrate({ userName: 'ada', legacyFlag: true }, { carryUnknown: true });
  assert.deepStrictEqual(r.config, { 'user.name': 'ada', legacyFlag: true });
  assert.deepStrictEqual(r.unconsumed, ['legacyFlag']);
  assert.deepStrictEqual(r.provenance['legacyFlag'], { rule: null, from: ['legacyFlag'] });
});

// --- split: one-to-many ----------------------------------------------------

test('split maps one key to many and reverts losslessly', () => {
  const m = new Migrator([split('name', 'name', ['firstName', 'lastName'], ' ')]);
  const r = m.migrate({ name: 'Ada Lovelace' });
  assert.deepStrictEqual(r.config, { firstName: 'Ada', lastName: 'Lovelace' });
  assert.deepStrictEqual(r.losses, []);

  const back = m.revert(r.config);
  assert.deepStrictEqual(back.config, { name: 'Ada Lovelace' });
  assert.deepStrictEqual(back.losses, []);
});

test('split keeps extra separators in the last part (lossless)', () => {
  const m = new Migrator([split('name', 'name', ['firstName', 'lastName'], ' ')]);
  const r = m.migrate({ name: 'John Ronald Reuel Tolkien' });
  assert.deepStrictEqual(r.config, { firstName: 'John', lastName: 'Ronald Reuel Tolkien' });
  assert.deepStrictEqual(r.losses, []);
  assert.deepStrictEqual(m.revert(r.config).config, { name: 'John Ronald Reuel Tolkien' });
});

test('split records a loss when parts are missing', () => {
  const m = new Migrator([split('name', 'name', ['firstName', 'lastName'], ' ')]);
  const r = m.migrate({ name: 'Madonna' });
  assert.deepStrictEqual(r.config, { firstName: 'Madonna' });
  assert.strictEqual(r.losses.length, 1);
  assert.strictEqual(r.losses[0].rule, 'name');
  assert.strictEqual(r.losses[0].phase, 'forward');
  assert.deepStrictEqual(r.losses[0].keys, ['name']);
  assert.strictEqual(r.losses[0].value, 'Madonna');

  const back = m.revert(r.config);
  assert.deepStrictEqual(back.config, { name: 'Madonna' });
  assert.strictEqual(back.losses.length, 1);
  assert.strictEqual(back.losses[0].phase, 'backward');
});

// --- merge: many-to-one ----------------------------------------------------

test('merge combines keys and reverts losslessly', () => {
  const m = new Migrator([merge('endpoint', ['host', 'port'], 'endpoint', ':')]);
  const r = m.migrate({ host: 'example.com', port: '8080' });
  assert.deepStrictEqual(r.config, { endpoint: 'example.com:8080' });
  assert.deepStrictEqual(r.losses, []);

  const back = m.revert(r.config);
  assert.deepStrictEqual(back.config, { host: 'example.com', port: '8080' });
  assert.deepStrictEqual(back.losses, []);
});

test('merge records a loss when a source key is absent', () => {
  const m = new Migrator([merge('endpoint', ['host', 'port'], 'endpoint', ':')]);
  const r = m.migrate({ host: 'example.com' });
  assert.deepStrictEqual(r.config, { endpoint: 'example.com' });
  assert.strictEqual(r.losses.length, 1);
  assert.deepStrictEqual(r.losses[0].keys, ['port']);

  const back = m.revert(r.config);
  assert.deepStrictEqual(back.config, { host: 'example.com' });
  assert.strictEqual(back.losses.length, 1);
});

// --- preconditions and priority ---------------------------------------------

test('precondition gates a rule on the source config', () => {
  const m = new Migrator([
    rename('v1-legacy', 'oldKey', 'newKey', { when: (s) => s.version === 1 }),
  ]);
  const migrated = m.migrate({ version: 1, oldKey: 'x' });
  assert.deepStrictEqual(migrated.config, { newKey: 'x' });

  const skipped = m.migrate({ version: 2, oldKey: 'x' });
  assert.deepStrictEqual(skipped.config, {});
  assert.deepStrictEqual(skipped.unconsumed.sort(), ['oldKey', 'version']);
  assert.deepStrictEqual(skipped.appliedRules, []);
});

test('priority orders execution; later rules can see earlier output', () => {
  const m = new Migrator([
    transform({
      id: 'host-port-fallback',
      priority: 0,
      from: ['dbHost', 'dbPort'],
      to: ['db.url'],
      forward: (v, ctx) =>
        ctx.has('db.url') ? {} : { 'db.url': `${v.dbHost}:${v.dbPort}` },
      backward: (v) => {
        const [host, port] = String(v['db.url']).split(':');
        return { dbHost: host, dbPort: port };
      },
    }),
    rename('url-direct', 'databaseUrl', 'db.url', { priority: 10 }),
  ]);

  // Higher-priority direct mapping wins; fallback sees db.url and stands down.
  const r = m.migrate({ databaseUrl: 'db.internal:5432', dbHost: 'ignored', dbPort: '0000' });
  assert.deepStrictEqual(r.config, { 'db.url': 'db.internal:5432' });
  assert.deepStrictEqual(r.provenance['db.url'], { rule: 'url-direct', from: ['databaseUrl'] });
  // The fallback fired and deliberately produced nothing, so its inputs count as consumed.
  assert.deepStrictEqual(r.unconsumed, []);

  // Without the direct key, the fallback fills db.url in.
  const r2 = m.migrate({ dbHost: 'db.internal', dbPort: '5432' });
  assert.deepStrictEqual(r2.config, { 'db.url': 'db.internal:5432' });
  assert.deepStrictEqual(r2.provenance['db.url'], {
    rule: 'host-port-fallback',
    from: ['dbHost', 'dbPort'],
  });
});

// --- conflicts ---------------------------------------------------------------

test('old and new keys that agree migrate cleanly', () => {
  const m = new Migrator([rename('user-name', 'userName', 'user.name')]);
  const r = m.migrate({ userName: 'ada', 'user.name': 'ada' });
  assert.deepStrictEqual(r.config, { 'user.name': 'ada' });
  assert.deepStrictEqual(r.provenance['user.name'], { rule: 'user-name', from: ['userName'] });
  assert.deepStrictEqual(r.unconsumed, []);
});

test('old and new keys that disagree are rejected, not guessed', () => {
  const m = new Migrator([merge('endpoint', ['host', 'port'], 'endpoint', ':')]);
  let caught: MigrationConflictError | undefined;
  try {
    m.migrate({ host: 'a.com', port: '80', endpoint: 'other.com:80' });
  } catch (err) {
    caught = err as MigrationConflictError;
  }
  assert.ok(caught instanceof MigrationConflictError);
  assert.strictEqual(caught.conflicts.length, 1);
  assert.strictEqual(caught.conflicts[0].key, 'endpoint');
  assert.strictEqual(caught.conflicts[0].existing, 'other.com:80');
  assert.strictEqual(caught.conflicts[0].incoming, 'a.com:80');
});

// --- idempotency -------------------------------------------------------------

test('repeated migration is a fixed point', () => {
  const m = new Migrator([
    rename('user-name', 'userName', 'user.name'),
    split('name', 'fullName', ['firstName', 'lastName'], ' '),
    merge('endpoint', ['host', 'port'], 'endpoint', ':'),
  ]);
  const once = m.migrate({
    userName: 'ada',
    fullName: 'Ada Lovelace',
    host: 'example.com',
    port: '8080',
  });
  const twice = m.migrate(once.config);
  assert.deepStrictEqual(twice.config, once.config);
  assert.deepStrictEqual(twice.unconsumed, []);
  assert.deepStrictEqual(twice.losses, []);
  assert.deepStrictEqual(twice.appliedRules, []);
});

// --- reversibility reporting ---------------------------------------------------

test('revert reports non-reversible rules as losses', () => {
  const m = new Migrator([
    rename('user-name', 'userName', 'user.name'),
    transform({
      id: 'hash-password',
      from: ['password'],
      to: ['passwordHash'],
      forward: (v) => ({ passwordHash: `sha1(${String(v.password)})` }),
    }),
  ]);
  const fwd = m.migrate({ userName: 'ada', password: 'secret' });
  assert.deepStrictEqual(fwd.config, { 'user.name': 'ada', passwordHash: 'sha1(secret)' });

  const back = m.revert(fwd.config);
  assert.deepStrictEqual(back.config, { userName: 'ada' });
  assert.strictEqual(back.losses.length, 1);
  assert.strictEqual(back.losses[0].rule, 'hash-password');
  assert.deepStrictEqual(back.losses[0].value, { passwordHash: 'sha1(secret)' });
});

test('full round-trip restores the original config', () => {
  const m = new Migrator([
    rename('user-name', 'userName', 'user.name'),
    split('name', 'fullName', ['firstName', 'lastName'], ' '),
    merge('endpoint', ['host', 'port'], 'endpoint', ':'),
  ]);
  const original = {
    userName: 'ada',
    fullName: 'Ada Lovelace',
    host: 'example.com',
    port: '8080',
  };
  const fwd = m.migrate(original);
  const back = m.revert(fwd.config);
  assert.deepStrictEqual(back.config, original);
  assert.deepStrictEqual(back.losses, []);
});

// --- summary -------------------------------------------------------------------

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
