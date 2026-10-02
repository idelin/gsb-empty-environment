import { test } from "node:test";
import assert from "node:assert/strict";
import { CausalBroadcast, type Message } from "../src/causalBroadcast.ts";

/** 收集节点交付序列的辅助函数 */
function collect(node: CausalBroadcast): Message[] {
  const delivered: Message[] = [];
  node.onDeliver((m) => delivered.push(m));
  return delivered;
}

test("验收边界：M1 先于 M0 到达时被隔离缓存，M0 到达后自动级联交付", () => {
  const a = new CausalBroadcast("A");
  const b = new CausalBroadcast("B");
  const deliveredAtB = collect(b);

  // A 连续广播：M1 因果依赖 M0
  const m0 = a.broadcast("M0");
  const m1 = a.broadcast("M1");

  // 网络抖动：M1 先到达 B
  b.receive(m1);
  assert.equal(b.bufferedCount, 1, "M1 应被暂存在隔离队列");
  assert.deepEqual(deliveredAtB, [], "依赖未满足时不得交付 M1");

  // M0 随后到达：先交付 M0，再级联交付 M1
  b.receive(m0);
  assert.equal(b.bufferedCount, 0, "隔离队列应被清空");
  assert.deepEqual(
    deliveredAtB.map((m) => m.id),
    ["A#1", "A#2"],
    "交付顺序必须是 M0 -> M1，杜绝因果倒置",
  );
  assert.deepEqual(
    deliveredAtB.map((m) => m.payload),
    ["M0", "M1"],
  );
});

test("顺序到达的消息立即交付，不进入隔离队列", () => {
  const a = new CausalBroadcast("A");
  const b = new CausalBroadcast("B");
  const deliveredAtB = collect(b);

  b.receive(a.broadcast("x"));
  b.receive(a.broadcast("y"));

  assert.equal(b.bufferedCount, 0);
  assert.deepEqual(deliveredAtB.map((m) => m.payload), ["x", "y"]);
});

test("无因果关系的并发消息可立即交付", () => {
  const a = new CausalBroadcast("A");
  const c = new CausalBroadcast("C");
  const b = new CausalBroadcast("B");
  const deliveredAtB = collect(b);

  // A 和 C 各自独立广播（互不依赖）
  const fromA = a.broadcast("from A");
  const fromC = c.broadcast("from C");

  b.receive(fromC);
  b.receive(fromA);

  assert.equal(b.bufferedCount, 0, "并发消息不应被缓存");
  assert.deepEqual(deliveredAtB.map((m) => m.id), ["C#1", "A#1"]);
});

test("跨节点因果依赖：B 的消息依赖 A 的消息，乱序时同样被缓存", () => {
  const a = new CausalBroadcast("A");
  const b = new CausalBroadcast("B");
  const c = new CausalBroadcast("C");
  const deliveredAtC = collect(c);

  // A 广播 m0；B 收到后基于它广播 n1（n1 因果依赖 m0）
  const m0 = a.broadcast("A 的消息");
  b.receive(m0);
  const n1 = b.broadcast("B 基于 A 的回复");

  // C 先收到 n1，后收到 m0
  c.receive(n1);
  assert.equal(c.bufferedCount, 1);
  assert.deepEqual(deliveredAtC, []);

  c.receive(m0);
  assert.equal(c.bufferedCount, 0);
  assert.deepEqual(deliveredAtC.map((m) => m.id), ["A#1", "B#1"]);
});

test("长依赖链整体逆序到达时按因果序级联交付", () => {
  const a = new CausalBroadcast("A");
  const b = new CausalBroadcast("B");
  const deliveredAtB = collect(b);

  const chain = [
    a.broadcast("第1条"),
    a.broadcast("第2条"),
    a.broadcast("第3条"),
    a.broadcast("第4条"),
  ];

  // 完全逆序到达
  for (const m of [...chain].reverse()) {
    b.receive(m);
  }
  assert.equal(b.bufferedCount, 0);
  assert.deepEqual(
    deliveredAtB.map((m) => m.id),
    ["A#1", "A#2", "A#3", "A#4"],
  );
});

test("重复投递的消息被去重，只交付一次", () => {
  const a = new CausalBroadcast("A");
  const b = new CausalBroadcast("B");
  const deliveredAtB = collect(b);

  const m0 = a.broadcast("唯一消息");
  b.receive(m0);
  b.receive(m0);
  b.receive(m0);

  assert.equal(deliveredAtB.length, 1);
  assert.equal(b.bufferedCount, 0);
});

test("发送端本地广播立即交付给本地应用层", () => {
  const a = new CausalBroadcast("A");
  const deliveredAtA = collect(a);

  a.broadcast("本地消息");

  assert.deepEqual(deliveredAtA.map((m) => m.payload), ["本地消息"]);
  assert.deepEqual(a.currentClock, { A: 1 });
});
