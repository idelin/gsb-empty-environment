import { test } from "node:test";
import assert from "node:assert/strict";
import { MvccStore, Version, VersionChain } from "../src/mvcc.ts";

/** 构造验收边界场景：X 有 v1(t=1), v2(t=5), v3(t=10)。 */
function buildStore(): MvccStore {
  const store = new MvccStore();
  store.put("X", 1, "v1");
  store.put("X", 5, "v2");
  store.put("X", 10, "v3");
  return store;
}

test("版本链按更新时间倒序链接", () => {
  const store = buildStore();
  assert.deepEqual(store.chain("X")!.timestamps(), [10, 5, 1]);
});

test("读操作沿链表找到可见版本（可重复读）", () => {
  const store = buildStore();
  assert.equal(store.read("X", 0), undefined); // 早于所有版本
  assert.equal(store.read("X", 1), "v1");
  assert.equal(store.read("X", 4), "v1");
  assert.equal(store.read("X", 5), "v2");
  assert.equal(store.read("X", 9), "v2");
  assert.equal(store.read("X", 10), "v3");
  assert.equal(store.read("X", 99), "v3");
});

test("同一 ReadView 重复读结果稳定（可重复读语义）", () => {
  const store = new MvccStore();
  store.put("X", 1, "v1");
  const rv = store.registry.begin(3); // 快照点 t=3
  store.put("X", 5, "v2"); // 快照之后的新版本
  assert.equal(store.read("X", rv), "v1");
  assert.equal(store.read("X", rv), "v1"); // 再读仍是 v1
  store.registry.end(rv);
});

test("验收场景：minActiveReadView=t=8 时 GC 安全修剪 v1 并释放内存", () => {
  const store = buildStore();
  const before = Version.allocated;

  // 当前活跃最小 ReadView 为 t=8（另有一个更晚的读者 t=12）
  const rv8 = store.registry.begin(8);
  const rv12 = store.registry.begin(12);
  assert.equal(store.registry.minActive, 8);

  const report = store.gc();

  // v1(t=1) 被修剪，v2/v3 保留；内存释放 1 个版本节点
  assert.deepEqual(report.pruned.get("X"), [1]);
  assert.equal(report.freedVersions, 1);
  assert.equal(Version.allocated, before - 1);
  assert.deepEqual(store.chain("X")!.timestamps(), [10, 5]);

  // 所有只读查询依然正确：t=8 读到 v2，t=12 读到 v3
  assert.equal(store.read("X", rv8), "v2");
  assert.equal(store.read("X", rv12), "v3");
  assert.equal(store.read("X", 5), "v2");
  assert.equal(store.read("X", 9), "v2");
  assert.equal(store.read("X", 10), "v3");

  store.registry.end(rv8);
  store.registry.end(rv12);
});

test("GC 不修剪仍对活跃读者可见的旧版本", () => {
  const store = buildStore();
  const rv3 = store.registry.begin(3); // 老读者仍需要 v1
  const rv12 = store.registry.begin(12);

  const report = store.gc();
  assert.equal(report.freedVersions, 0);
  assert.deepEqual(store.chain("X")!.timestamps(), [10, 5, 1]);
  assert.equal(store.read("X", rv3), "v1"); // 老读者不受影响

  // 老读者结束后，最小活跃 ReadView 变为 t=12，v1 与 v2 都成为垃圾
  store.registry.end(rv3);
  const report2 = store.gc();
  assert.deepEqual(report2.pruned.get("X"), [5, 1]);
  assert.deepEqual(store.chain("X")!.timestamps(), [10]);
  assert.equal(store.read("X", rv12), "v3");
  store.registry.end(rv12);
});

test("无活跃读者时 GC 只保留每个 key 的最新版本", () => {
  const store = buildStore();
  store.put("Y", 2, "y1");
  store.put("Y", 7, "y2");
  const report = store.gc();
  assert.equal(report.minVisibleT, Number.POSITIVE_INFINITY);
  assert.deepEqual(report.pruned.get("X"), [5, 1]);
  assert.deepEqual(report.pruned.get("Y"), [2]);
  assert.deepEqual(store.chain("X")!.timestamps(), [10]);
  assert.equal(store.read("X", 100), "v3");
  assert.equal(store.read("Y", 100), "y2");
});

test("所有版本都新于最小 ReadView 时不修剪", () => {
  const chain = new VersionChain<string>();
  chain.put(10, "a");
  chain.put(20, "b");
  assert.deepEqual(chain.prune(5), []);
  assert.deepEqual(chain.timestamps(), [20, 10]);
});

test("重复时间戳写入被拒绝", () => {
  const store = buildStore();
  assert.throws(() => store.put("X", 5, "dup"), /duplicate version timestamp/);
});

test("乱序插入仍保持倒序链表", () => {
  const chain = new VersionChain<string>();
  chain.put(5, "v2");
  chain.put(10, "v3");
  chain.put(1, "v1");
  chain.put(7, "v2.5");
  assert.deepEqual(chain.timestamps(), [10, 7, 5, 1]);
  assert.equal(chain.read(6), "v2");
  assert.equal(chain.read(8), "v2.5");
});

test("ReadView 结束后从注册表移除，重复结束报错", () => {
  const store = new MvccStore();
  const rv = store.registry.begin(8);
  assert.equal(store.registry.activeCount, 1);
  store.registry.end(rv);
  assert.equal(store.registry.activeCount, 0);
  assert.equal(store.registry.minActive, null);
  assert.throws(() => store.registry.end(rv), /not active/);
});
