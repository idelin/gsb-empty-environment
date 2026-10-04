import { test } from "node:test";
import assert from "node:assert/strict";
import { RadixTree, compareBytes, type Bytes } from "../src/radix-tree.ts";

/** Reference model: a sorted key set backed by a Map. */
class Reference {
  private map = new Map<string, number>();

  private static enc(key: Bytes): string {
    return Buffer.from(key).toString("hex");
  }

  private static dec(hex: string): Bytes {
    return new Uint8Array(Buffer.from(hex, "hex"));
  }

  set(key: Bytes, value: number): void {
    this.map.set(Reference.enc(key), value);
  }

  delete(key: Bytes): boolean {
    return this.map.delete(Reference.enc(key));
  }

  get size(): number {
    return this.map.size;
  }

  entries(prefix: Bytes = new Uint8Array(0)): Array<[Bytes, number]> {
    const out: Array<[Bytes, number]> = [];
    for (const [hex, value] of this.map) {
      const key = Reference.dec(hex);
      if (startsWith(key, prefix)) out.push([key, value]);
    }
    out.sort((a, b) => compareBytes(a[0], b[0]));
    return out;
  }

  longestCommonPrefix(query: Bytes): Bytes {
    for (let d = query.length; d >= 0; d--) {
      const candidate = query.subarray(0, d);
      for (const [key] of this.entries()) {
        if (startsWith(key, candidate)) return query.slice(0, d);
      }
    }
    return new Uint8Array(0);
  }

  lcpOfAllKeys(): Bytes {
    const all = this.entries();
    if (all.length === 0) return new Uint8Array(0);
    let lcp = all[0][0];
    for (const [key] of all) {
      let i = 0;
      while (i < lcp.length && i < key.length && lcp[i] === key[i]) i++;
      lcp = lcp.slice(0, i);
    }
    return lcp;
  }
}

function startsWith(key: Bytes, prefix: Bytes): boolean {
  if (prefix.length > key.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (key[i] !== prefix[i]) return false;
  }
  return true;
}

function b(text: string): Bytes {
  return new Uint8Array(Buffer.from(text, "latin1"));
}

