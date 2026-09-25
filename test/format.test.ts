import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assemble, iterativeEqual } from '../src/assemble.ts';
import { BuildError } from '../src/errors.ts';
import {
  encodeRawRec,
  encodeTree,
  encodeTreeBuffer,
  Op,
  RecordDecoder,
  type Rec,
} from '../src/format.ts';
import { toArray } from '../src/path.ts';

function decodeAll(buf: Buffer, chunking: number | number[]): Rec[] {
  const dec = new RecordDecoder();
  const out: Rec[] = [];
  let off = 0;
  const push = (n: number) => {
    out.push(...dec.push(buf.subarray(off, off + n)));
    off += n;
  };
  if (typeof chunking === 'number') {
    while (off < buf.length) push(Math.min(chunking, buf.length - off));
  } else {
    for (const n of chunking) push(n);
  }
  dec.finish();
  return out;
}

test("{'0':'x'} stays an object, never becomes an array", () => {
  const raws = [...encodeTree({ '0': 'x' })];
  assert.equal(raws[0].op, Op.Obj); // container declared as object
  assert.deepEqual(raws[1].segs, [{ t: 'k', k: '0' }]); // numeric-looking key stays a string key

  const tree = assemble(decodeAll(encodeTreeBuffer({ '0': 'x' }), 1));
  assert.equal(Array.isArray(tree), false);
  assert.deepEqual(tree, { '0': 'x' });

  // a real array uses index segments and stays an array
  const arrRaws = [...encodeTree(['x'])];
  assert.equal(arrRaws[0].op, Op.Arr);
  assert.deepEqual(arrRaws[1].segs, [{ t: 'i', i: 0 }]);
  const arr = assemble(decodeAll(encodeTreeBuffer(['x']), 1));
  assert.ok(Array.isArray(arr));

  // several numeric keys, out of order, still an object
  const mixed = assemble(decodeAll(encodeTreeBuffer({ '2': 'b', '10': 'c', '1': 'a' }), 3));
  assert.equal(Array.isArray(mixed), false);
  assert.deepEqual(mixed, { '1': 'a', '2': 'b', '10': 'c' });
});

test('empty containers, null, holes and missing are encoded explicitly', () => {
  assert.deepEqual(assemble(decodeAll(encodeTreeBuffer({}), 3)), {});
  assert.deepEqual(assemble(decodeAll(encodeTreeBuffer([]), 3)), []);
  assert.equal(assemble(decodeAll(encodeTreeBuffer(null), 1)), null);
  assert.equal(assemble(decodeAll(encodeTreeBuffer(false), 2)), false);
  assert.equal(assemble(decodeAll(encodeTreeBuffer(0), 1)), 0);
  assert.equal(assemble(decodeAll(encodeTreeBuffer(''), 1)), '');

  // array holes: explicit Hole records, rebuilt as real elisions
  const sparse = [1, , 3]; // eslint-disable-line no-sparse-arrays
  const raws = [...encodeTree(sparse)];
  assert.ok(raws.some((r) => r.op === Op.Hole));
  const rebuilt = assemble(decodeAll(encodeTreeBuffer(sparse), 2)) as unknown[];
  assert.ok(Array.isArray(rebuilt));
  assert.equal(rebuilt.length, 3);
  assert.equal(1 in rebuilt, false);
  assert.equal(rebuilt[0], 1);
  assert.equal(rebuilt[2], 3);

  // missing (tombstone) survives a wire round-trip...
  const wire = Buffer.concat([
    encodeRawRec({ pop: 0, segs: [], op: Op.Obj }),
    encodeRawRec({ pop: 0, segs: [{ t: 'k', k: 'gone' }], op: Op.Missing }),
  ]);
  const recs = decodeAll(wire, 1);
  assert.equal(recs[1].op, Op.Missing);
  assert.deepEqual(toArray(recs[1].path), [{ t: 'k', k: 'gone' }]);
  // ...but is rejected in a tree build, located at its path
  assert.throws(() => assemble(recs), (e: unknown) => {
    assert.ok(e instanceof BuildError);
    assert.match((e as BuildError).message, /tombstone/);
    assert.equal((e as BuildError).at, '$.gone');
    return true;
  });

  // empty containers nested inside a larger tree keep their type
  const tree = { e: {}, a: [], n: null, s: [[]] };
  assert.ok(iterativeEqual(assemble(decodeAll(encodeTreeBuffer(tree), 5)), tree));
});

test('arbitrary chunking round-trips node-by-node', () => {
  const tree = {
    a: [1, 'two', null, true, { b: [] }],
    c: {},
    '0': 'zero',
    nest: { x: [{ y: 3.5 }], z: 'üñï✓' },
  };
  const buf = encodeTreeBuffer(tree);
  const baseline = decodeAll(buf, buf.length);

  const chunkings: Array<number | number[]> = [1, 2, 3, 5, 7, 64, 4096];
  // deterministic pseudo-random chunking
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  const sizes: number[] = [];
  for (let left = buf.length; left > 0; ) {
    const s = 1 + Math.floor(rand() * Math.min(left, 97));
    sizes.push(s);
    left -= s;
  }
  chunkings.push(sizes);

  for (const c of chunkings) {
    const recs = decodeAll(buf, c);
    assert.equal(recs.length, baseline.length, `chunking ${String(c)}`);
    for (let i = 0; i < recs.length; i++) {
      assert.deepEqual(toArray(recs[i].path), toArray(baseline[i].path));
      assert.equal(recs[i].op, baseline[i].op);
      assert.equal(recs[i].val, baseline[i].val);
    }
    assert.ok(iterativeEqual(assemble(recs), tree), `chunking ${String(c)}`);
  }
});

test('non-JSON values are rejected with their path', () => {
  assert.throws(() => [...encodeTree({ a: undefined })], /non-JSON value.*\$\.a/);
  assert.throws(() => [...encodeTree({ a: { b: Number.NaN } })], /non-finite number.*\$\.a\.b/);
  assert.throws(() => [...encodeTree([1, () => 2])], /non-JSON value.*\$\[1\]/);
});

test('depth 100k is handled iteratively; chunked round-trip is node-by-node equal', () => {
  const DEPTH = 100_000;
  let tree: unknown = 'leaf';
  for (let i = 0; i < DEPTH; i++) tree = { n: tree }; // {n:{n:...:'leaf'}}

  const buf = encodeTreeBuffer(tree); // delta-encoded: O(depth) bytes, no recursion
  const recs = decodeAll(buf, 65536);
  assert.equal(recs.length, DEPTH + 1);
  assert.ok(iterativeEqual(assemble(recs), tree));

  // byte-at-a-time decoding at depth: chunk boundaries cannot corrupt paths
  const recs1 = decodeAll(buf, 1);
  assert.equal(recs1.length, recs.length);
  for (let i = 0; i < recs.length; i += 9973) {
    assert.deepEqual(toArray(recs1[i].path), toArray(recs[i].path));
    assert.equal(recs1[i].op, recs[i].op);
  }
  assert.ok(iterativeEqual(assemble(recs1), tree));

  // alternating object/array chain exercises both segment kinds at depth
  let mixed: unknown = 0;
  for (let i = 0; i < 50_000; i++) mixed = i % 2 === 0 ? { k: mixed } : [mixed];
  assert.ok(iterativeEqual(assemble(decodeAll(encodeTreeBuffer(mixed), 13)), mixed));
});
