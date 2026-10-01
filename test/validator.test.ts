import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate } from '../src/index.ts';

const userSchema = {
  type: 'object',
  required: ['name', 'age'],
  properties: {
    name: { type: 'string', minLength: 1, pattern: '^[A-Z]' },
    age: { type: 'integer', minimum: 0, maximum: 150 },
    tags: { type: 'array', items: { type: 'string' }, maxItems: 3 },
  },
  additionalProperties: false,
};

test('valid instance returns valid with no errors', () => {
  const result = validate(userSchema, { name: 'Ada', age: 36, tags: ['admin'] });
  assert.equal(result.status, 'valid');
  assert.deepEqual(result.errors, []);
  assert.equal(result.reason, undefined);
});

test('invalid instance returns invalid with instance and schema paths', () => {
  const result = validate(userSchema, { name: 'ada', age: 200, extra: true });
  assert.equal(result.status, 'invalid');
  const byPath = new Map(result.errors.map((e) => [e.instancePath + ' @ ' + e.schemaPath, e]));
  assert.ok(byPath.has('/name @ /properties/name/pattern'), JSON.stringify(result.errors, null, 2));
  assert.ok(byPath.has('/age @ /properties/age/maximum'));
  assert.ok(byPath.has('/extra @ /additionalProperties'));
});

test('missing required property reports instance path of the object', () => {
  const result = validate(userSchema, { age: 10 });
  assert.equal(result.status, 'invalid');
  assert.ok(result.errors.some((e) => e.instancePath === '' && e.schemaPath === '/required'));
});

test('nested errors carry full instance and schema pointers', () => {
  const schema = {
    type: 'object',
    properties: {
      users: { type: 'array', items: { type: 'object', properties: { email: { type: 'string', pattern: '@' } } } },
    },
  };
  const result = validate(schema, { users: [{ email: 'ok@x' }, { email: 'nope' }] });
  assert.equal(result.status, 'invalid');
  assert.deepEqual(result.errors[0].instancePath, '/users/1/email');
  assert.deepEqual(result.errors[0].schemaPath, '/properties/users/items/properties/email/pattern');
});

test('budget exhaustion returns incomplete, not invalid', () => {
  const result = validate(userSchema, { name: 'Ada', age: 36 }, { budget: 3 });
  assert.equal(result.status, 'incomplete');
  assert.equal(result.reason, 'budget-exhausted');
  assert.ok(result.budgetUsed <= result.budgetLimit);
});

test('duplicate $refs cannot bypass the budget via caching', () => {
  // 500 properties all referencing the same cached definition.
  const properties: Record<string, unknown> = {};
  const instance: Record<string, unknown> = {};
  for (let i = 0; i < 500; i++) {
    properties['f' + i] = { $ref: '#/$defs/positive' };
    instance['f' + i] = i;
  }
  const schema = { type: 'object', properties, $defs: { positive: { type: 'integer', minimum: 0 } } };
  const result = validate(schema, instance, { budget: 1_000 });
  assert.equal(result.status, 'incomplete');
  assert.equal(result.reason, 'budget-exhausted');
  // same input with enough budget validates fine
  const ample = validate(schema, instance, { budget: 100_000 });
  assert.equal(ample.status, 'valid');
});

test('$ref errors point at the resolved rule location', () => {
  const schema = {
    type: 'object',
    properties: { age: { $ref: '#/$defs/positive' } },
    $defs: { positive: { type: 'integer', minimum: 0 } },
  };
  const result = validate(schema, { age: -5 });
  assert.equal(result.status, 'invalid');
  assert.equal(result.errors[0].instancePath, '/age');
  assert.equal(result.errors[0].schemaPath, '/$defs/positive/minimum');
});

test('long strings make pattern matching consume the shared budget', () => {
  const schema = { type: 'object', properties: { blob: { type: 'string', pattern: '^a+$' } } };
  const instance = { blob: 'a'.repeat(50_000) };
  const tight = validate(schema, instance, { budget: 1_000 });
  assert.equal(tight.status, 'incomplete');
  assert.equal(tight.reason, 'budget-exhausted');
  const ample = validate(schema, instance, { budget: 1_000_000 });
  assert.equal(ample.status, 'valid');
});

test('pre-aborted signal cancels before any work and cleans up', () => {
  const controller = new AbortController();
  controller.abort();
  const result = validate(userSchema, { name: 'Ada', age: 36 }, { signal: controller.signal });
  assert.equal(result.status, 'incomplete');
  assert.equal(result.reason, 'cancelled');
  assert.deepEqual(result.errors, []);
});

