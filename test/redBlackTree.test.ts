import { test } from "node:test";
import assert from "node:assert/strict";
import { RedBlackTree } from "../src/redBlackTree.ts";

const cmp = (a: number, b: number): number => (a < b ? -1 : a > b ? 1 : 0);

test("红黑树插入后中序遍历保持升序", () => {
  const tree = new RedBlackTree<number, string>(cmp);
  const keys: number[] = [];
  for (let i = 0; i < 5000; i++) {
    const k = (i * 2654435761) % 100003;
    if (!tree.has(k)) {
      tree.insert(k, `v${k}`);
      keys.push(k);
    }
  }
  const ordered = tree.inOrder().map((e) => e.key);
  const expected = [...keys].sort((a, b) => a - b);
  assert.deepEqual(ordered, expected);
  assert.equal(tree.size, expected.length);
});

test("ceiling 返回大于等于 key 的最小条目，min 返回环首", () => {
  const tree = new RedBlackTree<number, string>(cmp);
  for (const k of [10, 20, 30, 40]) tree.insert(k, `n${k}`);
  assert.deepEqual(tree.ceiling(10), { key: 10, value: "n10" });
  assert.deepEqual(tree.ceiling(11), { key: 20, value: "n20" });
  assert.deepEqual(tree.ceiling(35), { key: 40, value: "n40" });
  assert.equal(tree.ceiling(41), null, "越过环尾应为 null，由调用方回绕");
  assert.deepEqual(tree.min(), { key: 10, value: "n10" });
});

test("删除后结构仍正确且查找结果准确", () => {
  const tree = new RedBlackTree<number, string>(cmp);
  const keys: number[] = [];
  for (let i = 0; i < 3000; i++) {
    const k = (i * 40503) % 65536;
    if (!tree.has(k)) {
      tree.insert(k, `v${k}`);
      keys.push(k);
    }
  }
  // 删除一半
  const survivors: number[] = [];
  keys.forEach((k, i) => {
    if (i % 2 === 0) assert.equal(tree.delete(k), true);
    else survivors.push(k);
  });
  assert.equal(tree.delete(99999999), false);
  const ordered = tree.inOrder().map((e) => e.key);
  assert.deepEqual(ordered, survivors.sort((a, b) => a - b));
  for (const k of survivors) {
    assert.equal(tree.get(k), `v${k}`);
  }
});
