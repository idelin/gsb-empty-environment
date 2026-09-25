import { test } from 'node:test';
import assert from 'node:assert/strict';
import { queryOnce } from '../src/store.ts';
import { PathError } from '../src/errors.ts';

test('prototype-chain properties are always invisible', () => {
  const doc = { a: { b: 1 } };
  for (const path of ['a.hasOwnProperty', 'a.toString', 'a.valueOf', 'a.length']) {
    assert.deepEqual(queryOnce(doc, path), [], path);
  }
  // Wildcard enumeration must not leak inherited members either.
  const matches = queryOnce(doc, 'a.*');
  assert.deepEqual(matches.map((m) => m.path), [['a', 'b']]);
});

test('own __proto__ key (via JSON.parse) is denied and skipped', () => {
  const doc = JSON.parse('{"safe": 1, "__proto__": {"polluted": true}}');
  assert.throws(
    () => queryOnce(doc, `['__proto__']`),
    (e: unknown) => e instanceof PathError && e.code === 'FORBIDDEN_PROPERTY',
  );
  const matches = queryOnce(doc, '*');
  assert.deepEqual(matches.map((m) => m.path), [['safe']]);
  assert.equal(({} as Record<string, unknown>).polluted, undefined);
});

test('getters are never invoked; accessor properties are invisible', () => {
  let called = 0;
  const doc: Record<string, unknown> = { real: 5 };
  Object.defineProperty(doc, 'trap', {
    enumerable: true,
    get() {
      called++;
      throw new Error('getter must not run');
    },
  });
  assert.deepEqual(queryOnce(doc, 'trap'), []);
  assert.deepEqual(queryOnce(doc, '*').map((m) => m.path), [['real']]);
  assert.equal(called, 0);
});

test('host objects and class instances are rejected, never called', () => {
  class Widget {
    run() {
      throw new Error('must not be called');
    }
  }
  assert.throws(
    () => queryOnce({ w: new Widget() }, 'w.x'),
    (e: unknown) => e instanceof PathError && e.code === 'HOST_OBJECT',
  );
  assert.throws(
    () => queryOnce({ f: () => 1 }, 'f'),
    (e: unknown) => e instanceof PathError && e.code === 'HOST_OBJECT',
  );
  assert.throws(
    () => queryOnce({ d: new Date(0) }, 'd'),
    (e: unknown) => e instanceof PathError && e.code === 'HOST_OBJECT',
  );
  assert.throws(
    () => queryOnce(new Map() as unknown, 'a'),
    (e: unknown) => e instanceof PathError && e.code === 'HOST_OBJECT',
  );
});

test('functions reachable via descend are rejected without invocation', () => {
  const doc = { a: { b: () => 0 } };
  assert.throws(
    () => queryOnce(doc, '..b'),
    (e: unknown) => e instanceof PathError && e.code === 'HOST_OBJECT',
  );
});

test('null-prototype data objects work and stay safe', () => {
  const inner: Record<string, unknown> = Object.create(null);
  inner.x = 1;
  const doc: Record<string, unknown> = Object.create(null);
  doc.inner = inner;
  assert.deepEqual(queryOnce(doc, 'inner.x').map((m) => m.value), [1]);
  // Explicit constructor access is denied at parse time even on null-proto objects.
  assert.throws(() => queryOnce(doc, 'inner.constructor'), PathError);
});

test('errors reference the user path only', () => {
  try {
    queryOnce({ w: new Date(0) }, 'w.x.y');
    assert.unreachable();
  } catch (e) {
    assert.ok(e instanceof PathError);
    assert.equal(e.userPath, 'w.x.y');
    assert.doesNotMatch(e.message, /Date|object at|0x/);
  }
});
