import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SlottedPage,
  PageType,
  PageFullError,
  DuplicateKeyError,
  PAGE_SIZE,
  type Probe,
} from "../src/slottedPage.ts";

/** Deterministic variable-length keys: "key-0000" .. "key-0149" plus padding. */
function makeKeys(count: number): string[] {
  const keys: string[] = [];
  for (let i = 0; i < count; i++) {
    const pad = "x".repeat(i % 11); // variable length 8..18 bytes
    keys.push(`key-${String(i).padStart(4, "0")}${pad}`);
  }
  return keys;
}

function shuffled<T>(items: T[], seed: number): T[] {
  const arr = [...items];
  let s = seed;
  for (let i = arr.length - 1; i > 0; i--) {
    s = (s * 1103515245 + 12345) & 0x7fffffff;
    const j = s % (i + 1);
    [arr[i], arr[j]] = [arr[j], arr[i]];
  }
  return arr;
}

test("leaf page holds 150 variable-length keys in a single 4KB page", () => {
  const page = SlottedPage.create(PageType.Leaf);
  const keys = makeKeys(150);

  // Insert in shuffled order; slots must stay sorted.
  for (const [i, key] of shuffled(keys, 42).entries()) {
    page.insertLeaf(key, { pageId: 1000 + i, slotId: i });
  }

  assert.equal(page.slotCount, 150);
  assert.ok(page.freeSpace >= 0, "directory and cells must not overlap");

  // Slot directory is sorted ascending regardless of insertion order.
  const sorted = [...page.keys()];
  assert.deepEqual(sorted, [...keys].sort());
});

test("every key is located via binary search in O(log K) comparisons", () => {
  const page = SlottedPage.create(PageType.Leaf);
  const keys = makeKeys(150);
  const rids = new Map<string, { pageId: number; slotId: number }>();

  for (const [i, key] of shuffled(keys, 7).entries()) {
    const rid = { pageId: 5000 + i, slotId: (i * 13) % 251 };
    rids.set(key, rid);
    page.insertLeaf(key, rid);
  }

  const maxComparisons = Math.ceil(Math.log2(150)) + 1; // <= 9 for K=150
  for (const key of keys) {
    const probe: Probe = { comparisons: 0 };
    const loc = page.find(key, probe);
    assert.ok(loc, `key ${key} must be found`);
    assert.ok(
      probe.comparisons <= maxComparisons,
      `binary search used ${probe.comparisons} comparisons (> ${maxComparisons})`,
    );
    // The located slot offset points at the exact cell with the right RID.
    assert.deepEqual(page.readRid(loc!), rids.get(key));
    // Slot index matches the key's rank in sorted order.
    assert.equal(page.keyAt(loc!.slotIndex), key);
  }

  // Missing keys are reported without scanning.
  const probe: Probe = { comparisons: 0 };
  assert.equal(page.find("key-9999", probe), null);
  assert.ok(probe.comparisons <= maxComparisons);
});

test("insertion returns exact cell offsets and slots grow toward the middle", () => {
  const page = SlottedPage.create(PageType.Leaf);
  const first = page.insertLeaf("alpha", { pageId: 1, slotId: 0 });
  const second = page.insertLeaf("omega", { pageId: 2, slotId: 0 });
  const middle = page.insertLeaf("middle", { pageId: 3, slotId: 0 });

  // Cells grow downwards from PAGE_SIZE.
  assert.equal(first.offset, PAGE_SIZE - first.length);
  assert.ok(second.offset < first.offset);
  assert.ok(middle.offset < second.offset);

  // Slot directory order follows key order, not insertion order.
  assert.deepEqual([...page.keys()], ["alpha", "middle", "omega"]);
  assert.equal(page.find("middle")!.slotIndex, 1);
});

test("internal page stores key -> childPageId with binary search", () => {
  const page = SlottedPage.create(PageType.Internal);
  const separators: Array<[string, number]> = [
    ["a", 101],
    ["m", 102],
    ["t", 103],
    ["zebra", 104],
  ];
  for (const [key, child] of shuffled(separators, 3)) {
    page.insertInternal(key, child);
  }
  assert.deepEqual([...page.keys()], ["a", "m", "t", "zebra"]);
  for (const [key, child] of separators) {
    const loc = page.find(key);
    assert.ok(loc);
    assert.equal(page.readChildPageId(loc!), child);
  }
});

test("duplicate keys are rejected", () => {
  const page = SlottedPage.create(PageType.Leaf);
  page.insertLeaf("dup", { pageId: 1, slotId: 1 });
  assert.throws(() => page.insertLeaf("dup", { pageId: 2, slotId: 2 }), DuplicateKeyError);
});

test("page full is reported when 4KB is exhausted", () => {
  const page = SlottedPage.create(PageType.Leaf);
  let inserted = 0;
  assert.throws(() => {
    for (let i = 0; ; i++) {
      page.insertLeaf(`overflow-${String(i).padStart(5, "0")}-${"y".repeat(i % 17)}`, {
        pageId: i,
        slotId: i,
      });
      inserted++;
    }
  }, PageFullError);
  assert.ok(inserted > 100, `expected >100 cells per 4KB page, got ${inserted}`);
});

test("page survives a serialization round-trip through its raw buffer", () => {
  const page = SlottedPage.create(PageType.Leaf);
  for (const key of makeKeys(150)) {
    page.insertLeaf(key, { pageId: 9, slotId: key.length });
  }
  const restored = SlottedPage.from(Buffer.from(page.buffer));
  assert.equal(restored.slotCount, 150);
  for (const key of makeKeys(150)) {
    const loc = restored.find(key);
    assert.ok(loc);
    assert.deepEqual(restored.readRid(loc!), { pageId: 9, slotId: key.length });
  }
});