test('aborting mid-run cancels and the next call is unaffected', () => {
  const controller = new AbortController();
  const instance = {
    name: 'Ada',
    get age() {
      controller.abort(); // cancel while the validator is walking properties
      return 36;
    },
  };
  const result = validate(userSchema, instance, { signal: controller.signal });
  assert.equal(result.status, 'incomplete');
  assert.equal(result.reason, 'cancelled');
  // temporary state was cleaned up: a fresh call with the same schema works
  const after = validate(userSchema, { name: 'Ada', age: 36 });
  assert.equal(after.status, 'valid');
});

test('error limit yields incomplete, distinct from invalid', () => {
  const schema = { type: 'object', properties: Object.fromEntries(Array.from({ length: 20 }, (_, i) => ['p' + i, { type: 'string' }])) };
  const instance = Object.fromEntries(Array.from({ length: 20 }, (_, i) => ['p' + i, i])); // all wrong type
  const capped = validate(schema, instance, { maxErrors: 5 });
  assert.equal(capped.status, 'incomplete');
  assert.equal(capped.reason, 'error-limit-reached');
  assert.equal(capped.errors.length, 5);
  const uncapped = validate(schema, instance);
  assert.equal(uncapped.status, 'invalid');
  assert.equal(uncapped.errors.length, 20);
});

test('an exhausted run does not poison the next call', () => {
  const instance = { name: 'Ada', age: 36 };
  const first = validate(userSchema, instance, { budget: 2 });
  assert.equal(first.status, 'incomplete');
  const second = validate(userSchema, instance, { budget: 2 });
  assert.equal(second.status, 'incomplete'); // deterministic, not worse
  const third = validate(userSchema, instance);
  assert.equal(third.status, 'valid'); // fresh budget every call
});

test('circular $ref is reported, depth-bombs are contained', () => {
  const circular = validate({ $ref: '#' }, 1, { budget: 1_000 });
  assert.equal(circular.status, 'invalid');
  assert.match(circular.errors[0].message, /circular/);

  const mutual = validate(
    { $ref: '#/$defs/a', $defs: { a: { $ref: '#/$defs/b' }, b: { $ref: '#/$defs/a' } } },
    1,
    { budget: 1_000_000 },
  );
  assert.equal(mutual.status, 'invalid');
  assert.match(mutual.errors[0].message, /circular/);

  // deeply nested (but finite) schema/instance trips the depth guard
  let schema: Record<string, unknown> = { type: 'null' };
  let instance: unknown = null;
  for (let i = 0; i < 200; i++) {
    schema = { type: 'array', items: schema };
    instance = [instance];
  }
  const deep = validate(schema, instance, { budget: 1_000_000, maxDepth: 50 });
  assert.equal(deep.status, 'incomplete');
  assert.equal(deep.reason, 'depth-limit-reached');
});

test('unknown keywords are ignored (only the documented subset is promised)', () => {
  const schema = { type: 'string', format: 'email', minLength: 2, fancyKeyword: true };
  const result = validate(schema, 'ab');
  assert.equal(result.status, 'valid'); // format/fancyKeyword not enforced
});

test('allOf, anyOf, enum and const work within the subset', () => {
  assert.equal(validate({ allOf: [{ type: 'integer' }, { minimum: 5 }] }, 3).status, 'invalid');
  assert.equal(validate({ allOf: [{ type: 'integer' }, { minimum: 5 }] }, 7).status, 'valid');
  assert.equal(validate({ anyOf: [{ type: 'string' }, { type: 'integer' }] }, true).status, 'invalid');
  assert.equal(validate({ anyOf: [{ type: 'string' }, { type: 'integer' }] }, 'x').status, 'valid');
  assert.equal(validate({ enum: ['a', 1, null] }, 2).status, 'invalid');
  assert.equal(validate({ const: { a: [1, 2] } }, { a: [1, 2] }).status, 'valid');
});

test('boolean schemas and unresolvable refs', () => {
  assert.equal(validate(true, 'anything').status, 'valid');
  assert.equal(validate(false, 'anything').status, 'invalid');
  const result = validate({ $ref: '#/$defs/missing', $defs: {} }, 1);
  assert.equal(result.status, 'invalid');
  assert.match(result.errors[0].message, /unresolvable/);
});
