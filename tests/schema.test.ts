import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseJson } from '../src/incremental.ts';
import { validateJson, assertSchema, SchemaError } from '../src/schema.ts';

const S = (s: string) => parseJson(s);

test('exact decimal boundaries (floats would collapse these)', () => {
  const schema = S('{"minimum": 0.3}');
  assert.equal(validateJson(S('0.30000000000000000000000001'), schema).length, 0);
  const errs = validateJson(S('0.29999999999999999999999999'), schema);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].keyword, 'minimum');
  assert.equal(errs[0].instancePath, '');
  assert.equal(errs[0].schemaPath, '/minimum');
});

test('exclusive bounds beyond 53 bits', () => {
  const schema = S('{"exclusiveMaximum": 9007199254740992}');
  assert.equal(validateJson(S('9007199254740992'), schema).length, 1);
  assert.equal(validateJson(S('9007199254740993'), schema).length, 1);
  assert.equal(validateJson(S('9007199254740991'), schema).length, 0);
});

test('multipleOf is exact: 0.3 is a multiple of 0.1', () => {
  const schema = S('{"multipleOf": 0.1}');
  assert.equal(validateJson(S('0.3'), schema).length, 0);
  const errs = validateJson(S('0.25'), schema);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].schemaPath, '/multipleOf');
});

test('integer type uses exact values: 1e2 is an integer, 1e-2 is not', () => {
  const schema = S('{"type": "integer"}');
  assert.equal(validateJson(S('9007199254740993'), schema).length, 0);
  assert.equal(validateJson(S('1e2'), schema).length, 0);
  assert.equal(validateJson(S('1e-2'), schema).length, 1);
  assert.equal(validateJson(S('1.5'), schema).length, 1);
});

test('nested errors report instance path and rule path', () => {
  const schema = S('{"type":"object","properties":{"a":{"type":"array","items":{"minimum":2}}}}');
  const errs = validateJson(S('{"a":[3,1]}'), schema);
  assert.equal(errs.length, 1);
  assert.equal(errs[0].instancePath, '/a/1');
  assert.equal(errs[0].schemaPath, '/properties/a/items/minimum');
  assert.equal(errs[0].keyword, 'minimum');
});

test('required, additionalProperties, type union', () => {
  const schema = S('{"type":"object","required":["id"],"properties":{"id":{"type":["string","integer"]}},"additionalProperties":false}');
  assert.equal(validateJson(S('{"id":"x"}'), schema).length, 0);
  assert.equal(validateJson(S('{"id":7}'), schema).length, 0);

  const missing = validateJson(S('{}'), schema);
  assert.equal(missing.length, 1);
  assert.equal(missing[0].instancePath, '');
  assert.equal(missing[0].schemaPath, '/required');

  const extra = validateJson(S('{"id":"x","nope":1}'), schema);
  assert.equal(extra.length, 1);
  assert.equal(extra[0].instancePath, '/nope');
  assert.equal(extra[0].schemaPath, '/additionalProperties');

  const badType = validateJson(S('{"id":true}'), schema);
  assert.equal(badType.length, 1);
  assert.equal(badType[0].instancePath, '/id');
  assert.equal(badType[0].schemaPath, '/properties/id/type');
});

test('enum and const use numeric value equality', () => {
  assert.equal(validateJson(S('1.0'), S('{"const": 1}')).length, 0);
  assert.equal(validateJson(S('1e+02'), S('{"enum": [100, "x"]}')).length, 0);
  assert.equal(validateJson(S('2'), S('{"enum": [1, 3]}')).length, 1);
  assert.equal(validateJson(S('{"a":[1,2]}'), S('{"const": {"a":[1.0, 2e0]}}')).length, 0);
});

test('string/array/object size keywords', () => {
  assert.equal(validateJson(S('"ab"'), S('{"minLength":3}')).length, 1);
  assert.equal(validateJson(S('"abc"'), S('{"minLength":3,"maxLength":3}')).length, 0);
  assert.equal(validateJson(S('[1,2,3]'), S('{"maxItems":2}')).length, 1);
  assert.equal(validateJson(S('{"a":1}'), S('{"minProperties":2}')).length, 1);
});

test('unsupported keywords are rejected, not silently ignored', () => {
  assert.throws(() => assertSchema(S('{"pattern": "^a"}')), SchemaError);
  assert.throws(() => validateJson(S('1'), S('{"minimum2": 3}')), SchemaError);
  assert.throws(() => assertSchema(S('{"properties": {"a": {"allOf": []}}}')), SchemaError);
  assert.throws(() => assertSchema(S('{"multipleOf": 0}')), SchemaError);
  assert.throws(() => assertSchema(S('{"type": "bigint"}')), SchemaError);
  // annotation keywords are fine
  assert.equal(validateJson(S('1'), S('{"$schema":"x","title":"t","minimum":0}')).length, 0);
});
