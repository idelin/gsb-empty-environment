import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, assertSchema, SchemaError, type Schema } from '../src/index.ts';

test('exactlyOne: zero or two present fields both fail', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b'] }] };
  assert.equal(validate({ a: 1 }, schema).valid, true);
  const none = validate({}, schema);
  assert.equal(none.valid, false);
  assert.equal(none.errors[0].keyword, 'exactlyOne');
  const both = validate({ a: 1, b: 2 }, schema);
  assert.equal(both.valid, false);
});

test('false / null values still count as present', () => {
  const schema: Schema = { groups: [{ kind: 'exactlyOne', fields: ['a', 'b'] }] };
  assert.equal(validate({ a: false }, schema).valid, true);
  assert.equal(validate({ a: null }, schema).valid, true);
  assert.equal(validate({ a: false, b: null }, schema).valid, false);
});

test('atLeastOne and atMost k', () => {
  const atLeast: Schema = { groups: [{ kind: 'atLeastOne', fields: ['a', 'b', 'c'] }] };
  assert.equal(validate({}, atLeast).valid, false);
  assert.equal(validate({ c: 0 }, atLeast).valid, true);
  const atMost: Schema = { groups: [{ kind: 'atMost', k: 2, fields: ['a', 'b', 'c'] }] };
  assert.equal(validate({ a: 1, b: 2 }, atMost).valid, true);
  assert.equal(validate({ a: 1, b: 2, c: 3 }, atMost).valid, false);
  const zero: Schema = { groups: [{ kind: 'atMost', k: 0, fields: ['x'] }] };
  assert.equal(validate({ x: 1 }, zero).valid, false);
  assert.equal(validate({}, zero).valid, true);
});

test('conditional enablement gates the group', () => {
  const schema: Schema = {
    groups: [
      {
        kind: 'exactlyOne',
        fields: ['card', 'paypal'],
        when: { op: 'equals', field: 'mode', value: 'payment' },
      },
    ],
  };
  assert.equal(validate({ mode: 'payment' }, schema).valid, false);
  assert.equal(validate({ mode: 'payment', card: {} }, schema).valid, true);
  assert.equal(validate({ mode: 'other' }, schema).valid, true);
});

test('undecided condition follows the contract policy', () => {
  const group = {
    kind: 'exactlyOne' as const,
    fields: ['x', 'y'],
    when: { op: 'equals' as const, field: 'mode', value: 'on' },
  };
  const disabled: Schema = { groups: [group], onUndecidedCondition: 'disabled' };
  assert.equal(validate({}, disabled).valid, true);
  const enabled: Schema = { groups: [group], onUndecidedCondition: 'enabled' };
  assert.equal(validate({}, enabled).valid, false);
  const strict: Schema = { groups: [group], onUndecidedCondition: 'error' };
  const r = validate({}, strict);
  assert.equal(r.valid, false);
  assert.equal(r.errors[0].keyword, 'condition');
  assert.equal(r.errors[0].instancePath, '/mode');
  assert.equal(r.errors[0].rulePath, '/groups/0/when');
  assert.deepEqual(r.errors[0].details?.missingFields, ['mode']);
});

test('errors carry both instance path and rule path', () => {
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['a', 'b'] },
      { kind: 'atMost', k: 1, fields: ['c', 'd'] },
    ],
  };
  const r = validate({ c: 1, d: 2 }, schema);
  assert.equal(r.errors.length, 2);
  assert.equal(r.errors[0].instancePath, '');
  assert.equal(r.errors[0].rulePath, '/groups/0');
  assert.equal(r.errors[1].rulePath, '/groups/1');
});

test('overlapping groups are all checked against the same instance', () => {
  const schema: Schema = {
    groups: [
      { kind: 'exactlyOne', fields: ['a', 'b'] },
      { kind: 'exactlyOne', fields: ['b', 'c'] },
    ],
  };
  assert.equal(validate({ a: 1, c: 1 }, schema).valid, true);
  const r = validate({ a: 1, b: 1, c: 1 }, schema);
  assert.equal(r.errors.length, 2);
  assert.deepEqual(r.errors.map((e) => e.rulePath), ['/groups/0', '/groups/1']);
});

test('assertSchema rejects shapes outside the promised subset', () => {
  assert.throws(() => assertSchema({ groups: [{ kind: 'nope' as never, fields: ['a'] }] }), SchemaError);
  assert.throws(() => assertSchema({ groups: [{ kind: 'atMost', fields: ['a'] }] }), SchemaError);
  assert.throws(() => assertSchema({ groups: [{ kind: 'atMost', k: -1, fields: ['a'] }] }), SchemaError);
  assert.throws(() => assertSchema({ groups: [{ kind: 'exactlyOne', fields: [] }] }), SchemaError);
  assert.throws(() => assertSchema({ onUndecidedCondition: 'maybe' as never }), SchemaError);
  assert.throws(
    () => assertSchema({ groups: [{ kind: 'exactlyOne', fields: ['a'], when: { op: 'regex' } as never }] }),
    SchemaError,
  );
});
