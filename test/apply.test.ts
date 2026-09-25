import { test } from 'node:test';
import assert from 'node:assert/strict';
import { apply, PatchError, type Op } from '../src/patch.ts';
import { deepEqual } from '../src/json.ts';
import { frozen, frozenOps } from './helpers.ts';

test('add/remove/replace basic semantics', () => {
  const r1 = apply({ a: 1 }, [{ op: 'add', path: '/b', value: 2 }]);
  assert.deepEqual(r1.doc, { a: 1, b: 2 });
  const r2 = apply({ a: [1, 2, 3] }, [{ op: 'add', path: '/a/1', value: 9 }]);
  assert.deepEqual(r2.doc, { a: [1, 9, 2, 3] });
  const r3 = apply({ a: [1, 2, 3] }, [{ op: 'add', path: '/a/-', value: 4 }]);
  assert.deepEqual(r3.doc, { a: [1, 2, 3, 4] });
  const r4 = apply({ a: 1, b: 2 }, [{ op: 'remove', path: '/a' }]);
  assert.deepEqual(r4.doc, { b: 2 });
  const r5 = apply({ a: 1 }, [{ op: 'replace', path: '/a', value: null }]);
  assert.deepEqual(r5.doc, { a: null });
});

test('strictly distinguishes missing vs null/0/false', () => {
  assert.throws(() => apply({}, [{ op: 'replace', path: '/x', value: 1 }]), PatchError);
  assert.throws(() => apply({ x: null }, [{ op: 'replace', path: '/x/y', value: 1 }]), PatchError);
  // replace works on existing falsy values
  assert.deepEqual(apply({ x: null }, [{ op: 'replace', path: '/x', value: 1 }]).doc, { x: 1 });
  assert.deepEqual(apply({ x: 0 }, [{ op: 'replace', path: '/x', value: 1 }]).doc, { x: 1 });
  assert.deepEqual(apply({ x: false }, [{ op: 'replace', path: '/x', value: 1 }]).doc, { x: 1 });
  // test on falsy values
  apply({ x: null }, [{ op: 'test', path: '/x', value: null }]);
  apply({ x: 0 }, [{ op: 'test', path: '/x', value: 0 }]);
  apply({ x: false }, [{ op: 'test', path: '/x', value: false }]);
  assert.throws(() => apply({ x: 0 }, [{ op: 'test', path: '/x', value: false }]), PatchError);
  assert.throws(() => apply({}, [{ op: 'test', path: '/x', value: null }]), PatchError);
  // remove of existing falsy element
  assert.deepEqual(apply([0, false, null], [{ op: 'remove', path: '/0' }]).doc, [false, null]);
});

test('array index rules: no leading zeros, "-" only for add', () => {
  assert.throws(() => apply([1, 2], [{ op: 'remove', path: '/01' }]), PatchError);
  assert.throws(() => apply([1, 2], [{ op: 'replace', path: '/1x', value: 0 }]), PatchError);
  assert.throws(() => apply([1, 2], [{ op: 'remove', path: '/-' }]), PatchError);
  assert.throws(() => apply([1, 2], [{ op: 'test', path: '/-', value: 2 }]), PatchError);
  assert.throws(() => apply([1, 2], [{ op: 'add', path: '/5', value: 0 }]), PatchError);
  assert.deepEqual(apply([1, 2], [{ op: 'add', path: '/2', value: 3 }]).doc, [1, 2, 3]);
});

test('JSON Pointer escaping ~0 and ~1', () => {
  const doc = { 'a/b': { '~x': 1 }, '': { '~01': 2 } };
  assert.deepEqual(apply(doc, [{ op: 'replace', path: '/a~1b/~0x', value: 9 }]).doc, {
    'a/b': { '~x': 9 },
    '': { '~01': 2 },
  });
  assert.deepEqual(apply(doc, [{ op: 'test', path: '//~001', value: 2 }]).doc, doc);
  assert.throws(() => apply(doc, [{ op: 'test', path: '/a~2b', value: 1 }]), PatchError);
});

