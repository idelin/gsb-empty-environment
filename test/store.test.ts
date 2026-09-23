import { test } from "node:test";
import assert from "node:assert/strict";

import {
  applyPatch,
  AtomicPatchError,
  OptimisticStore,
  replay,
  type StoreState,
} from "../src/store.ts";

test("读者只能看到已确认状态，未确认的乐观写不可见", () => {
  const store = new OptimisticStore({ name: "v0" });
  store.submit("a", 0, [{ op: "set", path: ["name"], value: "v1" }]);

  assert.equal(store.version, 0);
  assert.deepEqual(store.read(), { name: "v0" });
  assert.deepEqual(store.view(), { name: "v1" });
});

test("确认按 操作ID + 登记版本 匹配，成功后版本与状态推进", () => {
  const store = new OptimisticStore({ count: 0 });
  store.submit("a", 0, [{ op: "add", path: ["count"], delta: 1 }]);
  const result = store.confirm("a", 0);

  assert.equal(result.kind, "committed");
  assert.equal(store.version, 1);
  assert.deepEqual(store.read(), { count: 1 });
});

test("A 乐观写、B 再写，A 失败只回滚自身，B 的写入保留", () => {
  const store = new OptimisticStore({ title: "orig", count: 0 });
  store.submit("A", 0, [{ op: "set", path: ["title"], value: "A" }]);
  store.submit("B", 0, [{ op: "set", path: ["count"], value: 42 }]);

  const result = store.reject("A");
  assert.equal(result.kind, "rejected");
  assert.deepEqual(store.read(), { title: "orig", count: 42 });
  assert.deepEqual(store.log().map((e) => e.id), ["B"]);
});

test("同字段叠加写：A 失败后 B 的补丁按当前已确认状态重放（不被 A 覆盖）", () => {
  const store = new OptimisticStore({ title: "orig" });
  store.submit("A", 0, [{ op: "set", path: ["title"], value: "A" }]);
  store.submit("B", 0, [{ op: "set", path: ["title"], value: "B" }]);

  store.reject("A");
  assert.deepEqual(store.read(), { title: "B" });
});

test("计数器多操作：A 失败撤销自己的增量，后续 B/C 增量保留", () => {
  const store = new OptimisticStore({ n: 0 });
  store.submit("A", 0, [{ op: "add", path: ["n"], delta: 5 }]);
  store.submit("B", 0, [{ op: "add", path: ["n"], delta: 10 }]);
  store.submit("C", 0, [{ op: "add", path: ["n"], delta: 20 }]);

  store.reject("A");
  assert.deepEqual(store.read(), { n: 30 });
  assert.deepEqual(store.log().map((e) => e.id), ["B", "C"]);
});

test("重复确认幂等：不重复提交、版本不再增长", () => {
  const store = new OptimisticStore({ n: 0 });
  store.submit("a", 0, [{ op: "add", path: ["n"], delta: 1 }]);

  const first = store.confirm("a", 0);
  const second = store.confirm("a", 0);
  const third = store.confirm("a", 0);

  assert.equal(first.kind, "committed");
  assert.equal(second.kind, "already_committed");
  assert.equal(third.kind, "already_committed");
  assert.equal(store.version, 1);
  assert.equal(store.log().length, 1);
});

test("迟到的旧响应（版本不匹配）被忽略，不回退新状态", () => {
  const store = new OptimisticStore({ n: 0 });
  store.submit("a", 0, [{ op: "add", path: ["n"], delta: 1 }]);
  store.confirm("a", 0);

  const stale = store.confirm("a", 7);
  assert.equal(stale.kind, "conflicted");
  assert.equal(store.version, 1);
  assert.deepEqual(store.read(), { n: 1 });
});

test("未知操作 ID 的确认/拒绝均被忽略", () => {
  const store = new OptimisticStore({ n: 0 });
  assert.equal(store.confirm("ghost", 0).kind, "unknown");
  assert.equal(store.reject("ghost").kind, "unknown");
  assert.equal(store.version, 0);
});

test("补丁原子性：其中一步非法时整体不生效，且不会留下中间态", () => {
  const base: StoreState = { a: 1, nested: { keep: true } };
  const patch = [
    { op: "set" as const, path: ["a"], value: 99 },
    { op: "add" as const, path: ["missing"], delta: 1 },
  ];
  assert.throws(() => applyPatch(base, patch), AtomicPatchError);
  assert.deepEqual(base, { a: 1, nested: { keep: true } });

  const store = new OptimisticStore(base);
  assert.throws(() => store.submit("bad", 0, patch), AtomicPatchError);
  assert.deepEqual(store.read(), base);
  assert.deepEqual(store.pendingIds(), []);
});

