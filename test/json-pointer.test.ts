import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  applyJsonPatch,
  canonicalJson,
  formatPointer,
  jsonEqual,
  JsonPatchError,
  parsePointer,
} from '../src/index.js';
import type { JsonObject } from '../src/types.js';

test('parsePointer handles escapes, root and formatting round trips', () => {
  assert.deepEqual(parsePointer(''), []);
  assert.deepEqual(parsePointer('/a/b~1c~0d'), ['a', 'b/c~d']);
  assert.equal(formatPointer(['a', 'b/c~d']), '/a/b~1c~0d');
  assert.throws(() => parsePointer('a/0'), /invalid JSON Pointer/);
  assert.throws(() => parsePointer('/a~2'), /invalid JSON Pointer escape/);
});

test('canonicalJson ignores key order and jsonEqual is structural', () => {
  const a: JsonObject = { z: 1, a: { y: 2, x: [1, 2] } };
  const b: JsonObject = { a: { x: [1, 2], y: 2 }, z: 1 };
  assert.equal(canonicalJson(a), canonicalJson(b));
  assert.ok(jsonEqual(a, b));
  assert.ok(!jsonEqual({ x: [1] }, { x: [1, 2] }));
});

test('atomic apply rolls back every earlier operation on failure', () => {
  const doc: JsonObject = { list: [{ keep: true }], meta: { v: 1 } };
  const original = structuredClone(doc);
  assert.throws(
    () =>
      applyJsonPatch(doc, [
        { op: 'add', path: '/list/-', value: { temp: true } },
        { op: 'replace', path: '/meta/missing', value: 2 },
      ]),
    (error: unknown) => {
      assert.ok(error instanceof JsonPatchError);
      assert.equal(error.operationIndex, 1);
      return true;
    },
  );
  assert.deepEqual(doc, original);
});

test('move removes the source and inserts at the destination, copy keeps source', () => {
  const moved = applyJsonPatch(
    { a: { deep: 1 }, b: {} },
    [{ op: 'move', from: '/a/deep', path: '/b/deep' }],
  );
  assert.deepEqual(moved.doc, { a: {}, b: { deep: 1 } });
  assert.ok(moved.changed);
  assert.deepEqual(moved.touchedPaths, ['/a/deep', '/b/deep']);

  const copied = applyJsonPatch({ a: 1 }, [{ op: 'copy', from: '/a', path: '/b' }]);
  assert.deepEqual(copied.doc, { a: 1, b: 1 });
});

test('array add splices at an index and refuses out-of-range positions', () => {
  const result = applyJsonPatch({ xs: [1, 2] }, [
    { op: 'add', path: '/xs/1', value: 9 },
  ]);
  assert.deepEqual(result.doc, { xs: [1, 9, 2] });
  assert.throws(
    () => applyJsonPatch({ xs: [1] }, [{ op: 'add', path: '/xs/5', value: 9 }]),
    JsonPatchError,
  );
});

test('a failed test op rejects the whole patch without mutation', () => {
  const doc: JsonObject = { v: 1 };
  assert.throws(
    () =>
      applyJsonPatch(doc, [
        { op: 'replace', path: '/v', value: 2 },
        { op: 'test', path: '/v', value: 1 },
      ]),
    (error: unknown) => error instanceof JsonPatchError && error.operationIndex === 1,
  );
  assert.deepEqual(doc, { v: 1 });
});

test('a semantically empty patch reports changed=false', () => {
  const result = applyJsonPatch({ v: 1 }, [{ op: 'replace', path: '/v', value: 1 }]);
  assert.equal(result.changed, false);
  assert.deepEqual(result.touchedPaths, ['/v']);
});
