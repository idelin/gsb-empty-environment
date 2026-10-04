import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  RadixTree,
  RadixSnapshot,
  commonPrefixLength,
  type Entry,
} from '../src/radix-tree.ts';

const enc = (s: string): Uint8Array =>
  new Uint8Array(Array.from(s, (ch) => ch.charCodeAt(0)));

const hex = (key: Uint8Array): string =>
  Array.from(key, (b) => b.toString(16).padStart(2, '0')).join(' ');

/** Reference model: a sorted key set implemented on a plain Map. */
class RefModel {
  private readonly map = new Map<string, number>();

  private static id(key: Uint8Array): string {
    let s = '';
    for (const byte of key) s += String.fromCharCode(byte);
    return s;
  }

  private static bytes(id: string): Uint8Array {
    const out = new Uint8Array(id.length);
    for (let i = 0; i < id.length; i++) out[i] = id.charCodeAt(i);
    return out;
  }

  clone(): RefModel {
    const copy = new RefModel();
    for (const [k, v] of this.map) copy.map.set(k, v);
    return copy;
  }

  set(key: Uint8Array, value: number): void {
    this.map.set(RefModel.id(key), value);
  }

  delete(key: Uint8Array): boolean {
    return this.map.delete(RefModel.id(key));
  }

  has(key: Uint8Array): boolean {
    return this.map.has(RefModel.id(key));
  }

  get size(): number {
    return this.map.size;
  }

  entries(prefix: Uint8Array = new Uint8Array(0)): Array<Entry<number>> {
    const pid = RefModel.id(prefix);
    return [...this.map.entries()]
      .filter(([k]) => k.startsWith(pid))
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
      .map(([k, v]) => ({ key: RefModel.bytes(k), value: v }));
  }

  /** Longest prefix of `query` that is a prefix of some stored key. */
  lcpOf(query: Uint8Array): Uint8Array {
    let best = 0;
    for (const k of this.map.keys()) {
      best = Math.max(best, commonPrefixLength(query, RefModel.bytes(k)));
    }
    return query.slice(0, best);
  }
}

