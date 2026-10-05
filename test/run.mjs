import assert from 'node:assert/strict';
import {
  migrate,
  unmigrate,
  rename,
  split,
  merge,
  MigrationConflictError,
  deepEqual,
} from '../dist/index.js';

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

// --- shared rules ---------------------------------------------------------

// one -> many: "host:port" string split into two keys (reversible)
const splitAddress = split(
  'split-address',
  'address',
  ['hostname', 'port'],
  (value) => {
    const [hostname, port] = String(value).split(':');
    return { hostname, port: Number(port) };
  },
  (values) => `${values.hostname}:${values.port}`,
);

// many -> one: first/last name merged into fullName (reversible)
const mergeName = merge(
  'merge-name',
  ['firstName', 'lastName'],
  'fullName',
  (values) => [values.firstName, values.lastName].filter(Boolean).join(' '),
  (value) => {
    const [firstName, ...rest] = String(value).split(' ');
    return { firstName, lastName: rest.join(' ') };
  },
);

// many -> one with a lossy part: timeout + unit -> timeoutMs.
// backward restores only `timeout`; the unit is the recorded loss.
const mergeTimeout = {
  id: 'merge-timeout',
  oldKeys: ['timeout', 'timeoutUnit'],
  newKeys: ['timeoutMs'],
  forward(old) {
    const unit = old.timeoutUnit ?? 'ms';
    const factors = { ms: 1, s: 1000, min: 60000 };
    if (!(unit in factors)) {
      return {
        values: { timeoutMs: old.timeout },
        losses: [
          { key: 'timeoutUnit', reason: `unknown unit "${unit}", treated as ms`, value: unit },
        ],
      };
    }
    return { values: { timeoutMs: old.timeout * factors[unit] } };
  },
  backward(current) {
    return { timeout: current.timeoutMs };
  },
};

// --- tests ----------------------------------------------------------------

test('rename moves a key 1:1 and reports provenance', () => {
  const rule = rename('rename-env', 'env', 'environment');
  const result = migrate({ env: 'prod' }, [rule]);
  assert.deepEqual(result.config, { environment: 'prod' });
  assert.deepEqual(result.appliedRules, ['rename-env']);
  assert.deepEqual(result.provenance, [
    { key: 'environment', ruleId: 'rename-env', sources: ['env'] },
  ]);
  assert.deepEqual(result.losses, []);
  assert.deepEqual(result.unconsumed, []);
});

test('split turns one key into many', () => {
  const result = migrate({ address: 'example.com:8080' }, [splitAddress]);
  assert.deepEqual(result.config, { hostname: 'example.com', port: 8080 });
  assert.deepEqual(result.provenance, [
    { key: 'hostname', ruleId: 'split-address', sources: ['address'] },
    { key: 'port', ruleId: 'split-address', sources: ['address'] },
  ]);
});

test('merge turns many keys into one', () => {
  const result = migrate({ firstName: 'Ada', lastName: 'Lovelace' }, [mergeName]);
  assert.deepEqual(result.config, { fullName: 'Ada Lovelace' });
  assert.deepEqual(result.provenance, [
    { key: 'fullName', ruleId: 'merge-name', sources: ['firstName', 'lastName'] },
  ]);
});

test('rules run in priority order so chained rules see earlier output', () => {
  const renameHost = rename('rename-host', 'legacyHost', 'address', { priority: 10 });
  // splitAddress (priority 0) consumes `address` produced by renameHost.
  const result = migrate({ legacyHost: 'a.b:9000' }, [splitAddress, renameHost]);
  assert.deepEqual(result.config, { hostname: 'a.b', port: 9000 });
  assert.deepEqual(result.appliedRules, ['rename-host', 'split-address']);
});

test('equal priority ties break by rule id for determinism', () => {
  const a = rename('a-rule', 'x', 'x2');
  const b = rename('b-rule', 'y', 'y2');
  const result = migrate({ x: 1, y: 2 }, [b, a]);
  assert.deepEqual(result.appliedRules, ['a-rule', 'b-rule']);
});

test('precondition gates a rule', () => {
  const onlyV1 = rename('v1-only', 'old', 'new', { when: (cfg) => cfg.version === 1 });
  const skipped = migrate({ version: 2, old: 'x' }, [onlyV1]);
  assert.deepEqual(skipped.config, { version: 2, old: 'x' });
  assert.deepEqual(skipped.appliedRules, []);
  assert.deepEqual(skipped.unconsumed, ['version', 'old']);

  const applied = migrate({ version: 1, old: 'x' }, [onlyV1]);
  assert.deepEqual(applied.config, { version: 1, new: 'x' });
});

test('conflicting old and new keys are rejected, not guessed', () => {
  const input = { address: 'a.com:80', hostname: 'other.com', port: 80 };
  assert.throws(() => migrate(input, [splitAddress]), (err) => {
    assert.ok(err instanceof MigrationConflictError);
    assert.equal(err.ruleId, 'split-address');
    assert.equal(err.key, 'hostname');
    assert.equal(err.existing, 'other.com');
    assert.equal(err.incoming, 'a.com');
    return true;
  });
});

