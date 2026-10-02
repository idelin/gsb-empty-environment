import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/compiler.ts';
import { validate } from '../src/validator.ts';
import { interpret } from '../src/interpreter.ts';
import { crossCheck } from '../src/index.ts';
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

const jsonSchema = {
  $defs: {
    v: {
      oneOf: [
        { type: 'string' },
        { type: 'number' },
        { type: 'boolean' },
        { type: 'null' },
        { type: 'array', items: { $ref: '#/$defs/v' } },
      ],
    },
  },
  $ref: '#/$defs/v',
} as unknown as Schema;

test('independent interpreter agrees with the graph validator on small bounded instances', () => {
  const cases: Array<{ schema: Schema; value: unknown }> = [
    { schema: treeSchema, value: { label: 'ok' } },
    { schema: treeSchema, value: { label: 'ok', children: [{ label: 'kid', children: [] }] } },
    { schema: treeSchema, value: { children: [] } },
    { schema: treeSchema, value: { label: 42 } },
    { schema: treeSchema, value: { label: 'x', children: [{ children: [] }] } },
    { schema: jsonSchema, value: ['a', 1, true, null, ['nested', [2]]] },
    { schema: jsonSchema, value: ['a', { not: 'in the subset' }] },
    { schema: { oneOf: [{ type: 'string' }, { type: 'number' }] }, value: 's' },
    { schema: { oneOf: [{ type: 'string' }, { type: 'number' }] }, value: false },
    { schema: { type: 'array', items: { type: 'number' } }, value: [1, 2, 3] },
    { schema: { type: 'array', items: { type: 'number' } }, value: [1, 'two', 3] },
  ];
  for (const { schema, value } of cases) {
    const g = compile(schema);
    const graphResult = validate(g, value);
    const interpResult = interpret(schema, value);
    assert.notEqual(interpResult.verdict, 'incomplete');
    assert.equal(
      interpResult.verdict,
      graphResult.verdict,
      `verdict mismatch for ${JSON.stringify(value)}: graph=${graphResult.verdict} interpreter=${interpResult.verdict}`,
    );
  }
});

test('crossCheck reports consistency and skips comparison when budget is exhausted', () => {
  const g = compile(treeSchema);
  const ok = crossCheck(g, treeSchema, { label: 'fine' });
  assert.equal(ok.consistent, true);
  assert.equal(ok.interpreter.verdict, 'valid');

  const bad = crossCheck(g, treeSchema, { label: 7 });
  assert.equal(bad.consistent, true);
  assert.equal(bad.interpreter.verdict, 'invalid');

  const starved = crossCheck(g, treeSchema, { label: 'fine' }, {}, { maxSteps: 1 });
  assert.equal(starved.interpreter.verdict, 'incomplete');
  assert.equal(starved.consistent, true); // incomplete is not a disagreement
});

test('budget exhaustion returns incomplete, never a schema failure', () => {
  const deep = { label: 'r', children: [{ label: 'a', children: [{ label: 'b' }] }] };
  const r = interpret(treeSchema, deep, { maxSteps: 3 });
  assert.equal(r.verdict, 'incomplete');
  assert.notEqual(r.verdict, 'invalid');
});

test('interpreter terminates on self-recursive schemas with finite trees', () => {
  let value: Record<string, unknown> = { label: 'leaf' };
  for (let i = 0; i < 40; i++) value = { label: `n${i}`, children: [value] };
  const r = interpret(treeSchema, value);
  assert.equal(r.verdict, 'valid');
  assert.ok(r.steps > 40);
});
