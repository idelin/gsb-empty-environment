import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { test } from 'node:test';
import { Assembler, iterativeEqual } from '../src/assemble.ts';
import { BuildError } from '../src/errors.ts';
import { Op, treeRecords, type OpCode, type Rec } from '../src/format.ts';
import { fromArray, type Seg } from '../src/path.ts';
import { TreeStore } from '../src/store.ts';

const k = (key: string): Seg => ({ t: 'k', k: key });
const i = (idx: number): Seg => ({ t: 'i', i: idx });
const rec = (segs: Seg[], op: OpCode, val?: string | number): Rec => ({ path: fromArray(segs), op, val });

function tmpdir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'pstore-'));
}

function buildFromTree(store: TreeStore, tree: unknown): string {
  const b = store.beginBuild();
  b.feedAll(treeRecords(tree));
  return b.commit();
}

test('child path under a scalar parent conflicts; failed commit leaves the tree untouched', () => {
  const store = TreeStore.open(tmpdir());
  const v1 = buildFromTree(store, { a: 1, b: 'keep' });

  const b = store.beginBuild();
  b.feed(rec([], Op.Obj));
  b.feed(rec([k('a')], Op.Num, 1));
  b.feed(rec([k('a'), k('b')], Op.Str, 'x')); // $.a is a number, yet has a child
  assert.throws(
    () => b.commit(),
    (e: unknown) => {
      assert.ok(e instanceof BuildError);
      assert.match((e as BuildError).message, /scalar parent/);
      assert.equal((e as BuildError).at, '$.a.b');
      return true;
    },
  );
  assert.equal(store.currentVersion, v1);
  assert.ok(iterativeEqual(store.getTree(), { a: 1, b: 'keep' }));
});

test('duplicate definitions are rejected with their path', () => {
  const store = TreeStore.open(tmpdir());
  const b = store.beginBuild();
  b.feed(rec([], Op.Obj));
  b.feed(rec([k('a')], Op.Num, 1));
  b.feed(rec([k('a')], Op.Num, 2));
  assert.throws(() => b.commit(), (e: unknown) => {
    assert.ok(e instanceof BuildError);
    assert.match((e as BuildError).message, /duplicate definition/);
    assert.equal((e as BuildError).at, '$.a');
    return true;
  });
  assert.equal(store.currentVersion, null);
});

test('reference integrity: a child without its parent is rejected at the parent path', () => {
  const store = TreeStore.open(tmpdir());
  const b = store.beginBuild();
  b.feed(rec([], Op.Obj));
  b.feed(rec([k('a'), k('b')], Op.Str, 'x')); // $.a itself is never defined
  assert.throws(() => b.commit(), (e: unknown) => {
    assert.ok(e instanceof BuildError);
    assert.match((e as BuildError).message, /missing parent/);
    assert.equal((e as BuildError).at, '$.a');
    return true;
  });
  assert.equal(store.currentVersion, null);
});

test('container type conflicts are located: index into object, key into array', () => {
  const store = TreeStore.open(tmpdir());
  const b1 = store.beginBuild();
  b1.feed(rec([], Op.Obj));
  b1.feed(rec([i(0)], Op.Num, 1));
  assert.throws(() => b1.commit(), (e: unknown) => {
    assert.match((e as BuildError).message, /array index \[0\] appears inside an object/);
    assert.equal((e as BuildError).at, '$[0]');
    return true;
  });

  const b2 = store.beginBuild();
  b2.feed(rec([], Op.Arr));
  b2.feed(rec([k('a')], Op.Num, 1));
  assert.throws(() => b2.commit(), (e: unknown) => {
    assert.match((e as BuildError).message, /object key "a" appears inside an array/);
    assert.equal((e as BuildError).at, '$.a');
    return true;
  });
});

test('tombstones are patch-only and rejected in a full build', () => {
  const store = TreeStore.open(tmpdir());
  const b = store.beginBuild();
  b.feed(rec([], Op.Obj));
  b.feed(rec([k('a')], Op.Missing));
  assert.throws(() => b.commit(), (e: unknown) => {
    assert.match((e as BuildError).message, /tombstone/);
    assert.equal((e as BuildError).at, '$.a');
    return true;
  });
});

test('prefix cursor opened before a subtree move keeps the old version; inverse patch restores', () => {
  const store = TreeStore.open(tmpdir());
  const original = { a: { x: 1, y: [10, 20] }, b: 'keep', c: {} };
  const v1 = buildFromTree(store, original);

  // open a prefix cursor on the old version BEFORE the move
  const cursor = store.queryPrefix(v1, [k('a')]);

  const { version: v2, inverse } = store.moveSubtree(v1, [k('a')], [k('c'), k('moved')]);
  assert.notEqual(v2, v1);
  assert.equal(store.currentVersion, v2);

  // the cursor still reads the pre-move version
  const rows = [...cursor];
  assert.ok(rows.length > 0);
  const asm = new Assembler();
  for (const row of rows) asm.push({ path: fromArray(row.path), op: row.op, val: row.val });
  assert.ok(iterativeEqual(asm.finish(), { x: 1, y: [10, 20] }));

  // the new version reflects the move, the old one is untouched
  assert.ok(iterativeEqual(store.getTree(v2), { b: 'keep', c: { moved: { x: 1, y: [10, 20] } } }));
  assert.ok(iterativeEqual(store.getTree(v1), original));

  // inverse patch restores the original tree exactly
  const v3 = store.applyPatch(v2, inverse);
  assert.ok(iterativeEqual(store.getTree(v3), original));
});

