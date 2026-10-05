import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Store,
  CapacityExceededError,
  TransactionClosedError,
  VersionUnavailableError,
  type JsonObject,
} from "../src/store.ts";
import { ReferenceModel } from "./harness.ts";

const initial = () => ({
  cart: { items: [{ id: "a", qty: 1 }] },
  inventory: { a: 5 },
});

test("两分片在同一事务中原子提交，订阅者看到完整一致状态", () => {
  const store = new Store(initial());
  const ref = new ReferenceModel(initial());
  const seen: Array<{ version: number; state: JsonObject }> = [];
  store.subscribe((state, version) => seen.push({ version, state }));

  // 购物车数量与库存占用互相依赖，必须落在同一个版本里。
  const tx = store.begin();
  tx.set(["cart", "items", 0, "qty"], 3);
  tx.set(["inventory", "a"], 2);
  const version = tx.commit();

  ref.begin("t");
  ref.set("t", ["cart", "items", 0, "qty"], 3);
  ref.set("t", ["inventory", "a"], 2);
  assert.equal(version, ref.commit("t"));

  // 两次 set 之间没有任何通知；订阅者只收到一次、且两个分片同时到位。
  assert.equal(seen.length, 1);
  assert.equal(seen[0].version, version);
  assert.deepEqual(seen[0].state, ref.headState);
  assert.deepEqual(store.state, ref.headState);
  assert.equal(store.state.cart !== store.checkout(1).state.cart, true); // 新节点
  assert.equal(store.state.inventory !== store.checkout(1).state.inventory, true);
});

test("通知期间发起的重入事务排到本轮通知之后", () => {
  const store = new Store(initial());
  const events: string[] = [];
  const states = new Map<string, JsonObject>();
  let recommitted = false;

  store.subscribe((state, version) => {
    events.push(`A:v${version}`);
    states.set(`A:v${version}`, state);
    if (!recommitted) {
      recommitted = true;
      const tx = store.begin();
      tx.set(["cart", "stamp"], "reentrant");
      tx.commit();
    }
  });
  store.subscribe((state, version) => {
    events.push(`B:v${version}`);
    states.set(`B:v${version}`, state);
  });

  const tx = store.begin();
  tx.set(["cart", "items"], []);
  tx.commit(); // v2；A 的回调里重入提交出 v3

  // 严格轮次：v2 轮（A、B）全部结束后才进入 v3 轮。
  assert.deepEqual(events, ["A:v2", "B:v2", "A:v3", "B:v3"]);
  // 每个订阅者每个版本恰好收到一次通知，且看到该版本完整状态。
  assert.deepEqual(states.get("A:v2"), states.get("B:v2"));
  assert.equal((states.get("A:v2")!.cart as any).stamp, undefined);
  assert.equal((states.get("A:v3")!.cart as any).stamp, "reentrant");
  assert.deepEqual(states.get("A:v3"), store.state);
});

test("订阅回调抛错不阻止其他订阅者，错误被收集", () => {
  const store = new Store(initial());
  const received: number[] = [];
  store.subscribe(() => {
    throw new Error("boom");
  });
  store.subscribe((_state, version) => received.push(version));

  const tx = store.begin();
  tx.set(["inventory", "a"], 4);
  tx.commit();

  assert.deepEqual(received, [2]);
  assert.equal(store.listenerErrors.length, 1);
  assert.equal((store.listenerErrors[0] as Error).message, "boom");
});

test("回调中取消订阅：自身与他人都不再收到后续通知", () => {
  const store = new Store(initial());
  const events: string[] = [];
  const offA = store.subscribe((_s, v) => {
    events.push(`A:v${v}`);
    offA(); // 取消自己
    offB(); // 取消排在自己后面的 B
  });
  const offB = store.subscribe((_s, v) => events.push(`B:v${v}`));

  let tx = store.begin();
  tx.set(["inventory", "a"], 3);
  tx.commit(); // v2：A 收到并当场取消 B，B 本轮即不再被调用
  assert.deepEqual(events, ["A:v2"]);

  tx = store.begin();
  tx.set(["inventory", "a"], 1);
  tx.commit(); // v3：无人收到
  assert.deepEqual(events, ["A:v2"]);
});

test("checkout 旧版本后开分支提交：既有历史不被覆盖", () => {
  const store = new Store(initial());
  const ref = new ReferenceModel(initial());

  let tx = store.begin();
  tx.set(["inventory", "a"], 99);
  const v2 = tx.commit();
  ref.begin("m");
  ref.set("m", ["inventory", "a"], 99);
  assert.equal(v2, ref.commit("m"));

  // 从 v1 检出并开分支：购物车回到旧版时，库存也一起回到旧版，无混合画面。
  const snap = store.checkout(1);
  const branch = snap.begin();
  branch.set(["cart", "items"], [{ id: "b", qty: 7 }]);
  const v3 = branch.commit();
  ref.begin("b", 1);
  ref.set("b", ["cart", "items"], [{ id: "b", qty: 7 }]);
  assert.equal(v3, ref.commit("b"));

  assert.equal(store.parentOf(v3), 1); // 分支父版本是 v1，不是 v2
  assert.equal(store.version, v3);
  assert.deepEqual(store.state, ref.headState);
  assert.equal((store.state.inventory as any).a, 5); // 分支基于旧版，v2 的改动不在其中

  // 既有历史原样保留：v1、v2 都可检出且与参考模型一致。
  assert.deepEqual(store.checkout(1).state, ref.stateAt(1));
  assert.deepEqual(store.checkout(v2).state, ref.stateAt(v2));
  assert.deepEqual(store.ledger.versions, [1, 2, 3]);
  snap.release();
});

