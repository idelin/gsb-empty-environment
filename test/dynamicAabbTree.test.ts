import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { DynamicAabbTree } from "../src/dynamicAabbTree.ts";
import { overlaps } from "../src/aabb.ts";
import type { AABB } from "../src/aabb.ts";
import { bruteForcePairs, mulberry32, pairKeys, randomAabb } from "./utils.ts";

function queryAll(tree: DynamicAabbTree<number>, aabb: AABB): number[] {
  const hits: number[] = [];
  tree.query(aabb, (userData) => hits.push(userData));
  return hits.sort((a, b) => a - b);
}

describe("DynamicAabbTree", () => {
  it("插入与范围查询：结果与暴力比对一致", () => {
    const rand = mulberry32(42);
    const tree = new DynamicAabbTree<number>();
    const boxes: AABB[] = [];
    for (let i = 0; i < 200; i++) {
      const box = randomAabb(rand, 50, 3);
      boxes.push(box);
      tree.createProxy(box, i);
    }
    tree.validate();
    assert.equal(tree.proxyCount, 200);

    for (let q = 0; q < 50; q++) {
      const queryBox = randomAabb(rand, 50, 10);
      const expected: number[] = [];
      for (let i = 0; i < boxes.length; i++) {
        if (overlaps(queryBox, boxes[i])) expected.push(i);
      }
      assert.deepEqual(queryAll(tree, queryBox), expected);
    }
  });

  it("删除代理后查询结果保持正确", () => {
    const rand = mulberry32(7);
    const tree = new DynamicAabbTree<number>();
    const boxes: AABB[] = [];
    const proxies: number[] = [];
    for (let i = 0; i < 200; i++) {
      const box = randomAabb(rand, 50, 3);
      boxes.push(box);
      proxies.push(tree.createProxy(box, i));
    }
    // 删除偶数编号的代理
    for (let i = 0; i < 200; i += 2) {
      tree.destroyProxy(proxies[i]);
    }
    tree.validate();
    assert.equal(tree.proxyCount, 100);

    for (let q = 0; q < 50; q++) {
      const queryBox = randomAabb(rand, 50, 10);
      const expected: number[] = [];
      for (let i = 1; i < 200; i += 2) {
        if (overlaps(queryBox, boxes[i])) expected.push(i);
      }
      assert.deepEqual(queryAll(tree, queryBox), expected);
    }
  });

  it("moveProxy 增量重定位后查询正确", () => {
    const rand = mulberry32(99);
    const tree = new DynamicAabbTree<number>();
    const boxes: AABB[] = [];
    const proxies: number[] = [];
    for (let i = 0; i < 100; i++) {
      const box = randomAabb(rand, 20, 2);
      boxes.push(box);
      proxies.push(tree.createProxy(box, i));
    }
    // 随机移动 40 个代理
    for (let k = 0; k < 40; k++) {
      const i = Math.floor(rand() * 100);
      const box = randomAabb(rand, 20, 2);
      boxes[i] = box;
      tree.moveProxy(proxies[i], box);
    }
    tree.validate();

    for (let q = 0; q < 30; q++) {
      const queryBox = randomAabb(rand, 20, 8);
      const expected: number[] = [];
      for (let i = 0; i < 100; i++) {
        if (overlaps(queryBox, boxes[i])) expected.push(i);
      }
      assert.deepEqual(queryAll(tree, queryBox), expected);
    }
  });

  it("大量随机插入/删除/移动后树保持平衡且结构合法", () => {
    const rand = mulberry32(2024);
    const tree = new DynamicAabbTree<number>();
    const proxies: number[] = [];
    for (let step = 0; step < 3000; step++) {
      const op = rand();
      if (op < 0.5 || proxies.length === 0) {
        proxies.push(tree.createProxy(randomAabb(rand, 100, 5), step));
      } else if (op < 0.8) {
        const idx = Math.floor(rand() * proxies.length);
        tree.destroyProxy(proxies[idx]);
        proxies.splice(idx, 1);
      } else {
        const idx = Math.floor(rand() * proxies.length);
        tree.moveProxy(proxies[idx], randomAabb(rand, 100, 5));
      }
    }
    tree.validate();
    // 平衡性：高度应保持在 O(log N) 量级
    const n = Math.max(tree.proxyCount, 2);
    assert.ok(
      tree.getHeight() <= Math.ceil(2.2 * Math.log2(n)) + 2,
      `tree height ${tree.getHeight()} too large for ${tree.proxyCount} proxies`,
    );
  });

  it("自相交遍历：叶节点重叠对与暴力比对一致", () => {
    const rand = mulberry32(555);
    const tree = new DynamicAabbTree<number>();
    const items: Array<[number, AABB]> = [];
    for (let i = 0; i < 150; i++) {
      const box = randomAabb(rand, 15, 2);
      items.push([i, box]);
      tree.createProxy(box, i);
    }
    const pairs: Array<[number, number]> = [];
    tree.forEachOverlappingPair((a, b) => pairs.push([a, b]));
    assert.deepEqual(pairKeys(pairs), pairKeys(bruteForcePairs(items)));
  });
});