test('failed patches and illegal moves never change the original tree', () => {
  const store = TreeStore.open(tmpdir());
  const original = { a: 1, b: { c: 2 } };
  const v1 = buildFromTree(store, original);

  // patch that turns $.a into a scalar but also adds a child under it
  assert.throws(
    () => store.applyPatch(v1, [rec([k('a')], Op.Num, 9), rec([k('a'), k('x')], Op.Str, 'bad')]),
    (e: unknown) => (e as BuildError).at === '$.a.x' && /scalar/.test((e as Error).message),
  );
  assert.equal(store.currentVersion, v1);
  assert.ok(iterativeEqual(store.getTree(), original));

  // moving a subtree into itself is rejected
  assert.throws(() => store.moveSubtree(v1, [k('b')], [k('b'), k('c'), k('d')]), /into itself/);
  // moving under a scalar is rejected, located at the scalar parent
  assert.throws(
    () => store.moveSubtree(v1, [k('b')], [k('a'), k('x')]),
    (e: unknown) => (e as BuildError).at === '$.a' && /scalar/.test((e as Error).message),
  );
  assert.equal(store.currentVersion, v1);
  assert.equal(store.versions().length, 1);
  assert.ok(iterativeEqual(store.getTree(), original));
});

test('crash after index write but before root publish recovers without exposing a half tree', () => {
  const dir = tmpdir();
  let store = TreeStore.open(dir);
  const original = { stable: true, list: [1, 2, 3] };
  const v1 = buildFromTree(store, original);

  const b = store.beginBuild();
  b.feedAll(treeRecords({ stable: false, half: { tree: [1, 2, 3] } }));
  const crashedId = b.id;
  store.hooks.afterIndexWrite = () => {
    throw new Error('simulated crash');
  };
  assert.throws(() => b.commit(), /simulated crash/);

  // the new index is fully on disk, but the root pointer was never published
  assert.ok(fs.existsSync(path.join(dir, `${crashedId}.idx`)));
  assert.equal(store.currentVersion, v1);

  // recovery: reopening exposes only the last published version
  store = TreeStore.open(dir);
  assert.equal(store.currentVersion, v1);
  assert.equal(store.hasVersion(crashedId), false);
  assert.ok(!fs.existsSync(path.join(dir, `${crashedId}.idx`)), 'orphan index cleaned');
  assert.ok(iterativeEqual(store.getTree(), original));
});

test('memory pressure spills sorted runs to disk; out-of-order arrival still commits correctly', () => {
  const dir = tmpdir();
  const store = TreeStore.open(dir, { spillThreshold: 8 });
  const tree = {
    a: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9],
    b: { x: 1, y: 2, z: 3, w: [true, false, null] },
    c: 's'.repeat(100),
    d: {},
    e: [],
    f: [1, , 3], // eslint-disable-line no-sparse-arrays
  };
  const recs = [...treeRecords(tree)];

  // deterministic shuffle: records arrive out of order
  let seed = 7;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let n = recs.length - 1; n > 0; n--) {
    const j = Math.floor(rand() * (n + 1));
    [recs[n], recs[j]] = [recs[j], recs[n]];
  }

  const b = store.beginBuild();
  b.feedAll(recs);
  assert.ok(b.spills >= 1, 'records spilled to disk');
  const v = b.commit();

  const rebuilt = store.getTree(v) as { f: unknown[] };
  assert.ok(iterativeEqual(rebuilt, tree));
  assert.equal(1 in rebuilt.f, false, 'array hole survived the spill round-trip');
  assert.equal(fs.readdirSync(dir).filter((f) => f.endsWith('.run')).length, 0, 'spill runs cleaned');
});

test('object key order is canonicalized, array order is significant', () => {
  const dir = tmpdir();
  const store = TreeStore.open(dir);
  const v1 = buildFromTree(store, { a: 1, b: 2, c: { d: 3, e: 4 } });
  const v2 = buildFromTree(store, { c: { e: 4, d: 3 }, b: 2, a: 1 });
  assert.ok(
    fs.readFileSync(store.idxPath(v1)).equals(fs.readFileSync(store.idxPath(v2))),
    'same object, different key order -> identical canonical index',
  );
  const v3 = buildFromTree(store, [1, 2, 3]);
  const v4 = buildFromTree(store, [3, 2, 1]);
  assert.ok(!fs.readFileSync(store.idxPath(v3)).equals(fs.readFileSync(store.idxPath(v4))));
});

test('prefix query streams relative rows and materializes subtrees', () => {
  const store = TreeStore.open(tmpdir());
  const v = buildFromTree(store, { a: { x: 1, y: [10, 20] }, b: 'keep' });

  const rows = [...store.queryPrefix(v, [k('a'), k('y')])];
  assert.deepEqual(
    rows.map((r) => r.path),
    [[], [i(0)], [i(1)]],
  );
  assert.deepEqual(
    rows.map((r) => r.op),
    [Op.Arr, Op.Num, Op.Num],
  );
  assert.deepEqual(store.materializePrefix(v, [k('a'), k('y')]), [10, 20]);
  assert.deepEqual(store.materializePrefix(v, [k('a')]), { x: 1, y: [10, 20] });
  assert.equal(store.materializePrefix(v, [k('nope')]), undefined);
});