function hex(key: Bytes): string {
  return Buffer.from(key).toString("hex");
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

function assertSameEntries(
  actual: Array<[Bytes, number]>,
  expected: Array<[Bytes, number]>,
  context: string,
): void {
  assert.equal(actual.length, expected.length, `${context}: length`);
  for (let i = 0; i < actual.length; i++) {
    assert.equal(hex(actual[i][0]), hex(expected[i][0]), `${context}: key #${i}`);
    assert.equal(actual[i][1], expected[i][1], `${context}: value #${i}`);
  }
}

test("empty key, basic set/get/has/delete", () => {
  const tree = new RadixTree<number>();
  assert.equal(tree.size, 0);
  assert.equal(tree.has(b("")), false);

  assert.equal(tree.set(b(""), 1), true);
  assert.equal(tree.set(b("a"), 2), true);
  assert.equal(tree.has(b("")), true);
  assert.equal(tree.get(b("")), 1);
  assert.equal(tree.get(b("a")), 2);
  assert.equal(tree.get(b("ab")), undefined);

  // Overwrite keeps size, bumps version.
  const v = tree.version;
  assert.equal(tree.set(b("a"), 22), false);
  assert.equal(tree.size, 2);
  assert.equal(tree.get(b("a")), 22);
  assert.ok(tree.version > v);

  assert.equal(tree.delete(b("ab")), false);
  assert.equal(tree.delete(b("")), true);
  assert.equal(tree.has(b("")), false);
  assert.equal(tree.size, 1);
  tree.validate();
});

test("edge split in the middle", () => {
  const tree = new RadixTree<number>();
  tree.set(b("abc"), 1);
  // "abd" diverges inside the "abc" edge label -> split "ab" + ("c" | "d").
  tree.set(b("abd"), 2);
  tree.validate();
  assertSameEntries(tree.entries(), [[b("abc"), 1], [b("abd"), 2]], "split");

  // Key ending exactly at the split point marks the intermediate node.
  tree.set(b("ab"), 3);
  tree.validate();
  assertSameEntries(
    tree.entries(),
    [[b("ab"), 3], [b("abc"), 1], [b("abd"), 2]],
    "split+mid key",
  );

  // New key that itself splits an edge from below ("abcd" under "abc").
  tree.set(b("abcd"), 4);
  tree.validate();
  assertSameEntries(
    tree.entries(b("abc")),
    [[b("abc"), 1], [b("abcd"), 4]],
    "prefix under split",
  );
});

test("prefix key deletion merges single child, keeps terminal markers", () => {
  const tree = new RadixTree<number>();
  tree.set(b("ab"), 1);
  tree.set(b("abc"), 2);
  const before = tree.nodeCount();

  // Deleting the prefix key "ab" leaves a non-terminal single-child node,
  // which must be merged: root gains a single "abc" edge.
  assert.equal(tree.delete(b("ab")), true);
  tree.validate();
  assertSameEntries(tree.entries(), [[b("abc"), 2]], "after prefix delete");
  assert.ok(tree.nodeCount() < before, "single-child node merged");

  // Deleting the longer key keeps the terminal marker on "ab".
  const tree2 = new RadixTree<number>();
  tree2.set(b("ab"), 1);
  tree2.set(b("abc"), 2);
  assert.equal(tree2.delete(b("abc")), true);
  tree2.validate();
  assertSameEntries(tree2.entries(), [[b("ab"), 1]], "terminal preserved");
  assert.equal(tree2.get(b("ab")), 1);

  // Chain merge: deleting "x" collapses "a"->"b"->"c" into one edge.
  const tree3 = new RadixTree<number>();
  tree3.set(b("x"), 9);
  tree3.set(b("abc"), 1);
  tree3.set(b("abd"), 2);
  tree3.delete(b("abd"));
  tree3.validate();
  assertSameEntries(tree3.entries(), [[b("abc"), 1], [b("x"), 9]], "chain");
});

test("longestCommonPrefix and lcpOfAllKeys", () => {
  const tree = new RadixTree<number>();
  tree.set(b("flower"), 1);
  tree.set(b("flow"), 2);
  tree.set(b("flight"), 3);

  assert.equal(hex(tree.longestCommonPrefix(b("flowing"))), hex(b("flow")));
  // Diverges inside an edge label: "flow" + "er" vs query "flowep".
  assert.equal(hex(tree.longestCommonPrefix(b("flowep"))), hex(b("flowe")));
  assert.equal(hex(tree.longestCommonPrefix(b("fl"))), hex(b("fl")));
  assert.equal(hex(tree.longestCommonPrefix(b("xyz"))), hex(b("")));
  assert.equal(hex(tree.longestCommonPrefix(b(""))), hex(b("")));
  assert.equal(hex(tree.lcpOfAllKeys()), hex(b("fl")));

  const empty = new RadixTree<number>();
  assert.equal(hex(empty.lcpOfAllKeys()), hex(b("")));
  assert.equal(hex(empty.longestCommonPrefix(b("abc"))), hex(b("")));
});

test("iterator: version binding, paging, snapshot continuation", () => {
  const tree = new RadixTree<number>();
  const keys = ["", "a", "ab", "abc", "b", "b\x00", "\xff", "ca"];
  keys.forEach((k, i) => tree.set(b(k), i));
  const expectedAll = keys
    .map((k, i) => [b(k), i] as [Bytes, number])
    .sort((x, y) => compareBytes(x[0], y[0]));

  const it = tree.iterator();
  assert.equal(it.version, tree.version);
  assert.equal(it.isStale(), false);
  assert.equal(it.remaining, keys.length);

  // First page of 3.
  const page1 = it.nextPage(3);
  assert.equal(page1.done, false);
  assertSameEntries(page1.entries, expectedAll.slice(0, 3), "page1");

  // Mutate the tree; the iterator keeps walking its snapshot.
  tree.set(b("zzz"), 99);
  tree.delete(b("ab"));
  assert.equal(it.isStale(), true);

  const rest: Array<[Bytes, number]> = [];
  for (;;) {
    const page = it.nextPage(2);
    rest.push(...page.entries);
    if (page.done) break;
  }
  assertSameEntries([...page1.entries, ...rest], expectedAll, "snapshot pages");
  assert.equal(it.remaining, 0);

  // Empty page after exhaustion.
  const tail = it.nextPage(5);
  assert.equal(tail.entries.length, 0);
  assert.equal(tail.done, true);

  // for...of over a prefixed snapshot iterator.
  const prefixed = [...tree.iterator(b("a"))];
  assertSameEntries(prefixed, [[b("a"), 1], [b("abc"), 3]], "prefixed for-of");

  // Invalid page size rejected.
  assert.throws(() => tree.iterator().nextPage(0), RangeError);
});

test("randomized cross-check against sorted reference model", async (t) => {
  for (const seed of [0xc0ffee, 7, 12345]) {
    await t.test(`seed ${seed}`, () => runRandomized(seed, 2000));
  }
});

function runRandomized(seed: number, rounds: number): void {
  const rand = mulberry32(seed);
  const alphabet = [0x00, 0x61, 0x62, 0x63, 0xff];
  const tree = new RadixTree<number>();
  const ref = new Reference();
  const liveKeys: Bytes[] = [];

  const randomKey = (): Bytes => {
    const len = Math.floor(rand() * 7); // 0..6, includes the empty key
    const key = new Uint8Array(len);
    for (let i = 0; i < len; i++) {
      key[i] = alphabet[Math.floor(rand() * alphabet.length)];
    }
    return key;
  };

  const checkAll = (round: number): void => {
    tree.validate();
    assert.equal(tree.size, ref.size, `round ${round}: size`);
    assertSameEntries(tree.entries(), ref.entries(), `round ${round}: entries`);
    assertSameEntries(
      tree.entries(b("ab")),
      ref.entries(b("ab")),
      `round ${round}: prefix ab`,
    );
    assertSameEntries(
      tree.entries(b("")),
      ref.entries(b("")),
      `round ${round}: prefix empty`,
    );
    const query = randomKey();
    assert.equal(
      hex(tree.longestCommonPrefix(query)),
      hex(ref.longestCommonPrefix(query)),
      `round ${round}: lcp query ${hex(query)}`,
    );
    assert.equal(
      hex(tree.lcpOfAllKeys()),
      hex(ref.lcpOfAllKeys()),
      `round ${round}: lcp all`,
    );
  };

  for (let round = 0; round < rounds; round++) {
    const roll = rand();
    if (roll < 0.55) {
      const key = randomKey();
      const value = Math.floor(rand() * 1e9);
      tree.set(key, value);
      ref.set(key, value);
      if (!liveKeys.some((k) => hex(k) === hex(key))) liveKeys.push(key);
    } else if (roll < 0.85 && liveKeys.length > 0) {
      const key = liveKeys[Math.floor(rand() * liveKeys.length)];
      assert.equal(tree.delete(key), true, `round ${round}: delete ${hex(key)}`);
      ref.delete(key);
      liveKeys.splice(liveKeys.findIndex((k) => hex(k) === hex(key)), 1);
    } else {
      const key = randomKey();
      const existed = tree.delete(key);
      assert.equal(existed, ref.delete(key), `round ${round}: random delete`);
      if (existed) {
        // Random key happened to exist; keep the live-key list in sync.
        liveKeys.splice(liveKeys.findIndex((k) => hex(k) === hex(key)), 1);
      }
    }
    if (round % 100 === 0) checkAll(round);
  }
  checkAll(rounds);

  // Snapshot continuation: bind an iterator, mutate heavily, then drain pages
  // and compare against the reference snapshot taken at creation time.
  const snapshotRef = ref.entries();
  const it = tree.iterator();
  const versionAtBind = tree.version;
  for (let i = 0; i < 50; i++) {
    const key = randomKey();
    tree.set(key, i);
    ref.set(key, i);
  }
  assert.notEqual(tree.version, versionAtBind);
  assert.equal(it.isStale(), true);
  const drained: Array<[Bytes, number]> = [];
  for (;;) {
    const page = it.nextPage(1 + Math.floor(rand() * 5));
    drained.push(...page.entries);
    if (page.done) break;
  }
  assertSameEntries(drained, snapshotRef, "snapshot continuation");
}
