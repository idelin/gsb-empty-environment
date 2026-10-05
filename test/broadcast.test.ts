import test from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";
import { idKey } from "../src/types.ts";
import type { Message, PrefixTable } from "../src/types.ts";

const msg = (
  node: string,
  epoch: number,
  counter: number,
  deps: PrefixTable,
  payload: string,
): Message => ({ id: { node, epoch, counter }, deps, payload });

const recv = (c: Cluster, at: string, m: Message) =>
  c.apply({ kind: "recv", at, msg: m });

test("因果依赖未满足时进入暂存, 依赖补齐后按序排空", () => {
  const c = new Cluster(["A", "B", "R"], 8);
  const a1 = msg("A", 1, 1, {}, "a1");
  const a2 = msg("A", 1, 2, { "A@1": 1 }, "a2");
  const b1 = msg("B", 1, 1, { "A@1": 2 }, "b1");

  assert.equal(recv(c, "R", b1)?.status, "staged");
  assert.equal(recv(c, "R", a2)?.status, "staged");
  const res = recv(c, "R", a1);
  assert.equal(res?.status, "delivered");
  assert.deepEqual(
    c.deliveries().map((d) => idKey(d.id)),
    ["A@1#1", "A@1#2", "B@1#1"],
  );
  assert.deepEqual(c.pending(), []);
});

test("高序号不能自动填缺口, 只能等连续前驱", () => {
  const c = new Cluster(["A", "R"], 8);
  const a1 = msg("A", 1, 1, {}, "a1");
  const a2 = msg("A", 1, 2, { "A@1": 1 }, "a2");
  const a3 = msg("A", 1, 3, { "A@1": 2 }, "a3");

  const r3 = recv(c, "R", a3);
  assert.equal(r3?.status, "staged");
  assert.deepEqual(r3?.missing, ["A@1#1", "A@1<=2"]); // 缺直接前驱 + 未满足的携带依赖
  assert.equal(recv(c, "R", a1)?.status, "delivered");
  // 缺口仍在 (缺 #2), a3 不得被推进
  assert.deepEqual(c.deliveries().map((d) => idKey(d.id)), ["A@1#1"]);
  assert.deepEqual(c.pending().map((p) => idKey(p.id)), ["A@1#3"]);
  assert.equal(recv(c, "R", a2)?.status, "delivered");
  assert.deepEqual(
    c.deliveries().map((d) => idKey(d.id)),
    ["A@1#1", "A@1#2", "A@1#3"],
  );
});

test("重复消息幂等; 同身份不同内容报错", () => {
  const c = new Cluster(["A", "R"], 8);
  const a1 = msg("A", 1, 1, {}, "a1");
  assert.equal(recv(c, "R", a1)?.status, "delivered");
  assert.equal(recv(c, "R", a1)?.status, "duplicate");
  assert.equal(c.deliveries().length, 1);

  const evil = msg("A", 1, 1, {}, "tampered");
  assert.equal(recv(c, "R", evil)?.status, "conflict");

  // 暂存中的消息同样幂等且检测冲突
  const a3 = msg("A", 1, 3, { "A@1": 2 }, "a3");
  assert.equal(recv(c, "R", a3)?.status, "staged");
  assert.equal(recv(c, "R", a3)?.status, "duplicate");
  assert.equal(recv(c, "R", msg("A", 1, 3, { "A@1": 2 }, "evil"))?.status, "conflict");
  assert.equal(c.resources().staged, 1);
});

test("有界暂存: 超额明确背压, 补洞后重试成功", () => {
  const c = new Cluster(["A", "R"], 2);
  const a1 = msg("A", 1, 1, {}, "a1");
  const a2 = msg("A", 1, 2, { "A@1": 1 }, "a2");
  const a3 = msg("A", 1, 3, { "A@1": 2 }, "a3");
  const a4 = msg("A", 1, 4, { "A@1": 3 }, "a4");

  assert.equal(recv(c, "R", a3)?.status, "staged");
  assert.equal(recv(c, "R", a4)?.status, "staged");
  const bp = recv(c, "R", a2); // a2 缺前驱且暂存已满
  assert.equal(bp?.status, "backpressure");
  assert.equal(c.backpressureLog.length, 1);
  assert.equal(c.resources().staged, 2);

  assert.equal(recv(c, "R", a1)?.status, "delivered");
  assert.equal(recv(c, "R", a2)?.status, "delivered"); // 重试, 连带排空 a3 a4
  assert.deepEqual(
    c.deliveries().map((d) => idKey(d.id)),
    ["A@1#1", "A@1#2", "A@1#3", "A@1#4"],
  );
});

test("成员重启使用新 epoch, 计数器从 1 重新开始", () => {
  const c = new Cluster(["A", "B"], 8);
  const a1 = c.node("A").publish("a1");
  const a2 = c.node("A").publish("a2");
  assert.deepEqual(a1.id, { node: "A", epoch: 1, counter: 1 });
  assert.deepEqual(a2.id, { node: "A", epoch: 1, counter: 2 });

  c.apply({ kind: "epochSwitch", node: "A", oldEpoch: 1, newEpoch: 2, finalCounter: 2 });
  const a3 = c.node("A").publish("a3");
  assert.deepEqual(a3.id, { node: "A", epoch: 2, counter: 1 });
  assert.equal(a3.deps["A@1"], 2); // 依赖携带旧 epoch 的完整前缀

  // 旧 epoch 迟到消息在 B 处仍可交付
  assert.equal(recv(c, "B", a2)?.status, "staged");
  assert.equal(recv(c, "B", a1)?.status, "delivered");
  assert.deepEqual(
    c.deliveries().filter((d) => d.at === "B").map((d) => idKey(d.id)),
    ["A@1#1", "A@1#2"],
  );
});

