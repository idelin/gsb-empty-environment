import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Decimal } from '../src/decimal.ts';
import { parseJson } from '../src/parser.ts';
import { validate } from '../src/schema.ts';

const USER_SCHEMA = {
  type: 'object',
  required: ['id', 'name'],
  properties: {
    id: { type: 'integer', minimum: '9007199254740993' },
    name: { type: 'string', minLength: 1, maxLength: 10 },
    score: { type: 'number', exclusiveMaximum: '1e2' },
    tags: { type: 'array', items: { type: 'string' }, minItems: 1 },
    role: { enum: ['admin', 'user', 7] },
  },
  additionalProperties: false,
};

test('valid document passes', () => {
  const value = parseJson('{"id":9007199254740993,"name":"ada","score":99.5,"tags":["x"],"role":"admin"}');
  const result = validate(value, USER_SCHEMA);
  assert.equal(result.valid, true);
  assert.deepEqual(result.errors, []);
});

test('errors carry instance path and rule path', () => {
  const value = parseJson('{"id":9007199254740992,"name":"","score":100,"tags":[],"extra":1}');
  const result = validate(value, USER_SCHEMA);
  assert.equal(result.valid, false);
  const byKeyword = new Map(result.errors.map((e) => [e.keyword, e]));

  const minimum = byKeyword.get('minimum');
  assert.equal(minimum?.instancePath, '/id');
  assert.equal(minimum?.schemaPath, '/properties/id/minimum');

  const minLength = byKeyword.get('minLength');
  assert.equal(minLength?.instancePath, '/name');
  assert.equal(minLength?.schemaPath, '/properties/name/minLength');

  const exclusiveMaximum = byKeyword.get('exclusiveMaximum');
  assert.equal(exclusiveMaximum?.instancePath, '/score');
  assert.equal(exclusiveMaximum?.schemaPath, '/properties/score/exclusiveMaximum');

  const minItems = byKeyword.get('minItems');
  assert.equal(minItems?.instancePath, '/tags');
  assert.equal(minItems?.schemaPath, '/properties/tags/minItems');

  const additional = byKeyword.get('additionalProperties');
  assert.equal(additional?.instancePath, '/extra');
  assert.equal(additional?.schemaPath, '/additionalProperties');
});

test('integer bounds are exact beyond 2**53', () => {
  // Bounds beyond 2**53 must be given as strings/Decimals: a JS number
  // literal would already be rounded before it reaches the validator.
  const schema = { type: 'integer', minimum: '9007199254740993' };
  assert.equal(validate(parseJson('9007199254740992'), schema).valid, false);
  assert.equal(validate(parseJson('9007199254740993'), schema).valid, true);
  assert.equal(validate(parseJson('9007199254740993.5'), schema).valid, false,
    'fractional value fails the integer type');
  assert.equal(validate(parseJson('100e-1'), { type: 'integer' }).valid, true,
    '100e-1 is exactly 10');
});

test('exclusiveMaximum with exact decimal comparison: 1e2 equals 100', () => {
  const schema = { exclusiveMaximum: '1e2' };
  assert.equal(validate(parseJson('100'), schema).valid, false);
  assert.equal(validate(parseJson('99.999'), schema).valid, true);
  assert.equal(validate(parseJson('1e2'), schema).valid, false);
});

test('enum and const compare numbers numerically', () => {
  assert.equal(validate(parseJson('1e2'), { const: 100 }).valid, true);
  assert.equal(validate(parseJson('100.0'), { enum: [100] }).valid, true);
  assert.equal(validate(parseJson('7'), { enum: ['admin', 'user', 7] }).valid, true);
  assert.equal(validate(parseJson('"admin"'), { enum: ['admin', 'user', 7] }).valid, true);
  assert.equal(validate(parseJson('8'), { enum: ['admin', 'user', 7] }).valid, false);
});

test('nested errors report deep paths', () => {
  const schema = {
    type: 'object',
    properties: {
      items: { type: 'array', items: { type: 'object', properties: { n: { type: 'integer' } } } },
    },
  };
  const value = parseJson('{"items":[{"n":1},{"n":1.5}]}');
  const result = validate(value, schema);
  assert.equal(result.valid, false);
  assert.equal(result.errors.length, 1);
  assert.equal(result.errors[0].instancePath, '/items/1/n');
  assert.equal(result.errors[0].schemaPath, '/properties/items/items/properties/n/type');
});

test('type unions and pattern', () => {
  assert.equal(validate(parseJson('"x"'), { type: ['string', 'null'] }).valid, true);
  assert.equal(validate(parseJson('null'), { type: ['string', 'null'] }).valid, true);
  assert.equal(validate(parseJson('5'), { type: ['string', 'null'] }).valid, false);
  assert.equal(validate(parseJson('"abc-123"'), { pattern: '^[a-z]+-[0-9]+$' }).valid, true);
  assert.equal(validate(parseJson('"ABC"'), { pattern: '^[a-z]+$' }).valid, false);
});

test('unsupported keywords are warned about, not silently trusted', () => {
  const result = validate(parseJson('5'), { type: 'number', multipleOf: 2, minimum: 10 });
  assert.equal(result.valid, false, 'minimum still enforced');
  assert.equal(result.warnings.length, 1);
  assert.match(result.warnings[0], /unsupported schema keyword "multipleOf"/);
});

test('required reports the missing property at the object path', () => {
  const result = validate(parseJson('{"name":"ada"}'), USER_SCHEMA);
  const required = result.errors.find((e) => e.keyword === 'required');
  assert.equal(required?.instancePath, '');
  assert.equal(required?.schemaPath, '/required');
  assert.match(required?.message ?? '', /"id"/);
});

test('schema values may be Decimals directly', () => {
  const schema = { minimum: Decimal.fromLexeme('0.1') };
  assert.equal(validate(parseJson('0.09'), schema).valid, false);
  assert.equal(validate(parseJson('0.1'), schema).valid, true);
});