test('matching old and new keys are consumed silently', () => {
  const input = { address: 'a.com:80', hostname: 'a.com', port: 80 };
  const result = migrate(input, [splitAddress]);
  assert.deepEqual(result.config, { hostname: 'a.com', port: 80 });
  assert.deepEqual(result.appliedRules, ['split-address']);
});

test('repeated migration is stable (idempotent)', () => {
  const rules = [splitAddress, mergeName, mergeTimeout];
  const once = migrate(
    { address: 'a.com:80', firstName: 'Ada', lastName: 'Lovelace', timeout: 5, timeoutUnit: 's' },
    rules,
  );
  const twice = migrate(once.config, rules);
  assert.deepEqual(twice.config, once.config);
  assert.deepEqual(twice.appliedRules, []);
  assert.deepEqual(twice.provenance, []);
  assert.deepEqual(twice.losses, []);
});

test('lossy transforms record loss entries', () => {
  const result = migrate({ timeout: 5, timeoutUnit: 'fortnights' }, [mergeTimeout]);
  assert.deepEqual(result.config, { timeoutMs: 5 });
  assert.deepEqual(result.losses, [
    {
      ruleId: 'merge-timeout',
      key: 'timeoutUnit',
      reason: 'unknown unit "fortnights", treated as ms',
      value: 'fortnights',
    },
  ]);
});

test('unconsumed old fields are reported', () => {
  const result = migrate({ env: 'dev', mystery: true }, [rename('r', 'env', 'environment')]);
  assert.deepEqual(result.config, { environment: 'dev', mystery: true });
  assert.deepEqual(result.unconsumed, ['mystery']);
});

test('input config is never mutated', () => {
  const input = { address: 'a.com:80', nested: { list: [1, 2] } };
  const snapshot = JSON.parse(JSON.stringify(input));
  migrate(input, [splitAddress]);
  assert.deepEqual(input, snapshot);
});

test('unmigrate reverses reversible rules (round trip)', () => {
  const rules = [splitAddress, mergeName];
  const original = { address: 'a.com:80', firstName: 'Ada', lastName: 'Lovelace' };
  const forward = migrate(original, rules);
  const back = unmigrate(forward.config, rules);
  assert.ok(deepEqual(back.config, original));
  assert.deepEqual(back.losses, []);
  assert.deepEqual(back.provenance, [
    { key: 'address', ruleId: 'split-address', sources: ['hostname', 'port'] },
    { key: 'firstName', ruleId: 'merge-name', sources: ['fullName'] },
    { key: 'lastName', ruleId: 'merge-name', sources: ['fullName'] },
  ]);
});

test('unmigrate restores the reversible part and keeps losses explicit', () => {
  const forward = migrate({ timeout: 5, timeoutUnit: 's' }, [mergeTimeout]);
  assert.deepEqual(forward.config, { timeoutMs: 5000 });
  const back = unmigrate(forward.config, [mergeTimeout]);
  // timeout restored; timeoutUnit is gone for good (loss was recorded forward).
  assert.deepEqual(back.config, { timeout: 5000 });
});

test('unmigrate records a loss for non-reversible rules and leaves keys', () => {
  const oneWay = {
    id: 'one-way',
    oldKeys: ['secret'],
    newKeys: ['secretHash'],
    forward: (old) => ({ values: { secretHash: `hash(${old.secret})` } }),
  };
  const forward = migrate({ secret: 'pw' }, [oneWay]);
  assert.deepEqual(forward.config, { secretHash: 'hash(pw)' });
  const back = unmigrate(forward.config, [oneWay]);
  assert.deepEqual(back.config, { secretHash: 'hash(pw)' });
  assert.equal(back.losses.length, 1);
  assert.equal(back.losses[0].ruleId, 'one-way');
  assert.match(back.losses[0].reason, /not reversible/);
});

test('unmigrate also refuses to guess on conflicts', () => {
  const rules = [rename('r', 'env', 'environment')];
  assert.throws(
    () => unmigrate({ environment: 'prod', env: 'dev' }, rules),
    MigrationConflictError,
  );
});

test('rule producing undeclared keys is a rule bug and throws', () => {
  const buggy = {
    id: 'buggy',
    oldKeys: ['a'],
    newKeys: ['b'],
    forward: () => ({ values: { c: 1 } }),
  };
  assert.throws(() => migrate({ a: 1 }, [buggy]), /undeclared key "c"/);
});

// --- runner ---------------------------------------------------------------

let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok   ${name}`);
  } catch (err) {
    failed += 1;
    console.error(`FAIL ${name}`);
    console.error(err);
  }
}
console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
process.exit(failed === 0 ? 0 : 1);
