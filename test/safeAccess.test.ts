import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile } from '../src/index.ts';

test('getters are never invoked and accessor properties are invisible', () => {
  let getterCalls = 0;
  const doc: Record<string, unknown> = { plain: 1 };
  Object.defineProperty(doc, 'trap', {
    enumerable: true,
    get() { getterCalls += 1; return 999; },
  });
  assert.deepEqual(compile('$.trap').run(doc), []);
  assert.deepEqual(compile('$[*]').run(doc).map((m) => m.path[0]), ['plain']);
  assert.deepEqual(compile('$..trap').run(doc), []);
  assert.equal(getterCalls, 0);
});

test('prototype-chain properties are always invisible', () => {
  const proto = { inherited: 'nope', polluted: 'nope' };
  const doc = Object.create(proto);
  doc.own = 1;
  assert.deepEqual(compile('$.inherited').run(doc), []);
  assert.deepEqual(compile('$.polluted').run(doc), []);
  assert.deepEqual(compile('$[*]').run(doc).map((m) => m.path[0]), ['own']);
  assert.deepEqual(compile('$..polluted').run(doc), []);
});

test('proxy get traps never fire during evaluation', () => {
  let getTraps = 0;
  const target = { a: 1, nested: { b: 2 } };
  const doc = new Proxy(target, {
    get(t, k, r) { getTraps += 1; return Reflect.get(t, k, r); },
  });
  const matches = compile('$.nested.b').run(doc);
  assert.equal(matches.length, 1);
  assert.equal(matches[0].value, 2);
  assert.equal(getTraps, 0, 'no [[Get]] may be triggered');
});

test('functions (host objects) are never called nor traversed', () => {
  let calls = 0;
  const fn = () => { calls += 1; return 1; };
  const doc = { cb: fn, nested: { also: fn } };
  const m = compile('$.cb').run(doc);
  assert.equal(m.length, 1);
  assert.equal(m[0].value, fn, 'a function value may be matched but is never invoked');
  assert.deepEqual(compile('$.cb.anything').run(doc), []);
  assert.deepEqual(compile('$..anything').run(doc), []);
  assert.equal(calls, 0);
});

test('denied keys present as own data are invisible to wildcards and descent', () => {
  const doc = JSON.parse('{"__proto__": {"x": 1}, "constructor": 5, "ok": 2}');
  assert.ok(Object.prototype.hasOwnProperty.call(doc, '__proto__'));
  const keys = compile('$[*]').run(doc).map((m) => m.path[0]);
  assert.deepEqual(keys, ['ok']);
  assert.deepEqual(compile('$..x').run(doc), [], 'descent must not enter __proto__');
});

test('array holes and explicit undefined are observably different', () => {
  // eslint-disable-next-line no-sparse-arrays
  const arr = [1, , undefined]; // index 1 is a hole, index 2 is explicit undefined
  const wild = compile('$[*]').run(arr);
  assert.deepEqual(wild.map((m) => m.path[0]), [0, 2], 'holes are skipped');
  assert.deepEqual(compile('$[1]').run(arr), [], 'hole reads as absent');
  const explicit = compile('$[2]').run(arr);
  assert.equal(explicit.length, 1);
  assert.equal(explicit[0].value, undefined);
  assert.deepEqual(compile('$[?(@ == undefined)]').run(arr).map((m) => m.path[0]), [2]);
  assert.deepEqual(compile('$[?(exists(@))]').run(arr).map((m) => m.path[0]), [0, 2]);
});

test('missing keys fail every comparison, explicit undefined matches undefined', () => {
  const doc = { items: [{ a: undefined }, { b: 1 }] };
  assert.deepEqual(compile('$.items[?(@.a == undefined)]').run(doc).map((m) => m.path[1]), [0]);
  assert.deepEqual(compile('$.items[?(@.a != undefined)]').run(doc), [], 'absent fails even !=');
  assert.deepEqual(compile('$.items[?(exists(@.a))]').run(doc).map((m) => m.path[1]), [0]);
});
