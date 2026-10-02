import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { Broadphase } from "../src/broadphase.ts";
import type { AABB } from "../src/aabb.ts";
import { bruteForcePairs, mulberry32, pairKeys, randomAabb } from "./utils.ts";

function boxAt(x: number, y: number, z: number, half = 0.5): AABB {
  return { minX: x - half, minY: y - half, minZ: z - half, maxX: x + half, maxY: y + half, maxZ: z + half };
}

describe("Broadphase", () => {
  it("新增实体后 collectPairs 报告重叠对", () => {
    const bp = new Broadphase<number>();
    bp.add(1, boxAt(0, 0, 0));
    bp.add(2, boxAt(0.5, 0, 0)); // 与 1 重叠
    bp.add(3, boxAt(10, 0, 0)); // 远离
    const pairs = bp.collectPairs();
    assert.deepEqual(pairKeys(pairs), new Set(["1|2"]));
  });

  it("增量移动：仅移动实体参与的新重叠对被报告", () => {
    const bp = new Broadphase<number>();
    bp.add(1, boxAt(0, 0, 0));
    bp.add(2, boxAt(10, 0, 0));
    assert.equal(bp.collectPairs().length, 0);

    bp.move(2, boxAt(0.4, 0, 0)); // 移向 1 并重叠
    assert.deepEqual(pairKeys(bp.collectPairs()), new Set(["1|2"]));

    bp.move(2, boxAt(10, 0, 0)); // 移开
    assert.equal(bp.collectPairs().length, 0);
  });

  it("小幅移动被 fat AABB 余量吸收，仍能正确报告", () => {
    const bp = new Broadphase<number>(0.5);
    bp.add(1, boxAt(0, 0, 0));
    bp.add(2, boxAt(5, 0, 0));
    bp.collectPairs();
    // 2 缓慢逼近 1，每步 0.2（小于 margin），多步后进入重叠
    for (let step = 1; step <= 20; step++) {
      bp.move(2, boxAt(5 - step * 0.2, 0, 0));
    }
    const pairs = bp.collectPairs();
    assert.deepEqual(pairKeys(pairs), new Set(["1|2"]));
  });

  it("删除实体后不再出现在碰撞对中", () => {
    const bp = new Broadphase<number>();
    bp.add(1, boxAt(0, 0, 0));
    bp.add(2, boxAt(0.5, 0, 0));
    bp.remove(2);
    bp.add(3, boxAt(0.5, 0, 0));
    assert.deepEqual(pairKeys(bp.collectPairs()), new Set(["1|3"]));
  });

  it("未知实体操作抛出异常", () => {
    const bp = new Broadphase<number>();
    assert.throws(() => bp.move(99, boxAt(0, 0, 0)));
    assert.throws(() => bp.remove(99));
    bp.add(1, boxAt(0, 0, 0));
    assert.throws(() => bp.add(1, boxAt(0, 0, 0)));
  });

  it("随机场景：computeAllPairs 与暴力比对一致", () => {
    const rand = mulberry32(31337);
    const bp = new Broadphase<number>();
    const items: Array<[number, AABB]> = [];
    for (let i = 0; i < 300; i++) {
      const box = randomAabb(rand, 30, 2);
      items.push([i, box]);
      bp.add(i, box);
    }
    assert.deepEqual(pairKeys(bp.computeAllPairs()), pairKeys(bruteForcePairs(items)));
  });

  it("随机场景：全部移动后 collectPairs 与暴力比对一致", () => {
    const rand = mulberry32(777);
    const bp = new Broadphase<number>();
    const items: Array<[number, AABB]> = [];
    for (let i = 0; i < 300; i++) {
      const box = randomAabb(rand, 30, 2);
      items.push([i, box]);
      bp.add(i, box);
    }
    bp.collectPairs(); // 清空初始 moved 缓冲

    // 全部实体随机移动
    for (let i = 0; i < 300; i++) {
      const box = randomAabb(rand, 30, 2);
      items[i] = [i, box];
      bp.move(i, box);
    }
    const pairs = bp.collectPairs();
    assert.deepEqual(pairKeys(pairs), pairKeys(bruteForcePairs(items)));
    assert.deepEqual(pairKeys(bp.computeAllPairs()), pairKeys(bruteForcePairs(items)));
    // moved 缓冲已清空，再次收集应为空
    assert.equal(bp.collectPairs().length, 0);
  });

  it("支持字符串实体 id", () => {
    const bp = new Broadphase<string>();
    bp.add("hero", boxAt(0, 0, 0));
    bp.add("crate", boxAt(0.3, 0, 0));
    bp.add("tree", boxAt(20, 0, 0));
    assert.deepEqual(pairKeys(bp.collectPairs()), new Set(["crate|hero"]));
  });
});
