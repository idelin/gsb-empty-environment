import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/compiler.ts';
import { errorKey, validate } from '../src/validator.ts';
import type { Schema } from '../src/schema.ts';

const treeSchema = {
  $defs: {
    node: {
      type: 'object',
      required: ['label'],
      properties: {
        label: { type: 'string' },
        children: { type: 'array', items: { $ref: '#/$defs/node' } },
      },
    },
  },
  $ref: '#/$defs/node',
} as unknown as Schema;

test('valid recursive tree validates and terminates', () => {
  const g = compile(treeSchema);
  const value = {
    label: 'root',
    children: [
      { label: 'a', children: [] },
      { label: 'b', children: [{ label: 'b1' }] },
    ],
  };
  const r = validate(g, value);
  assert.equal(r.verdict, 'valid');
  assert.equal(r.errors.length, 0);
});

test('errors carry both instance path and schema (rule) path', () => {
  const g = compile(treeSchema);
  const value = { label: 'root', children: [{ children: [] }] };
  const r = validate(g, value);
  assert.equal(r.verdict, 'invalid');
  assert.equal(r.errors.length, 1);
  assert.equal(r.errors[0].instancePath, '/children/0');
  assert.equal(r.errors[0].schemaPath, '#/$defs/node');
  assert.match(r.errors[0].message, /missing required property "label"/);
});

test('oneOf requires exactly one matching branch', () => {
  const g = compile({ oneOf: [{ type: 'string' }, { type: 'number' }] });
  assert.equal(validate(g, 'x').verdict, 'valid');
  const none = validate(g, true);
  assert.equal(none.verdict, 'invalid');
  assert.match(none.errors[0].message, /no oneOf branch matched/);
  const both = validate(g, 'x', {});
  assert.equal(both.verdict, 'valid');
  const g2 = compile({ oneOf: [{ type: 'number' }, { type: 'number' }] });
  const two = validate(g2, 3);
  assert.equal(two.verdict, 'invalid');
  assert.match(two.errors[0].message, /matched 2 oneOf branches/);
  assert.equal(both.errors.length, 0);
});

test('oneOf branches sharing graph nodes do not share validation state', () => {
  // Both branches reach the SAME compiled node (#/$defs/str) at the SAME
  // instance locations. Branch 1 fails on /1; branch 2 must still evaluate
  // every node fresh and match. Leaked branch state would flip the verdict.
  const schema = {
    $defs: {
      str: { type: 'string' },
      s: { oneOf: [{ $ref: '#/$defs/str' }, { type: 'number' }] },
      t: { oneOf: [{ $ref: '#/$defs/str' }, { type: 'boolean' }] },
    },
    oneOf: [
      { type: 'array', items: { $ref: '#/$defs/s' } },
      { type: 'array', items: { $ref: '#/$defs/t' } },
    ],
  } as unknown as Schema;
  const g = compile(schema);

  const r = validate(g, ['x', true]);
  assert.equal(r.verdict, 'valid');
  assert.equal(r.errors.length, 0);

  const onlyFirst = validate(g, ['x', 1]);
  assert.equal(onlyFirst.verdict, 'valid');

  // All-string arrays match BOTH branches: exactly-one semantics -> invalid.
  const two = validate(g, ['x', 'y']);
  assert.equal(two.verdict, 'invalid');
  assert.match(two.errors[0].message, /matched 2 oneOf branches/);
});

test('instance depth limit yields incomplete, never invalid', () => {
  const schema = {
    $defs: {
      v: { oneOf: [{ type: 'string' }, { type: 'array', items: { $ref: '#/$defs/v' } }] },
    },
    $ref: '#/$defs/v',
  } as unknown as Schema;
  const g = compile(schema);
  let value: unknown = 'leaf';
  for (let i = 0; i < 50; i++) value = [value];
  const r = validate(g, value, { maxDepth: 10 });
  assert.equal(r.verdict, 'incomplete');
  assert.match(r.incompleteReason ?? '', /depth limit/);
  // Same instance within budget is plainly valid.
  assert.equal(validate(g, value, { maxDepth: 100 }).verdict, 'valid');
});

test('error budget exhaustion yields incomplete (truncated), never invalid', () => {
  const g = compile({ type: 'array', items: { type: 'string' } });
  const value = Array.from({ length: 100 }, (_, i) => i);
  const r = validate(g, value, { maxErrors: 5 });
  assert.equal(r.verdict, 'incomplete');
  assert.equal(r.truncated, true);
  assert.ok(r.errors.length <= 5);
});

test('deep finite recursion terminates within default budgets', () => {
  const schema = {
    $defs: {
      v: { oneOf: [{ type: 'number' }, { type: 'array', items: { $ref: '#/$defs/v' } }] },
    },
    $ref: '#/$defs/v',
  } as unknown as Schema;
  const g = compile(schema);
  let value: unknown = 0;
  for (let i = 0; i < 90; i++) value = [value];
  const r = validate(g, value);
  assert.equal(r.verdict, 'valid');
});

test('dedup keeps same instance path with different schema paths distinct', () => {
  // Two findings at the same instance location but from different rules
  // must never be merged.
  const a = { instancePath: '/x', schemaPath: '#/properties/x', message: 'm' };
  const b = { instancePath: '/x', schemaPath: '#/$defs/other', message: 'm' };
  assert.notEqual(errorKey(a), errorKey(b));
  const c = { instancePath: '/x', schemaPath: '#/properties/x', message: 'm' };
  assert.equal(errorKey(a), errorKey(c));

  // Two missing-required errors share instance path AND schema path but
  // differ in message: both are kept.
  const g = compile({ type: 'object', required: ['a', 'b'] });
  const r = validate(g, {});
  assert.equal(r.errors.length, 2);
});