test("容量上限按保留节点计费：快照固定不可回收，释放后可回收", () => {
  // 手工核对的账本（只计对象/数组节点）：
  // v1: root+cart+items+item+inventory = 5
  const store = new Store(initial(), { capacity: 12 });
  assert.equal(store.ledger.retainedNodes, 5);

  // v2 改购物车：新增 item/items/cart/root 4 个节点，inventory 子树共享。
  let tx = store.begin();
  tx.set(["cart", "items", 0, "qty"], 2);
  tx.commit();
  assert.equal(store.ledger.retainedNodes, 9);

  // v3 改库存：新增 inventory/root 2 个节点，cart 子树与 v2 共享。
  tx = store.begin();
  tx.set(["inventory", "b"], 3);
  tx.commit();
  assert.equal(store.ledger.retainedNodes, 11);

  const snapV1 = store.checkout(1); // 固定 v1

  // v4 改购物车：+3 -> 14 超上限；v1 被快照固定，逐出 v2(-1)、v3(-4) 后回落到 9。
  tx = store.begin();
  tx.set(["cart", "items"], []);
  tx.commit();
  assert.equal(store.ledger.retainedNodes, 9);
  assert.deepEqual(store.ledger.versions, [1, 4]);
  assert.throws(() => store.checkout(2), VersionUnavailableError);
  assert.throws(() => store.checkout(3), VersionUnavailableError);
  assert.deepEqual(snapV1.state, { cart: { items: [{ id: "a", qty: 1 }] }, inventory: { a: 5 } });

  snapV1.release();

  // v5 换购物车：+5 -> 14 超上限；v1 已释放被逐出(-5)，回落到 9。
  tx = store.begin();
  tx.set(["cart"], { items: [{ id: "b", qty: 1 }, { id: "c", qty: 2 }] });
  tx.commit();
  assert.equal(store.ledger.retainedNodes, 9);
  assert.deepEqual(store.ledger.versions, [4, 5]);
  assert.throws(() => store.checkout(1), VersionUnavailableError);

  // 账本与“从全部保留版本可达的节点数”完全一致。
  assert.equal(store.ledger.retainedNodes, store.debugReachableNodes());
});

test("快照钉住的节点把容量占满时，提交事务性失败且不留痕迹", () => {
  const store = new Store(initial(), { capacity: 6 }); // v1 占 5 个节点
  const snap = store.snapshot();
  const tx = store.begin();
  tx.set(["cart", "items", 0, "qty"], 2); // 需要 5(旧) + 4(新) = 9 > 6
  assert.throws(() => tx.commit(), CapacityExceededError);
  assert.equal(store.version, 1); // 头部未动
  assert.equal(store.ledger.retainedNodes, 5); // 账本未动
  assert.deepEqual(store.ledger.versions, [1]);
  assert.throws(() => tx.set(["a"], 1), TransactionClosedError); // 事务已关闭
  snap.release();
});

test("值必须是 JSON 兼容不可变树", () => {
  const store = new Store(initial());
  const bad: unknown[] = [
    undefined,
    NaN,
    Infinity,
    () => 1,
    Symbol("s"),
    10n,
    new Date(),
    [1, , 3], // 空洞
    { a: undefined },
  ];
  for (const value of bad) {
    const tx = store.begin();
    assert.throws(() => tx.set(["x"], value as never), TypeError, String(value));
    tx.abort();
  }
  const cyclic: any = { a: 1 };
  cyclic.self = cyclic;
  const tx = store.begin();
  assert.throws(() => tx.set(["x"], cyclic), TypeError);
  tx.abort();

  // 返回的状态层层冻结，不可被调用方改写。
  const assertFrozen = (node: unknown): void => {
    if (typeof node !== "object" || node === null) return;
    assert.ok(Object.isFrozen(node));
    for (const child of Object.values(node)) assertFrozen(child);
  };
  assertFrozen(store.state);
  assert.throws(() => {
    (store.state.cart as any).items = [];
  }, TypeError);

  // 传入的对象被克隆脱钩：调用方之后修改不影响容器。
  const payload = { items: [{ id: "z", qty: 1 }] };
  const tx2 = store.begin();
  tx2.set(["cart"], payload as never);
  payload.items[0].qty = 999;
  tx2.commit();
  assert.equal((store.state.cart as any).items[0].qty, 1);
});

test("中止事务不产生版本、不通知、不占账本", () => {
  const store = new Store(initial());
  let notified = 0;
  store.subscribe(() => {
    notified += 1;
  });
  const before = store.ledger.retainedNodes;
  const tx = store.begin();
  tx.set(["cart", "items"], [1, 2, 3] as never);
  tx.delete(["inventory", "a"]);
  tx.abort();
  assert.equal(store.version, 1);
  assert.equal(notified, 0);
  assert.equal(store.ledger.retainedNodes, before);
  assert.throws(() => tx.commit(), TransactionClosedError);
});

test("事务内 get 读到工作区（基版本 + 暂存写）", () => {
  const store = new Store(initial());
  const tx = store.begin();
  tx.set(["cart", "coupon"], "SAVE10");
  tx.delete(["inventory", "a"]);
  assert.equal(tx.get(["cart", "coupon"]), "SAVE10");
  assert.equal(tx.get(["inventory", "a"]), undefined);
  assert.equal(tx.get(["cart", "items", 0, "qty"]), 1); // 未触碰的子树来自基版本
  tx.abort();
});
