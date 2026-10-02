import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile, CompileError, type Graph } from '../src/compiler.ts';
import type { Schema } from '../src/schema.ts';

const recursiveTree = {
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

test('recursive schema compiles to a shared graph node', () => {
  const g: Graph = compile(recursiveTree);
  const root = g.nodes[g.root];
  assert.equal(root.kind, 'object');
  if (root.kind !== 'object') return;
  const childrenId = root.props.get('children');
  assert.notEqual(childrenId, undefined);
  const children = g.nodes[childrenId!];
  assert.equal(children.kind, 'array');
  if (children.kind !== 'array') return;
  // The recursion is a back-edge to the very same node: the graph is shared,
  // not unrolled.
  assert.equal(children.items, g.root);
  assert.equal(g.nodes.length, 3); // object + string scalar + array
});

test('dangling references are rejected at compile time', () => {
  assert.throws(
    () => compile({ $ref: '#/$defs/missing' } as unknown as Schema),
    (err: unknown) => err instanceof CompileError && /dangling/.test((err as Error).message),
  );
});

test('pure reference cycles (no instance consumed) are rejected', () => {
  const cyclic = {
    $defs: {
      a: { $ref: '#/$defs/b' },
      b: { $ref: '#/$defs/a' },
    },
    $ref: '#/$defs/a',
  } as unknown as Schema;
  assert.throws(
    () => compile(cyclic),
    (err: unknown) => err instanceof CompileError && /non-productive/.test((err as Error).message),
  );
});

test('self-referencing root ref is rejected as non-productive', () => {
  assert.throws(() => compile({ $ref: '#' } as unknown as Schema), /non-productive/);
});

test('cycles through oneOf options only are rejected as non-consuming', () => {
  const cyclic = {
    $defs: {
      u: { oneOf: [{ $ref: '#/$defs/u' }, { type: 'string' }] },
    },
    $ref: '#/$defs/u',
  } as unknown as Schema;
  assert.throws(
    () => compile(cyclic),
    (err: unknown) => err instanceof CompileError && /non-consuming/.test((err as Error).message),
  );
});

test('productive recursion through oneOf + array is allowed', () => {
  const json = {
    $defs: {
      value: {
        oneOf: [{ type: 'string' }, { type: 'array', items: { $ref: '#/$defs/value' } }],
      },
    },
    $ref: '#/$defs/value',
  } as unknown as Schema;
  const g = compile(json);
  assert.ok(g.nodes.length > 0);
});

test('constructs outside the promised subset are rejected', () => {
  assert.throws(() => compile({ type: 'integer' } as unknown as Schema), /unsupported/);
  assert.throws(() => compile({ allOf: [] } as unknown as Schema), /unsupported/);
  assert.throws(() => compile({ type: 'array' } as unknown as Schema), /items/);
});
