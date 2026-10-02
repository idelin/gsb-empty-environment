import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DuplicateKeyError,
  HEADER_SIZE,
  InternalPage,
  LeafPage,
  PAGE_SIZE,
  PageFullError,
  PageType,
  SLOT_SIZE,
  type Rid,
} from "../src/slotted-page.ts";

/** Deterministic variable-length keys, 14-18 bytes each. */
function makeKey(i: number): string {
  return `user:${String(i).padStart(8, "0")}:${"x".repeat(i % 5)}`;
}

function makeRid(i: number): Rid {
  return { pageId: 1000 + i, slotId: i % 64 };
}

/** Deterministic Fisher-Yates shuffle (LCG seeded). */
function shuffled(n: number, seed: number): number[] {
  const arr = Array.from({ length: n }, (_, i) => i);
  let state = seed >>> 0;
  const rand = () => (state = (state * 1664525 + 1013904223) >>> 0) / 2 ** 32;
  for (let i = n - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

const KEY_COUNT = 150;

function buildLeaf(): { page: LeafPage; order: number[] } {
  const page = new LeafPage();
  const order = shuffled(KEY_COUNT, 42);
  for (const i of order) {
    page.insert(makeKey(i), makeRid(i));
  }
  return { page, order };
}

test("empty leaf page has a well-formed header", () => {
  const page = new LeafPage();
  assert.equal(page.buf.length, PAGE_SIZE);
  assert.equal(page.pageType, PageType.Leaf);
  assert.equal(page.slotCount, 0);
  assert.equal(page.freeStart, HEADER_SIZE);
  assert.equal(page.freeEnd, PAGE_SIZE);
  assert.equal(page.freeSpace, PAGE_SIZE - HEADER_SIZE);
});

test("acceptance: 150 variable-length string keys fit in one 4KB page", () => {
  const { page } = buildLeaf();

  // All 150 keys live inside a single 4KB page.
  assert.equal(page.slotCount, KEY_COUNT);
  assert.ok(page.freeSpace >= 0, "directory and records must not overlap");

  // Slot directory grew toward the middle: 16-byte header + 150 * 4-byte slots.
  assert.equal(page.freeStart, HEADER_SIZE + KEY_COUNT * SLOT_SIZE);
  // Records grew toward the middle from the page end.
  assert.ok(page.freeEnd < PAGE_SIZE);
  assert.ok(page.freeStart < page.freeEnd);
});

test("slot directory stays sorted ascending under shuffled insertion", () => {
  const { page } = buildLeaf();
  let prev: Buffer | null = null;
  for (const { key } of page.entries()) {
    if (prev !== null) assert.ok(prev.compare(key) < 0, "keys must be strictly ascending");
    prev = Buffer.from(key);
  }
});

test("every key is located in O(log K) probes with the exact slot offset", () => {
  const { page } = buildLeaf();
  const maxProbes = Math.ceil(Math.log2(KEY_COUNT)); // 8 for K = 150
  let worst = 0;

  for (let i = 0; i < KEY_COUNT; i++) {
    const hit = page.lookup(makeKey(i));
    assert.ok(hit, `key ${makeKey(i)} must be found`);
    assert.deepEqual(hit.rid, makeRid(i));

    // The returned offset must be exactly what the slot directory stores.
    assert.equal(hit.offset, page.recordOffset(hit.slot));
    // The slot entry at that offset must decode back to the same key.
    const dirOff = page.slotDirOffset(hit.slot);
    assert.equal(page.buf.readUInt16LE(dirOff), hit.offset);
    const keyLen = page.buf.readUInt16LE(dirOff + 2);
    assert.equal(
      page.buf.subarray(hit.offset, hit.offset + keyLen).toString("utf8"),
      makeKey(i),
    );

    assert.ok(
      page.lastProbeCount <= maxProbes,
      `binary search used ${page.lastProbeCount} probes, bound is ${maxProbes}`,
    );
    worst = Math.max(worst, page.lastProbeCount);
  }
  assert.ok(worst <= maxProbes);
});

test("lookup returns null for absent keys", () => {
  const { page } = buildLeaf();
  assert.equal(page.lookup("user:99999999:xx"), null);
  assert.equal(page.lookup("aaa"), null);
  assert.equal(page.lookup("zzz"), null);
});

test("duplicate keys are rejected", () => {
  const page = new LeafPage();
  page.insert("alpha", { pageId: 1, slotId: 0 });
  assert.throws(() => page.insert("alpha", { pageId: 2, slotId: 1 }), DuplicateKeyError);
  assert.equal(page.slotCount, 1);
});

test("page full raises PageFullError instead of corrupting the page", () => {
  const page = new LeafPage();
  let inserted = 0;
  assert.throws(() => {
    for (let i = 0; ; i++) {
      page.insert(`key-${String(i).padStart(6, "0")}`, { pageId: i, slotId: 0 });
      inserted = i + 1;
    }
  }, PageFullError);
  assert.ok(inserted > KEY_COUNT, `expected > ${KEY_COUNT} short keys, got ${inserted}`);
  // Page is still consistent and searchable after the failed insert.
  const hit = page.lookup("key-000000");
  assert.ok(hit);
  assert.equal(hit.rid.pageId, 0);
});

test("delete removes the slot and keeps the page searchable", () => {
  const { page } = buildLeaf();
  const before = page.slotCount;
  assert.equal(page.delete(makeKey(0)), true);
  assert.equal(page.delete(makeKey(75)), true);
  assert.equal(page.delete(makeKey(0)), false, "double delete must fail");
  assert.equal(page.slotCount, before - 2);
  assert.equal(page.lookup(makeKey(0)), null);
  assert.equal(page.lookup(makeKey(75)), null);
  // Everything else is still reachable.
  for (let i = 1; i < KEY_COUNT; i++) {
    if (i === 75) continue;
    assert.ok(page.lookup(makeKey(i)), `key ${i} still present`);
  }
});

test("page survives a round-trip through its raw buffer", () => {
  const { page } = buildLeaf();
  const reopened = new LeafPage(Buffer.from(page.buf)); // as if read back from disk
  assert.equal(reopened.slotCount, KEY_COUNT);
  for (let i = 0; i < KEY_COUNT; i += 17) {
    assert.deepEqual(reopened.lookup(makeKey(i))?.rid, makeRid(i));
  }
  assert.throws(() => new InternalPage(reopened.buf), /page type mismatch/);
});

test("internal node routes keys to the correct child in O(log K)", () => {
  const page = new InternalPage();
  page.setLeftmostChild(0);
  // Separators: k010 -> child 1, k020 -> child 2, ..., k100 -> child 10
  const separators: string[] = [];
  for (let i = 1; i <= 10; i++) {
    const sep = `k${String(i * 10).padStart(3, "0")}`;
    separators.push(sep);
    page.insert(sep, i);
  }
  assert.equal(page.pageType, PageType.Internal);
  assert.equal(page.slotCount, 10);

  const maxProbes = Math.ceil(Math.log2(10));
  const cases: Array<[string, number]> = [
    ["k005", 0], // below first separator -> leftmost child
    ["k010", 1], // exact separator
    ["k015", 1], // between k010 and k020
    ["k055", 5],
    ["k100", 10], // exact last separator
    ["k999", 10], // above all separators
  ];
  for (const [key, expected] of cases) {
    assert.equal(page.findChild(key), expected, `routing of ${key}`);
    assert.ok(page.lastProbeCount <= maxProbes);
  }
});

test("internal node with 150 separators supports binary-search routing", () => {
  const page = new InternalPage();
  page.setLeftmostChild(0);
  for (let i = 1; i <= KEY_COUNT; i++) {
    page.insert(makeKey(i * 2), i); // even keys as separators
  }
  assert.equal(page.slotCount, KEY_COUNT);
  const maxProbes = Math.ceil(Math.log2(KEY_COUNT));
  for (let i = 1; i <= KEY_COUNT; i++) {
    assert.equal(page.findChild(makeKey(i * 2)), i); // exact separator
    assert.ok(page.lastProbeCount <= maxProbes);
    assert.equal(page.findChild(makeKey(i * 2 - 1)), i - 1); // odd keys route left
    assert.ok(page.lastProbeCount <= maxProbes);
  }
});

test("binary encoding: slot directory and records are laid out as specified", () => {
  const page = new LeafPage();
  page.insert("bravo", { pageId: 7, slotId: 3 });
  page.insert("alpha", { pageId: 5, slotId: 1 }); // sorts before bravo

  assert.equal(page.buf.readUInt8(0), PageType.Leaf);
  assert.equal(page.buf.readUInt16LE(2), 2); // slotCount
  assert.equal(page.buf.readUInt16LE(4), HEADER_SIZE + 2 * SLOT_SIZE); // freeStart

  // Slot 0 = "alpha" (sorted first). Records are placed in insertion order
  // from the page end: "bravo" first, so "alpha" sits directly below it.
  const recAlpha = page.buf.readUInt16LE(HEADER_SIZE);
  assert.equal(recAlpha, PAGE_SIZE - 2 * (5 + 6));
  assert.equal(page.buf.readUInt16LE(HEADER_SIZE + 2), 5); // key length
  assert.equal(page.buf.subarray(recAlpha, recAlpha + 5).toString("utf8"), "alpha");
  assert.equal(page.buf.readUInt32LE(recAlpha + 5), 5); // rid.pageId
  assert.equal(page.buf.readUInt16LE(recAlpha + 9), 1); // rid.slotId

  // Slot 1 = "bravo", record at the very end of the page (inserted first).
  const recBravo = page.buf.readUInt16LE(HEADER_SIZE + SLOT_SIZE);
  assert.equal(recBravo, PAGE_SIZE - (5 + 6));
  assert.equal(page.buf.readUInt32LE(recBravo + 5), 7);
});
