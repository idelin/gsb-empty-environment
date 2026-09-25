import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DigestMismatchError, PatchApplyError } from '../src/json.ts';
import { digestOfTree } from '../src/digest.ts';
import { applyPatch, buildPatch } from '../src/patch.ts';
import type { Patch } from '../src/patch.ts';

// 'list' arrays use stable ids; everything else uses explicit positions.
const idArrays = {
  arrayMode: (path: ReadonlyArray<string | number>) =>
    (path[path.length - 1] === 'list' ? 'id' : 'position') as 'id' | 'position',
};

const baseDoc = () => ({
  a: { b: 1 },
  list: [{ id: 'x', v: 1 }, { id: 'y', v: 2 }],
});

function capture(fn: () => unknown): Error {
  try {
    fn();
  } catch (e) {
    return e as Error;
  }
  assert.fail('expected the call to throw');
}

test('base digest is validated before any modification', () => {
  const base = baseDoc();
  const snapshot = structuredClone(base);
  const patch: Patch = {
    ops: [{ op: 'set', path: ['a', 'b'], value: 999 }],
    baseDigest: 'not-the-real-digest',
    targetDigest: 'whatever',
  };
  const err = capture(() => applyPatch(base, patch, idArrays));
  assert.ok(err instanceof DigestMismatchError);
  assert.deepEqual(base, snapshot); // untouched
});

test('a failing op rolls back all indices and the root', () => {
  const base = baseDoc();
  const snapshot = structuredClone(base);
  const patch: Patch = {
    ops: [
      { op: 'set', path: ['a', 'b'], value: 2 }, // succeeds, then must be rolled back
      { op: 'remove', path: ['a', 'missing'] }, // fails: nothing to remove
    ],
    baseDigest: digestOfTree(base, idArrays),
    targetDigest: 'deadbeef',
  };
  const err = capture(() => applyPatch(base, patch, idArrays));
  assert.ok(err instanceof PatchApplyError);
  assert.equal(err.opIndex, 1);
  assert.deepEqual(err.path, ['a', 'missing']); // error locates the failing path
  assert.equal(err.restored, true); // rollback restored the pre-apply digest
  assert.deepEqual(base, snapshot); // caller's root untouched
  assert.equal(digestOfTree(base, idArrays), patch.baseDigest);
});

test('target digest mismatch after apply also rolls back', () => {
  const base = baseDoc();
  const snapshot = structuredClone(base);
  const patch = buildPatch(base, { a: { b: 2 }, list: base.list }, idArrays);
  patch.targetDigest = 'tampered';
  const err = capture(() => applyPatch(base, patch, idArrays));
  assert.ok(err instanceof DigestMismatchError);
  assert.deepEqual(base, snapshot);
});

test('inverse patch restores the exact original tree', () => {
  const base = baseDoc();
  const target = {
    a: { b: 2, c: 'new' },
    list: [{ id: 'y', v: 2 }, { id: 'x', v: 9 }], // swapped + property edit
    extra: [1, 2, 3],
  };
  const patch = buildPatch(base, target, idArrays);
  const applied = applyPatch(base, patch, idArrays);
  assert.deepEqual(applied.root, target);
  assert.equal(digestOfTree(applied.root, idArrays), patch.targetDigest);
  // id index reflects the reordered array.
  assert.deepEqual([...applied.idIndex.get('$.list')!.entries()], [['y', 0], ['x', 1]]);
  // Inverse patch restores base exactly.
  const reverted = applyPatch(applied.root, applied.inverse, idArrays);
  assert.deepEqual(reverted.root, base);
  assert.equal(digestOfTree(reverted.root, idArrays), patch.baseDigest);
});

test('precondition expectOld failure is located and rolled back', () => {
  const base = baseDoc();
  const patch: Patch = {
    ops: [{ op: 'set', path: ['a', 'b'], value: 5, expectOld: 999 }],
    baseDigest: digestOfTree(base, idArrays),
    targetDigest: 'unused',
  };
  const err = capture(() => applyPatch(base, patch, idArrays));
  assert.ok(err instanceof PatchApplyError);
  assert.deepEqual(err.path, ['a', 'b']);
  assert.match(err.message, /expectOld/);
});
