import { test } from "node:test";
import assert from "node:assert/strict";
import { BPlusTree, MAX_KEYS } from "../src/bplustree.ts";

const N = 500;
const records = () => Array.from({ length: N }, (_, i) => i + 1);

test("bulk-loaded full tree: height 4, fully packed pages, empty free list", () => {
  const tree = BPlusTree.bulkLoad(records());
  // 125 leaves (4 keys each) + 25 + 5 + 1 internal pages = 156
  assert.equal(tree.height(), 4);
  assert.equal(tree.allocatedPages, 156);
  assert.equal(tree.freePages, 0);
  assert.equal(tree.leafKeys().length, N);
  tree.validate();
});

test("acceptance: delete 500 -> 3 records, height shrinks 4 -> 1, pages recycled", () => {
  const tree = BPlusTree.bulkLoad(records());
  const initialPages = tree.allocatedPages;

  const heights = [tree.height()];
  for (let key = 1; key <= N - 3; key++) {
    assert.ok(tree.delete(key), `delete(${key})`);
    tree.validate(); // invariants hold after every single merge/borrow
    const h = tree.height();
    if (h !== heights[heights.length - 1]) heights.push(h);
  }

  // Height decreased monotonically one level at a time: 4 -> 3 -> 2 -> 1.
  assert.deepEqual(heights, [4, 3, 2, 1]);
  assert.equal(tree.rootShrinkCount, 3);

  // Exactly the 3 surviving records remain, root is a leaf.
  assert.deepEqual(tree.leafKeys(), [498, 499, 500]);
  for (const k of [498, 499, 500]) assert.ok(tree.search(k));
  assert.ok(!tree.search(1));

  // Every emptied page (155 of 156) was returned to the free page list.
  assert.equal(tree.allocatedPages, 1);
  assert.equal(tree.freePages, initialPages - 1);
  assert.equal(tree.pm.capacity, initialPages); // no slot leaked or duplicated

  // Physical merges actually happened at both leaf and internal levels.
  assert.ok(tree.mergeCount > 0);
  assert.ok(tree.borrowCount > 0);
});

test("free page list reuses recycled page ids", () => {
  const tree = BPlusTree.bulkLoad(records());
  for (let key = 1; key <= 250; key++) tree.delete(key);
  const freed = tree.freePages;
  assert.ok(freed > 0);
  const capacityBefore = tree.pm.capacity;
  // Allocating again must pop from the free list instead of growing storage.
  const id = tree.pm.allocate({} as never);
  assert.equal(tree.freePages, freed - 1);
  assert.equal(tree.pm.capacity, capacityBefore);
  tree.pm.free(id);
});

test("cascading merge is order-independent (seeded random deletion)", () => {
  const tree = BPlusTree.bulkLoad(records());
  // Deterministic LCG shuffle of the deletion order.
  const order = records();
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) % 2 ** 31);
  for (let i = order.length - 1; i > 0; i--) {
    const j = rand() % (i + 1);
    [order[i], order[j]] = [order[j], order[i]];
  }
  const survivors = order.slice(N - 3).sort((a, b) => a - b);
  for (const key of order.slice(0, N - 3)) {
    assert.ok(tree.delete(key));
    tree.validate();
  }
  assert.equal(tree.height(), 1);
  assert.deepEqual(tree.leafKeys(), survivors);
  assert.equal(tree.allocatedPages, 1);
  assert.equal(tree.freePages, 155);
});

test("deleting a missing key is a no-op", () => {
  const tree = BPlusTree.bulkLoad(records());
  assert.ok(!tree.delete(9999));
  assert.equal(tree.allocatedPages, 156);
  tree.validate();
});

test("max keys constant sanity", () => {
  assert.equal(MAX_KEYS, 4);
});