function assertEntriesEqual(
  actual: Array<Entry<number>>,
  expected: Array<Entry<number>>,
  context: string,
): void {
  assert.equal(
    actual.length,
    expected.length,
    `${context}: length ${actual.length} != ${expected.length}`,
  );
  for (let i = 0; i < actual.length; i++) {
    assert.equal(
      hex(actual[i].key),
      hex(expected[i].key),
      `${context}: key #${i}`,
    );
    assert.equal(actual[i].value, expected[i].value, `${context}: value #${i}`);
  }
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('edge split in the middle of a label', () => {
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  const put = (s: string, v: number) => {
    assert.equal(tree.set(enc(s), v), true);
    ref.set(enc(s), v);
    tree.checkInvariants();
    assertEntriesEqual([...tree.entries()], ref.entries(), `after set(${s})`);
  };
  // Forces a split of the edge "romane" into "roman" + "e"/"us".
  put('romane', 1);
  put('romanus', 2);
  // Key ends exactly at the split point: mid node becomes terminal.
  put('rom', 3);
  // Splits the root edge "rom..." vs "rubens" at "r".
  put('rubens', 4);
  put('rube', 5);
  // New key extends an existing terminal key.
  put('romanesque', 6);
  assert.equal(tree.size, ref.size);
  assert.deepEqual(
    Array.from(tree.keys(), hex),
    ref.entries().map((e) => hex(e.key)),
  );
  // Updating an existing key keeps size and order.
  assert.equal(tree.set(enc('rom'), 30), false);
  ref.set(enc('rom'), 30);
  tree.checkInvariants();
  assert.equal(tree.size, 6);
  assert.equal(tree.get(enc('rom')), 30);
  assertEntriesEqual([...tree.entries()], ref.entries(), 'after update');
});

test('deleting a prefix key keeps the terminal marker and merges single children', () => {
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  const put = (s: string, v: number) => {
    tree.set(enc(s), v);
    ref.set(enc(s), v);
  };
  put('ab', 1);
  put('abc', 2);
  put('abd', 3);

  // Delete a leaf: the "ab" node loses a child, stays terminal, and its
  // remaining edge is merged: root --"abd"--> leaf, with "ab" terminal.
  assert.equal(tree.delete(enc('abc')), true);
  ref.delete(enc('abc'));
  tree.checkInvariants();
  assertEntriesEqual([...tree.entries()], ref.entries(), 'after delete abc');

  // Delete the prefix key itself: its terminal marker goes away and the
  // remaining single edge is absorbed into the root edge.
  assert.equal(tree.delete(enc('ab')), true);
  ref.delete(enc('ab'));
  tree.checkInvariants();
  assertEntriesEqual([...tree.entries()], ref.entries(), 'after delete ab');
  assert.equal(tree.size, 1);

  // Terminal node must survive deletion of its only descendant key.
  const t2 = new RadixTree<number>();
  t2.set(enc('x'), 1);
  t2.set(enc('xyz'), 2);
  assert.equal(t2.delete(enc('xyz')), true);
  t2.checkInvariants();
  assert.equal(t2.get(enc('x')), 1);
  assert.equal(t2.has(enc('xyz')), false);
  assertEntriesEqual(
    [...t2.entries()],
    [{ key: enc('x'), value: 1 }],
    'terminal preserved',
  );

  // Deleting missing keys is a no-op and does not bump the version.
  const before = t2.version;
  assert.equal(t2.delete(enc('')), false);
  assert.equal(t2.delete(enc('xy')), false);
  assert.equal(t2.delete(enc('xyzz')), false);
  assert.equal(t2.version, before);
  assert.equal(t2.size, 1);
});

test('empty key and binary bytes', () => {
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  const keys = [
    new Uint8Array(0),
    Uint8Array.of(0x00),
    Uint8Array.of(0x00, 0x00),
    Uint8Array.of(0xff),
    Uint8Array.of(0x00, 0xff),
    Uint8Array.of(0x61),
  ];
  keys.forEach((k, i) => {
    tree.set(k, i);
    ref.set(k, i);
  });
  tree.checkInvariants();
  assert.equal(tree.get(new Uint8Array(0)), 0);
  assertEntriesEqual([...tree.entries()], ref.entries(), 'binary order');
  // Empty key sorts first.
  assert.equal(hex([...tree.entries()][0].key), '');
  assert.equal(tree.delete(new Uint8Array(0)), true);
  ref.delete(new Uint8Array(0));
  tree.checkInvariants();
  assert.equal(tree.has(new Uint8Array(0)), false);
  assertEntriesEqual([...tree.entries()], ref.entries(), 'after deleting empty key');
});

test('longest common prefix queries', () => {
  const tree = new RadixTree<number>();
  ['romane', 'romanus', 'rubens'].forEach((k, i) => tree.set(enc(k), i));
  assert.equal(hex(tree.longestCommonPrefix(enc('romulus'))), hex(enc('rom')));
  assert.equal(hex(tree.longestCommonPrefix(enc('romanesque'))), hex(enc('romane')));
  assert.equal(hex(tree.longestCommonPrefix(enc('ruben'))), hex(enc('ruben')));
  assert.equal(hex(tree.longestCommonPrefix(enc('xyz'))), '');
  assert.equal(hex(tree.longestCommonPrefix(new Uint8Array(0))), '');
  // Prefix ending mid-edge counts fully.
  assert.equal(hex(tree.longestCommonPrefix(enc('roman'))), hex(enc('roman')));
});

test('prefix enumeration including prefixes ending mid-edge', () => {
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  ['romane', 'romanus', 'rom', 'rubens', 'ruby', ''].forEach((k, i) => {
    tree.set(enc(k), i);
    ref.set(enc(k), i);
  });
  for (const p of ['', 'r', 'ro', 'rom', 'roma', 'romane', 'romanes', 'rub', 'ruby', 'x']) {
    assertEntriesEqual(
      [...tree.entries(enc(p))],
      ref.entries(enc(p)),
      `prefix "${p}"`,
    );
  }
});

test('paginated iteration in byte lexicographic order', () => {
  const rand = mulberry32(42);
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  for (let i = 0; i < 100; i++) {
    const len = Math.floor(rand() * 6);
    const key = new Uint8Array(len);
    for (let j = 0; j < len; j++) key[j] = Math.floor(rand() * 256);
    tree.set(key, i);
    ref.set(key, i);
  }
  tree.checkInvariants();
  for (const pageSize of [1, 2, 7, 33, 1000]) {
    const it = tree.iterator();
    const seen: Array<Entry<number>> = [];
    for (;;) {
      const page = it.nextPage(pageSize);
      if (page.length === 0) break;
      assert.ok(page.length <= pageSize);
      seen.push(...page);
    }
    assertEntriesEqual(seen, ref.entries(), `pageSize=${pageSize}`);
    assert.deepEqual(it.next(), { done: true, value: undefined });
  }
  assert.throws(() => tree.iterator().nextPage(0), RangeError);
});

test('iterator stays bound to its version across mutations', () => {
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  ['alpha', 'alpine', 'bet', 'beta', 'delta', 'epsilon', 'gamma'].forEach(
    (k, i) => {
      tree.set(enc(k), i + 1);
      ref.set(enc(k), i + 1);
    },
  );
  const it = tree.iterator();
  assert.equal(it.version, tree.version);

  // Consume one page, then mutate the tree heavily.
  const page1 = it.nextPage(3);
  assertEntriesEqual(page1, ref.entries().slice(0, 3), 'first page');

  tree.delete(enc('alpha'));
  tree.delete(enc('gamma'));
  tree.set(enc('al'), 100);
  tree.set(enc('zeta'), 200);
  tree.set(enc('beta'), 999);
  assert.ok(tree.version > it.version);

  // The iterator keeps traversing the version it was created on.
  const rest: Array<Entry<number>> = [];
  for (;;) {
    const page = it.nextPage(2);
    if (page.length === 0) break;
    rest.push(...page);
  }
  assertEntriesEqual(rest, ref.entries().slice(3), 'remaining pages');

  // The live tree reflects the mutations instead.
  const liveRef = new RefModel();
  ['al', 'alpine', 'bet', 'beta', 'delta', 'epsilon', 'zeta'].forEach((k, i) =>
    liveRef.set(enc(k), 0),
  );
  liveRef.set(enc('al'), 100);
  liveRef.set(enc('alpine'), 2);
  liveRef.set(enc('bet'), 3);
  liveRef.set(enc('beta'), 999);
  liveRef.set(enc('delta'), 5);
  liveRef.set(enc('epsilon'), 6);
  liveRef.set(enc('zeta'), 200);
  tree.checkInvariants();
  assertEntriesEqual([...tree.entries()], liveRef.entries(), 'live tree');
});

test('snapshot continues to traverse the old version', () => {
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  ['a', 'ab', 'abc', 'b', 'bc'].forEach((k, i) => {
    tree.set(enc(k), i);
    ref.set(enc(k), i);
  });
  const snap: RadixSnapshot<number> = tree.snapshot();
  const frozen = ref.clone();

  tree.delete(enc('ab'));
  tree.delete(enc('b'));
  tree.set(enc('abd'), 10);
  tree.set(enc('c'), 11);

  assert.equal(snap.version < tree.version, true);
  assert.equal(snap.size, 5);
  assert.equal(snap.get(enc('ab')), 1);
  assertEntriesEqual([...snap.entries()], frozen.entries(), 'snapshot entries');
  assertEntriesEqual(
    [...snap.entries(enc('a'))],
    frozen.entries(enc('a')),
    'snapshot prefix',
  );
  assert.equal(hex(snap.longestCommonPrefix(enc('abcd'))), hex(enc('abc')));
  // Snapshot-bound iterator paginates over the frozen state.
  const it = snap.iterator();
  const seen: Array<Entry<number>> = [];
  for (;;) {
    const page = it.nextPage(2);
    if (page.length === 0) break;
    seen.push(...page);
  }
  assertEntriesEqual(seen, frozen.entries(), 'snapshot pagination');
});

test('randomized cross-check against sorted reference model', () => {
  const rand = mulberry32(0xc0ffee);
  const tree = new RadixTree<number>();
  const ref = new RefModel();
  const alphabet = [0x00, 0x61, 0x62, 0x63, 0xff];
  const randomKey = (): Uint8Array => {
    const len = Math.floor(rand() * 7); // includes the empty key
    const key = new Uint8Array(len);
    for (let i = 0; i < len; i++) {
      key[i] = alphabet[Math.floor(rand() * alphabet.length)];
    }
    return key;
  };
  const snapshots: Array<{ snap: RadixSnapshot<number>; ref: RefModel }> = [];

  for (let op = 0; op < 600; op++) {
    const key = randomKey();
    if (rand() < 0.55) {
      const value = Math.floor(rand() * 1e9);
      assert.equal(tree.set(key, value), !ref.has(key), `set op ${op}`);
      ref.set(key, value);
    } else {
      assert.equal(tree.delete(key), ref.has(key), `delete op ${op}`);
      ref.delete(key);
    }
    tree.checkInvariants();
    assert.equal(tree.size, ref.size, `size op ${op}`);
    if (op % 20 === 0) {
      assertEntriesEqual([...tree.entries()], ref.entries(), `entries op ${op}`);
      const p = randomKey();
      assertEntriesEqual(
        [...tree.entries(p)],
        ref.entries(p),
        `prefix op ${op}`,
      );
      const q = randomKey();
      assert.equal(
        hex(tree.longestCommonPrefix(q)),
        hex(ref.lcpOf(q)),
        `lcp op ${op}`,
      );
    }
    if (op % 100 === 0) {
      snapshots.push({ snap: tree.snapshot(), ref: ref.clone() });
    }
  }

  assertEntriesEqual([...tree.entries()], ref.entries(), 'final entries');
  for (const { snap, ref: frozen } of snapshots) {
    assert.equal(snap.size, frozen.size);
    assertEntriesEqual([...snap.entries()], frozen.entries(), 'snapshot');
  }
});