test('move: destination interpreted after removal; no move into descendant', () => {
  // same array, from < to
  assert.deepEqual(apply([1, 2, 3, 4], [{ op: 'move', from: '/0', path: '/2' }]).doc, [2, 3, 1, 4]);
  // same array, from > to
  assert.deepEqual(apply([1, 2, 3, 4], [{ op: 'move', from: '/3', path: '/1' }]).doc, [1, 4, 2, 3]);
  // cross container
  assert.deepEqual(
    apply({ a: { x: 1 }, b: [1, 2] }, [{ op: 'move', from: '/a/x', path: '/b/1' }]).doc,
    { a: {}, b: [1, 1, 2] },
  );
  // object key rename
  assert.deepEqual(apply({ a: 1 }, [{ op: 'move', from: '/a', path: '/b' }]).doc, { b: 1 });
  // into own descendant is rejected
  assert.throws(() => apply({ a: { b: { c: 1 } } }, [{ op: 'move', from: '/a', path: '/a/b/c/d' }]), PatchError);
  assert.throws(() => apply({ a: { b: 1 } }, [{ op: 'move', from: '/a', path: '/a/b' }]), PatchError);
});

test('atomicity: a failing step leaves the document and patch untouched', () => {
  const doc = frozen({ a: [1, 2], b: { x: 1 } });
  const ops = frozenOps([
    { op: 'replace', path: '/b/x', value: 42 },
    { op: 'add', path: '/a/0', value: 0 },
    { op: 'remove', path: '/nope' },
    { op: 'replace', path: '/b/x', value: 100 },
  ]);
  let err: unknown;
  try {
    apply(doc, ops);
  } catch (e) {
    err = e;
  }
  assert.ok(err instanceof PatchError);
  assert.equal(err.opIndex, 2);
  assert.equal(err.path, '/nope');
  assert.match(err.reason, /does not exist/);
  assert.deepEqual(doc, { a: [1, 2], b: { x: 1 } }); // unchanged
});

test('PatchError carries opIndex, path and reason', () => {
  try {
    apply({ a: 1 }, [
      { op: 'test', path: '/a', value: 1 },
      { op: 'test', path: '/a', value: 2 },
    ]);
    assert.unreachable();
  } catch (e) {
    assert.ok(e instanceof PatchError);
    assert.equal(e.opIndex, 1);
    assert.equal(e.path, '/a');
    assert.match(e.reason, /test failed/);
  }
});

test('inverse replays exactly: object overwrite, array order, move paths', () => {
  const cases: { doc: unknown; ops: Op[] }[] = [
    { doc: { a: 1 }, ops: [{ op: 'add', path: '/a', value: 2 }] }, // overwrite inverse is replace
    { doc: { a: 1 }, ops: [{ op: 'add', path: '/b', value: 2 }] }, // inverse is remove
    { doc: [1, 2, 3], ops: [{ op: 'remove', path: '/1' }] }, // restores order
    { doc: [1, 2, 3], ops: [{ op: 'add', path: '/1', value: 9 }] },
    { doc: [1, 2, 3], ops: [{ op: 'add', path: '/-', value: 4 }] },
    { doc: { a: { x: 1 }, b: [1, 2] }, ops: [{ op: 'move', from: '/a/x', path: '/b/0' }] },
    { doc: [1, 2, 3, 4], ops: [{ op: 'move', from: '/0', path: '/3' }] },
    { doc: { a: 1 }, ops: [{ op: 'move', from: '/a', path: '/b' }] },
    { doc: { a: 1 }, ops: [{ op: 'replace', path: '', value: [1, 2] }] }, // root replace
    {
      doc: { a: [1, { b: 2 }], c: 'x' },
      ops: [
        { op: 'test', path: '/c', value: 'x' },
        { op: 'remove', path: '/a/1' },
        { op: 'add', path: '/a/-', value: { z: [true] } },
        { op: 'move', from: '/c', path: '/a/0/k' },
        { op: 'replace', path: '/a/0/k', value: null },
      ],
    },
  ];
  for (const { doc, ops } of cases) {
    const original = JSON.parse(JSON.stringify(doc));
    const r = apply(doc as never, ops);
    // inverse is not a single root replace masquerading as undo
    if (ops.length > 1) {
      assert.ok(!(r.inverse.length === 1 && r.inverse[0]!.op === 'replace' && r.inverse[0]!.path === ''));
    }
    const back = apply(r.doc, r.inverse);
    assert.ok(deepEqual(back.doc, original as never), `inverse failed for ${JSON.stringify(ops)}`);
  }
});

test('apply never mutates its inputs', () => {
  const doc = frozen({ a: [1, 2, { x: 1 }], b: null });
  const ops = frozenOps([
    { op: 'remove', path: '/a/2' },
    { op: 'add', path: '/a/1', value: { y: [1] } },
    { op: 'move', from: '/b', path: '/a/0/k' },
  ]);
  const r = apply(doc, ops);
  assert.deepEqual(doc, { a: [1, 2, { x: 1 }], b: null });
  const back = apply(r.doc, frozenOps(r.inverse));
  assert.deepEqual(back.doc, { a: [1, 2, { x: 1 }], b: null });
});
