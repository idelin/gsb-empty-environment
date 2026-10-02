import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile, type Graph } from '../src/compiler.ts';
import { errorKey, validate } from '../src/validator.ts';
import { InstanceStore } from '../src/store.ts';
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

/** Invariant: after any incremental edit, store state == full revalidation. */
function assertConsistent(store: InstanceStore, graph: Graph): void {
  const full = validate(graph, store.value);
  const storeKeys = store.errors.map(errorKey).sort();
  const fullKeys = full.errors.map(errorKey).sort();
  assert.deepEqual(storeKeys, fullKeys);
  assert.equal(store.verdict, full.verdict);
  for (const e of store.errors) {
    assert.ok(store.errorsAt(e.instancePath).some((x) => errorKey(x) === errorKey(e)));
  }
}

test('initial validation populates errors and the dependency index', () => {
  const g = compile(treeSchema);
  const store = new InstanceStore(g, { label: 'r', children: [{ children: [] }] });
  assert.equal(store.verdict, 'invalid');
  assert.equal(store.errors.length, 1);
  const at = store.errorsAt('/children/0');
  assert.equal(at.length, 1);
  assert.match(at[0].message, /missing required property "label"/);
  assert.equal(at[0].schemaPath, '#/$defs/node');
});

test('fixing a nested value removes exactly the stale errors', () => {
  const g = compile(treeSchema);
  const store = new InstanceStore(g, {
    label: 'r',
    children: [{ children: [] }, { label: 9 }],
  });
  assert.equal(store.errors.length, 2);

  const res = store.set('/children/0/label', 'fixed');
  assert.equal(res.errors.length, 1);
  assert.equal(store.errorsAt('/children/0').length, 0);
  assert.equal(store.errorsAt('/children/1/label').length, 1);
  assertConsistent(store, g);

  store.set('/children/1/label', 'also fixed');
  assert.equal(store.verdict, 'valid');
  assert.equal(store.errors.length, 0);
  assertConsistent(store, g);
});

test('introducing an error adds it without touching unrelated errors', () => {
  const g = compile(treeSchema);
  const store = new InstanceStore(g, {
    label: 'r',
    children: [{ label: 'ok' }, { label: 'also ok' }],
  });
  assert.equal(store.verdict, 'valid');

  store.set('/children/1/label', 123);
  assert.equal(store.verdict, 'invalid');
  assert.equal(store.errors.length, 1);
  assert.equal(store.errors[0].instancePath, '/children/1/label');
  assertConsistent(store, g);
});

test('array element removal revalidates shifted sibling paths', () => {
  const g = compile({ type: 'array', items: { type: 'string' } });
  const store = new InstanceStore(g, ['a', 1, 'b', 2]);
  assert.deepEqual(store.errors.map((e) => e.instancePath).sort(), ['/1', '/3']);

  store.remove('/0'); // [1, 'b', 2]
  assert.deepEqual(store.errors.map((e) => e.instancePath).sort(), ['/0', '/2']);
  assertConsistent(store, g);
});

test('edits under a oneOf ancestor re-evaluate the branch choice', () => {
  const schema = {
    oneOf: [
      { type: 'object', required: ['a'], properties: { a: { type: 'string' } } },
      { type: 'object', required: ['b'], properties: { b: { type: 'number' } } },
    ],
  } as unknown as Schema;
  const g = compile(schema);
  const store = new InstanceStore(g, { a: 'x' });
  assert.equal(store.verdict, 'valid');

  const removed = store.remove('/a'); // {} matches no branch
  assert.equal(removed.verdict, 'invalid');
  assert.match(removed.errors[0].message, /no oneOf branch matched/);
  assertConsistent(store, g);

  const added = store.set('/b', 1); // now the second branch matches
  assert.equal(added.verdict, 'valid');
  assert.equal(store.errors.length, 0);
  assertConsistent(store, g);
});

test('adding a previously missing required property clears the container error', () => {
  const g = compile(treeSchema);
  const store = new InstanceStore(g, { label: 'r', children: [{}] });
  assert.equal(store.errorsAt('/children/0').length, 1);

  store.set('/children/0/label', 'now present');
  assert.equal(store.verdict, 'valid');
  assertConsistent(store, g);
});

test('root replacement revalidates everything', () => {
  const g = compile(treeSchema);
  const store = new InstanceStore(g, { label: 'r' });
  assert.equal(store.verdict, 'valid');
  store.set('', { children: [] });
  assert.equal(store.verdict, 'invalid');
  assert.equal(store.errorsAt('').length, 1);
  assertConsistent(store, g);
});
