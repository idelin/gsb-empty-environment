import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { MvccStore } from "../src/index.ts";

/** 构造验收场景：X 有 v1(t=1), v2(t=5), v3(t=10)。 */
function buildStore(): MvccStore<string> {
  const store = new MvccStore<string>();
  store.put("X", "v1", 1);
  store.put("X", "v2", 5);
  store.put("X", "v3", 10);
  return store;
}

describe("版本链组织", () => {
  it("按更新时间倒序链接（新->旧）", () => {
    const store = buildStore();
    assert.deepEqual(store.chainTimestamps("X"), [10, 5, 1]);
    assert.equal(store.chainLength("X"), 3);
  });

  it("乱序写入仍维持倒序", () => {
    const store = new MvccStore<string>();
    store.put("Y", "v3", 10);
    store.put("Y", "v1", 1);
    store.put("Y", "v2", 5);
    assert.deepEqual(store.chainTimestamps("Y"), [10, 5, 1]);
  });

  it("重复时间戳写入报错", () => {
    const store = new MvccStore<string>();
    store.put("Y", "v1", 1);
    assert.throws(() => store.put("Y", "v1b", 1), /duplicate version/);
  });
});

describe("可重复读可见性", () => {
  it("读操作沿链表找到第一个 ts <= readTs 的版本", () => {
    const store = buildStore();
    assert.equal(store.read("X", 1), "v1");
    assert.equal(store.read("X", 4), "v1");
    assert.equal(store.read("X", 5), "v2");
    assert.equal(store.read("X", 8), "v2");
    assert.equal(store.read("X", 10), "v3");
    assert.equal(store.read("X", 99), "v3");
  });

  it("早于所有版本时读不到数据", () => {
    const store = buildStore();
    assert.equal(store.read("X", 0), undefined);
    assert.equal(store.read("missing", 100), undefined);
  });

  it("同一 ReadView 内可重复读结果稳定", () => {
    const store = buildStore();
    const view = store.beginRead(8);
    const first = store.read("X", view.readTs);
    store.put("X", "v4", 20); // 期间有新提交
    const second = store.read("X", view.readTs);
    assert.equal(first, "v2");
    assert.equal(second, "v2");
    store.endRead(view);
  });
});

describe("ReadView 追踪", () => {
  it("全局最小活跃 ReadView 随开启/结束动态变化", () => {
    const store = buildStore();
    assert.equal(store.minActiveReadTs, Infinity);
    const a = store.beginRead(8);
    const b = store.beginRead(3);
    assert.equal(store.minActiveReadTs, 3);
    store.endRead(b);
    assert.equal(store.minActiveReadTs, 8);
    store.endRead(a);
    assert.equal(store.minActiveReadTs, Infinity);
  });

  it("重复结束同一 ReadView 幂等", () => {
    const store = buildStore();
    const view = store.beginRead(8);
    store.endRead(view);
    store.endRead(view);
    assert.equal(store.minActiveReadTs, Infinity);
  });
});

describe("GC 修剪（验收边界）", () => {
  it("最小活跃 ReadView=t=8 时安全修剪 v1，保留 v2/v3", () => {
    const store = buildStore();
    const view = store.beginRead(8); // 当前活跃最小 ReadView 为 t=8

    const pruned = store.gc();

    assert.equal(pruned, 1, "只修剪掉 v1");
    assert.equal(store.prunedCount, 1);
    assert.deepEqual(store.chainTimestamps("X"), [10, 5], "v1 被移除，v2/v3 保留");

    // 所有只读查询依然正确：
    assert.equal(store.read("X", view.readTs), "v2", "活跃读者 t=8 仍读到 v2");
    assert.equal(store.read("X", 5), "v2");
    assert.equal(store.read("X", 7), "v2");
    assert.equal(store.read("X", 10), "v3");
    assert.equal(store.read("X", 100), "v3");
    // t<5 的快照已无人持有（最小活跃为 t=8），v1 不可见是安全且预期的。
    assert.equal(store.read("X", 1), undefined);
    store.endRead(view);
  });

  it("水位线恰好等于版本时间戳时该版本必须保留", () => {
    const store = buildStore();
    const view = store.beginRead(5); // 最小活跃 ReadView 正好等于 v2 的 t=5
    const pruned = store.gc();
    assert.equal(pruned, 1);
    assert.equal(store.read("X", view.readTs), "v2", "t=5 的读者仍读到 v2");
    store.endRead(view);
  });

  it("存在更老的活跃读者时不修剪其可见版本", () => {
    const store = buildStore();
    const old = store.beginRead(3); // 最小活跃 ReadView 为 t=3
    store.beginRead(8);
    const pruned = store.gc();
    assert.equal(pruned, 0, "v1 对 t=3 的读者仍可见，不能修剪");
    assert.deepEqual(store.chainTimestamps("X"), [10, 5, 1]);
    assert.equal(store.read("X", 3), "v1");
    store.endRead(old);
  });

  it("无活跃读者时只保留最新版本", () => {
    const store = buildStore();
    const pruned = store.gc();
    assert.equal(pruned, 2);
    assert.deepEqual(store.chainTimestamps("X"), [10]);
    assert.equal(store.read("X", 100), "v3");
  });

  it("全部版本都新于水位线时不修剪", () => {
    const store = buildStore();
    const view = store.beginRead(0);
    assert.equal(store.gc(), 0);
    assert.deepEqual(store.chainTimestamps("X"), [10, 5, 1]);
    store.endRead(view);
  });

  it("多数据项独立修剪", () => {
    const store = buildStore();
    store.put("Y", "a1", 2);
    store.put("Y", "a2", 6);
    const view = store.beginRead(8);
    const pruned = store.gc();
    assert.equal(pruned, 2, "X 的 v1 与 Y 的 a1 各修剪一个");
    assert.deepEqual(store.chainTimestamps("X"), [10, 5]);
    assert.deepEqual(store.chainTimestamps("Y"), [6]);
    assert.equal(store.read("Y", 8), "a2");
    store.endRead(view);
  });

  it("读者结束后水位线推进，下一轮 GC 继续修剪", () => {
    const store = buildStore();
    const view = store.beginRead(8);
    assert.equal(store.gc(), 1); // 修剪 v1
    store.endRead(view);
    assert.equal(store.gc(), 1); // 无活跃读者，修剪 v2
    assert.deepEqual(store.chainTimestamps("X"), [10]);
    assert.equal(store.prunedCount, 2);
  });
});
