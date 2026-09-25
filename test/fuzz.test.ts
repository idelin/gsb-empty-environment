import { test } from "node:test";
import assert from "node:assert/strict";
import { FenwickIndex, NaiveIndex } from "../src/height-index.ts";
import { ListModel, type MeasureToken } from "../src/list-model.ts";
import { VirtualList } from "../src/scroller.ts";

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

/**
 * 随机操作对照：同一操作流同时驱动
 *   A) 增量 Fenwick 前缀索引的列表
 *   B) 独立全量 Naive 布局的列表
 * 每一步都对比滚动位置、锚点与可见集（而非只看最终总高度）。
 */
test("随机操作下增量索引与独立全量布局的滚动位置/可见集一致", () => {
  const SEEDS = 30;
  const STEPS = 400;
  for (let seed = 1; seed <= SEEDS; seed++) {
    const rand = mulberry32(seed * 7919);
    const pick = <T,>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];

    const listA = new VirtualList(new ListModel(40, new FenwickIndex()), 120);
    const listB = new VirtualList(new ListModel(40, new NaiveIndex()), 120);

    let counter = 0;
    const tokenPool: MeasureToken[] = [];

    const both = (fn: (l: VirtualList) => void) => {
      fn(listA);
      fn(listB);
    };

    for (let step = 0; step < STEPS; step++) {
      const size = listA.model.size;
      const roll = rand();
      if (roll < 0.25 || size === 0) {
        // 插入：随机位置，10% 零高，20% 挂到现有分组
        const id = `it-${counter++}`;
        const index = Math.floor(rand() * (size + 1));
        const estimate = rand() < 0.1 ? 0 : 10 + Math.floor(rand() * 120);
        const ids = listA.model.ids();
        const parentId = ids.length > 0 && rand() < 0.2 ? pick(ids) : undefined;
        both((l) => l.insert(index, { id, estimate, parentId }));
      } else if (roll < 0.4) {
        const id = pick(listA.model.ids());
        both((l) => l.remove(id));
      } else if (roll < 0.5 && size > 1) {
        const id = pick(listA.model.ids());
        const to = Math.floor(rand() * size);
        both((l) => l.move(id, to));
      } else if (roll < 0.6) {
        const id = pick(listA.model.ids());
        const collapsed = rand() < 0.5;
        both((l) => l.setCollapsed(id, collapsed));
      } else if (roll < 0.75) {
        // 测量：一半立即生效，一半先攒令牌制造迟到旧测量
        const id = pick(listA.model.ids());
        const h = rand() < 0.1 ? 0 : 10 + Math.floor(rand() * 200);
        if (rand() < 0.5) {
          const tokens = [listA, listB].map((l) => l.model.requestMeasure(id)!);
          const okA = listA.applyMeasure(tokens[0], h);
          const okB = listB.applyMeasure(tokens[1], h);
          assert.equal(okA, okB);
        } else {
          const [ta] = [listA, listB].map((l) => l.model.requestMeasure(id)!);
          tokenPool.push(ta);
          if (tokenPool.length > 12) tokenPool.shift();
        }
      } else if (roll < 0.85 && tokenPool.length > 0) {
        // 应用可能已过期的旧测量令牌
        const token = pick(tokenPool);
        const h = 10 + Math.floor(rand() * 200);
        const okA = listA.applyMeasure(token, h);
        const okB = listB.applyMeasure(token, h);
        assert.equal(okA, okB, `stale measure parity seed=${seed} step=${step}`);
      } else if (roll < 0.95) {
        // 滚动：正反向随机，含越界
        const current = listA.scrollTop;
        const y =
          rand() < 0.5
            ? current + Math.floor(rand() * 600) - 300
            : Math.floor(rand() * (listA.model.totalHeight() + 200)) - 50;
        both((l) => l.onScroll(y));
      } else {
        // 跳转：随机条目或随机绝对偏移
        if (rand() < 0.5 && listA.model.size > 0) {
          const id = pick(listA.model.ids());
          const off = Math.floor(rand() * 60);
          both((l) => l.jumpTo(id, off));
        } else {
          const y = Math.floor(rand() * (listA.model.totalHeight() + 300)) - 80;
          both((l) => l.jumpToOffset(y));
        }
      }

      const ctx = `seed=${seed} step=${step}`;
      assert.equal(listA.scrollTop, listB.scrollTop, `scrollTop ${ctx}`);
      assert.deepEqual(listA.getAnchor(), listB.getAnchor(), `anchor ${ctx}`);
      assert.deepEqual(
        listA.visibleRange().ids,
        listB.visibleRange().ids,
        `visible ids ${ctx}`,
      );
      assert.equal(listA.model.totalHeight(), listB.model.totalHeight(), `total ${ctx}`);
      assert.equal(listA.model.version, listB.model.version, `version ${ctx}`);
      assert.equal(listA.model.size, listB.model.size, `size ${ctx}`);
    }
  }
});

test("scrollTop 始终位于 [0, maxScroll]，锚点偏移非负", () => {
  const rand = mulberry32(42);
  const list = new VirtualList(new ListModel(40, new FenwickIndex()), 100);
  let counter = 0;
  for (let step = 0; step < 2000; step++) {
    const roll = rand();
    const size = list.model.size;
    if (roll < 0.4 || size === 0) {
      list.insert(Math.floor(rand() * (size + 1)), {
        id: `x-${counter++}`,
        estimate: rand() < 0.15 ? 0 : Math.floor(rand() * 150),
      });
    } else if (roll < 0.7) {
      const ids = list.model.ids();
      list.remove(ids[Math.floor(rand() * ids.length)]);
    } else {
      list.onScroll(list.scrollTop + Math.floor(rand() * 800) - 400);
    }
    assert.ok(list.scrollTop >= 0, `scrollTop >= 0 step=${step}`);
    assert.ok(list.scrollTop <= list.maxScroll(), `scrollTop <= maxScroll step=${step}`);
    const anchor = list.getAnchor();
    if (anchor) assert.ok(anchor.offset >= 0, `anchor offset >= 0 step=${step}`);
  }
});