test("已退休 epoch 上超出 finalCounter 的消息被拒绝", () => {
  const c = new Cluster(["A", "B"], 8);
  c.node("A").publish("a1");
  c.node("A").publish("a2");
  c.apply({ kind: "epochSwitch", node: "A", oldEpoch: 1, newEpoch: 2, finalCounter: 2 });
  const bogus = msg("A", 1, 3, { "A@1": 2 }, "bogus");
  assert.equal(recv(c, "B", bogus)?.status, "sealed");
});

test("退休屏障需要所有当前成员确认", () => {
  const c = new Cluster(["A", "B", "R"], 8);
  c.node("A").publish("a1");
  c.node("A").publish("a2");
  c.apply({ kind: "epochSwitch", node: "A", oldEpoch: 1, newEpoch: 2, finalCounter: 2 });

  assert.equal(c.retirementStatus()[0].complete, false);
  c.apply({ kind: "ack", from: "B", prefix: { "A@1": 2 } });
  assert.equal(c.retirementStatus()[0].complete, false); // R 未确认
  c.apply({ kind: "ack", from: "R", prefix: { "A@1": 2 } });
  assert.equal(c.retirementStatus()[0].complete, true);
});

test("共同稳定点取全员最小值, 不用单节点最大计数", () => {
  const c = new Cluster(["A", "B", "R"], 8);
  const a1 = c.node("A").publish("a1");
  const a2 = c.node("A").publish("a2");
  for (const at of ["B", "R"]) {
    recv(c, at, a1);
    recv(c, at, a2);
  }
  assert.equal(c.resources().dedup, 6); // 3 节点 x 2 条

  c.apply({ kind: "ack", from: "A", prefix: { "A@1": 2 } });
  c.apply({ kind: "ack", from: "B", prefix: { "A@1": 2 } });
  c.apply({ kind: "ack", from: "R", prefix: { "A@1": 1 } }); // R 落后
  c.apply({ kind: "checkpoint" });

  assert.equal(c.stablePrefix()["A@1"], 1); // min, 不是 max
  assert.deepEqual(
    c.reclaimed().filter((r) => r.kind === "dedup"),
    [{ kind: "dedup", prefix: "A@1", from: 1, to: 1 }],
  );
  assert.equal(c.resources().dedup, 3); // 每节点只剩 A@1#2

  c.apply({ kind: "ack", from: "R", prefix: { "A@1": 2 } });
  c.apply({ kind: "checkpoint" });
  assert.equal(c.resources().dedup, 0);
  assert.deepEqual(
    c.reclaimed().filter((r) => r.kind === "dedup").at(-1),
    { kind: "dedup", prefix: "A@1", from: 2, to: 2 },
  );
});

test("墓碑在屏障完成且稳定前缀覆盖后才回收; 回收后行为一致", () => {
  const c = new Cluster(["A", "B"], 8);
  const a1 = c.node("A").publish("a1");
  const a2 = c.node("A").publish("a2");
  recv(c, "B", a1);
  recv(c, "B", a2);
  c.apply({ kind: "epochSwitch", node: "A", oldEpoch: 1, newEpoch: 2, finalCounter: 2 });
  const a3 = c.node("A").publish("a3");
  recv(c, "B", a3);

  assert.equal(c.resources().tombstones, 1);
  c.apply({ kind: "ack", from: "A", prefix: { "A@1": 2, "A@2": 1 } });
  c.apply({ kind: "ack", from: "B", prefix: { "A@1": 2, "A@2": 1 } });
  c.apply({ kind: "checkpoint" });

  assert.equal(c.resources().tombstones, 0);
  assert.ok(
    c.reclaimed().some((r) => r.kind === "tombstone" && r.prefix === "A@1" && r.to === 2),
  );
  assert.equal(c.resources().dedup, 0);

  // 恢复后: dedup 已回收, 迟到重复仍靠前缀幂等; 越界旧纪元仍被拒
  assert.equal(recv(c, "B", a1)?.status, "duplicate");
  assert.equal(recv(c, "B", msg("A", 1, 3, { "A@1": 2 }, "x"))?.status, "sealed");
  // 新 epoch 继续正常编号
  const a4 = c.node("A").publish("a4");
  assert.deepEqual(a4.id, { node: "A", epoch: 2, counter: 2 });
});

test("待定依赖输出缺失明细", () => {
  const c = new Cluster(["A", "B", "R"], 8);
  const b1 = msg("B", 1, 1, { "A@1": 2 }, "b1");
  recv(c, "R", b1);
  assert.deepEqual(c.pending(), [
    { at: "R", id: { node: "B", epoch: 1, counter: 1 }, missing: ["A@1<=2"] },
  ]);
});