test("submit 时非法补丁不会污染乐观视图", () => {
  const store = new OptimisticStore({ n: 1 });
  store.submit("ok", 0, [{ op: "set", path: ["n"], value: 2 }]);
  assert.throws(
    () => store.submit("broken", 0, [{ op: "remove", path: ["nope"] }]),
    AtomicPatchError,
  );
  assert.deepEqual(store.view(), { n: 2 });
  assert.deepEqual(store.pendingIds(), ["ok"]);
});

test("过期的 baseVersion 拒绝登记，防止基于旧快照的写入", () => {
  const store = new OptimisticStore({ n: 0 });
  store.submit("a", 0, [{ op: "set", path: ["n"], value: 1 }]);
  store.confirm("a", 0);

  assert.throws(() => store.submit("late", 0, [{ op: "set", path: ["n"], value: 2 }]), AtomicPatchError);
  assert.doesNotThrow(() => store.submit("fresh", 1, [{ op: "set", path: ["n"], value: 2 }]));
});

test("前序未决时确认返回阻塞者；前序失败后自动推进后续确认的操作", () => {
  const store = new OptimisticStore({ n: 0 });
  store.submit("A", 0, [{ op: "add", path: ["n"], delta: 1 }]);
  store.submit("B", 0, [{ op: "add", path: ["n"], delta: 2 }]);

  const blocked = store.confirm("B", 0);
  assert.equal(blocked.kind, "pending");
  if (blocked.kind === "pending") {
    assert.deepEqual(blocked.blockedBy, ["A"]);
  }
  assert.equal(store.version, 0);

  store.reject("A");
  assert.equal(store.version, 1);
  assert.deepEqual(store.read(), { n: 2 });
  assert.deepEqual(store.log().map((e) => e.id), ["B"]);
});

test("前序成功后，已确认的后续操作按序提交（保持排队顺序）", () => {
  const store = new OptimisticStore({ n: 0 });
  store.submit("A", 0, [{ op: "add", path: ["n"], delta: 1 }]);
  store.submit("B", 0, [{ op: "add", path: ["n"], delta: 2 }]);

  store.confirm("A", 0);
  assert.equal(store.version, 2);
  assert.deepEqual(store.read(), { n: 3 });
});

test("拒绝已确认操作无效；重复拒绝幂等；失败后迟到的成功确认不回退", () => {
  const store = new OptimisticStore({ n: 0 });
  store.submit("A", 0, [{ op: "add", path: ["n"], delta: 1 }]);
  store.confirm("A", 0);
  assert.equal(store.reject("A").kind, "already_committed");
  assert.deepEqual(store.read(), { n: 1 });

  const store2 = new OptimisticStore({ n: 0 });
  store2.submit("X", 0, [{ op: "add", path: ["n"], delta: 1 }]);
  assert.equal(store2.reject("X").kind, "rejected");
  assert.equal(store2.reject("X").kind, "already_rejected");
  const lateAck = store2.confirm("X", 0);
  assert.equal(lateAck.kind, "conflicted");
  assert.deepEqual(store2.read(), { n: 0 });
});

test("状态是已确认日志的纯折叠：replay 与 hydrate 可完整重建", () => {
  const initial: StoreState = { n: 0, label: "s" };
  const store = new OptimisticStore(initial);
  store.submit("a", 0, [{ op: "add", path: ["n"], delta: 3 }]);
  store.confirm("a", 0);
  store.submit("b", 1, [{ op: "set", path: ["label"], value: "t" }]);
  store.confirm("b", 1);

  assert.deepEqual(replay(initial, store.log()), { n: 3, label: "t" });
  const rebuilt = OptimisticStore.hydrate(initial, store.log());
  assert.equal(rebuilt.version, 2);
  assert.deepEqual(rebuilt.read(), { n: 3, label: "t" });
});

test("嵌套结构与数组的原子补丁、深拷贝隔离", () => {
  const store = new OptimisticStore({ items: [{ n: 1 }] } as StoreState);
  store.submit("a", 0, [
    { op: "set", path: ["items", 1], value: { n: 2 } },
    { op: "set", path: ["meta", "deep"], value: true },
  ]);
  store.confirm("a", 0);

  assert.deepEqual(store.read(), { items: [{ n: 1 }, { n: 2 }], meta: { deep: true } });

  // set 的值是深拷贝：外部再改原对象不影响仓库。
  const shared = { hook: true } as unknown as StoreState;
  const store2 = new OptimisticStore({});
  store2.submit("s", 0, [{ op: "set", path: ["ref"], value: shared }]);
  shared.hook = false as never;
  assert.deepEqual(store2.view(), { ref: { hook: true } });
});

test("返回给调用方的状态快照被冻结，无法被外部篡改", () => {
  const store = new OptimisticStore({ nested: { v: 1 } });
  const snap = store.read() as { nested: { v: number } };
  assert.throws(() => {
    snap.nested.v = 999;
  }, TypeError);
});
