import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkCompatibility } from '../src/compat.ts';
import { validate, type Schema } from '../src/schema.ts';

const userV1: Schema = {
  kind: 'object',
  fields: {
    id: { schema: { kind: 'enum', values: ['a', 'b', 'c'] }, required: true },
    role: { schema: { kind: 'enum', values: ['admin', 'user'] }, required: true },
    email: { schema: { kind: 'enum', values: ['x@y.z'] }, nullable: true },
    tags: {
      schema: {
        kind: 'array',
        items: { kind: 'enum', values: ['t1', 't2'] },
        minLength: 0,
        maxLength: 3,
      },
    },
  },
};

test('identical schemas are compatible', () => {
  assert.deepEqual(checkCompatibility(userV1, userV1), { compatible: true });
});

test('new optional field is compatible', () => {
  const v2: Schema = {
    ...userV1,
    fields: {
      ...userV1.fields,
      nickname: { schema: { kind: 'enum', values: ['n'] } },
    },
  };
  assert.deepEqual(checkCompatibility(userV1, v2), { compatible: true });
});

test('new required field is not ignorable: incompatible, record omits it', () => {
  const v2: Schema = {
    ...userV1,
    fields: {
      ...userV1.fields,
      tenant: { schema: { kind: 'enum', values: ['acme'] }, required: true },
    },
  };
  const r = checkCompatibility(userV1, v2);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, { id: 'a', role: 'admin' });
  assert.deepEqual(r.path, ['tenant']);
  assert.ok(validate(userV1, r.counterexample));
  assert.ok(!validate(v2, r.counterexample));
});

test('enum domain shrink yields the removed value', () => {
  const v2: Schema = {
    ...userV1,
    fields: {
      ...userV1.fields,
      role: { schema: { kind: 'enum', values: ['user'] }, required: true },
    },
  };
  const r = checkCompatibility(userV1, v2);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, { id: 'a', role: 'admin' });
  assert.deepEqual(r.path, ['role']);
});

test('nullable dropped in new schema yields null at that field', () => {
  const v2: Schema = {
    ...userV1,
    fields: {
      ...userV1.fields,
      email: { schema: { kind: 'enum', values: ['x@y.z'] } },
    },
  };
  const r = checkCompatibility(userV1, v2);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, { id: 'a', role: 'admin', email: null });
  assert.deepEqual(r.path, ['email']);
});

test('array minLength raised yields shortest old array', () => {
  const old: Schema = { kind: 'array', items: { kind: 'enum', values: ['t'] }, minLength: 1, maxLength: 4 };
  const next: Schema = { kind: 'array', items: { kind: 'enum', values: ['t'] }, minLength: 2, maxLength: 4 };
  const r = checkCompatibility(old, next);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, ['t']);
});

test('array maxLength lowered respects old minLength when exceeding', () => {
  const old: Schema = { kind: 'array', items: { kind: 'enum', values: ['t'] }, minLength: 5, maxLength: 10 };
  const next: Schema = { kind: 'array', items: { kind: 'enum', values: ['t'] }, minLength: 0, maxLength: 3 };
  const r = checkCompatibility(old, next);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.equal((r.counterexample as unknown[]).length, 5); // max(oldMin, newMax+1)
  assert.ok(validate(old, r.counterexample));
  assert.ok(!validate(next, r.counterexample));
});

test('nested object violation surfaces deep path and minimal record', () => {
  const old: Schema = {
    kind: 'object',
    fields: {
      profile: {
        schema: {
          kind: 'object',
          fields: {
            level: { schema: { kind: 'enum', values: ['low', 'high'] }, required: true },
          },
        },
        required: true,
      },
    },
  };
  const next: Schema = {
    kind: 'object',
    fields: {
      profile: {
        schema: {
          kind: 'object',
          fields: {
            level: { schema: { kind: 'enum', values: ['low'] }, required: true },
          },
        },
        required: true,
      },
    },
  };
  const r = checkCompatibility(old, next);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, { profile: { level: 'high' } });
  assert.deepEqual(r.path, ['profile', 'level']);
});

test('array item violation wraps counterexample in minimal-length array', () => {
  const old: Schema = {
    kind: 'object',
    fields: {
      tags: {
        schema: { kind: 'array', items: { kind: 'enum', values: ['x', 'y'] }, minLength: 0 },
        required: true,
      },
    },
  };
  const next: Schema = {
    kind: 'object',
    fields: {
      tags: {
        schema: { kind: 'array', items: { kind: 'enum', values: ['x'] }, minLength: 0 },
        required: true,
      },
    },
  };
  const r = checkCompatibility(old, next);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, { tags: ['y'] });
  assert.deepEqual(r.path, ['tags', 0]);
});

test('old-only field rejected by closed new object', () => {
  const old: Schema = {
    kind: 'object',
    fields: { a: { schema: { kind: 'enum', values: ['1'] }, required: true } },
  };
  const next: Schema = { kind: 'object', fields: {} };
  const r = checkCompatibility(old, next);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, { a: '1' });
});

test('enum domain growth is compatible', () => {
  const old: Schema = { kind: 'enum', values: ['a'] };
  const next: Schema = { kind: 'enum', values: ['a', 'b'] };
  assert.deepEqual(checkCompatibility(old, next), { compatible: true });
});

test('counterexample is minimal: optional fields omitted', () => {
  const old: Schema = {
    kind: 'object',
    fields: {
      keep: { schema: { kind: 'enum', values: ['k'] }, required: true },
      opt: { schema: { kind: 'enum', values: ['o1', 'o2'] } },
    },
  };
  const next: Schema = {
    kind: 'object',
    fields: {
      keep: { schema: { kind: 'enum', values: ['k'] }, required: true },
      opt: { schema: { kind: 'enum', values: ['o1'] } },
    },
  };
  const r = checkCompatibility(old, next);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.deepEqual(r.counterexample, { keep: 'k', opt: 'o2' });
});

test('kind mismatch is incompatible', () => {
  const old: Schema = { kind: 'enum', values: ['a'] };
  const next: Schema = { kind: 'array', items: { kind: 'enum', values: ['a'] } };
  const r = checkCompatibility(old, next);
  assert.equal(r.compatible, false);
  if (r.compatible) return;
  assert.equal(r.counterexample, 'a');
});

test('validate sanity: required, nullable, closed objects, array bounds', () => {
  assert.ok(validate(userV1, { id: 'a', role: 'user' }));
  assert.ok(validate(userV1, { id: 'a', role: 'user', email: null, tags: ['t1', 't2'] }));
  assert.ok(!validate(userV1, { role: 'user' })); // missing required id
  assert.ok(!validate(userV1, { id: 'z', role: 'user' })); // enum outside domain
  assert.ok(!validate(userV1, { id: 'a', role: 'user', extra: 1 })); // unknown key
  assert.ok(!validate(userV1, { id: 'a', role: 'user', tags: ['t1', 't1', 't1', 't1'] })); // > maxLength
});
