import { test } from "node:test";
import assert from "node:assert/strict";
import { WaitlistEngine, type ChainEvent } from "../src/waitlist-engine.ts";

function promotes(events: readonly ChainEvent[]) {
  return events.filter((e) => e.kind === "promote");
}

test("候补队列按优先级降序、同优先级按报名序号升序", () => {
  const engine = new WaitlistEngine();
  engine.addSession("S", 0);
  engine.register({ id: "low", priority: 1, preferences: ["S"] });
  engine.register({ id: "mid1", priority: 5, preferences: ["S"] });
  engine.register({ id: "high", priority: 10, preferences: ["S"] });
  engine.register({ id: "mid2", priority: 5, preferences: ["S"] }); // 同优先级，序号靠后
  assert.deepEqual(engine.waitlistOf("S"), ["high", "mid1", "mid2", "low"]);
});

test("首次结算按队列顺序占满名额，一人最多持有一场", () => {
  const engine = new WaitlistEngine();
  engine.addSession("X", 1);
  engine.addSession("Y", 1);
  engine.register({ id: "p1", priority: 10, preferences: ["X", "Y"] });
  engine.register({ id: "p2", priority: 9, preferences: ["X", "Y"] });
  engine.register({ id: "p3", priority: 8, preferences: ["Y"] });
  engine.settle();
  // p1 拿最偏好的 X，p2 只能拿 Y，p3 候补
  assert.equal(engine.assignmentOf("p1"), "X");
  assert.equal(engine.assignmentOf("p2"), "Y");
  assert.equal(engine.assignmentOf("p3"), undefined);
  // p1 的条目仍留在 Y 队列中（已持有更偏好的 X，永远不会接受 Y），但不影响 p3 被补上的资格
  assert.deepEqual(engine.waitlistOf("Y"), ["p1", "p3"]);
});

test("与场次注册顺序无关：先扫到次偏好也会最终收敛到最偏好", () => {
  const engine = new WaitlistEngine();
  engine.addSession("Y", 1); // 先注册 Y，结算时先扫描 Y
  engine.addSession("X", 1);
  engine.register({ id: "p1", priority: 10, preferences: ["X", "Y"] });
  engine.register({ id: "p2", priority: 9, preferences: ["Y"] });
  engine.settle();
  assert.equal(engine.assignmentOf("p1"), "X"); // p1 最终升入更偏好的 X
  assert.equal(engine.assignmentOf("p2"), "Y"); // p1 释放的 Y 由 p2 补上
});

test("撤销一个名额触发级联递补，直到稳定", () => {
  const engine = new WaitlistEngine();
  engine.addSession("A", 1);
  engine.addSession("B", 1);
  engine.addSession("C", 1);
  engine.register({ id: "p1", priority: 100, preferences: ["A"] });
  engine.register({ id: "p2", priority: 90, preferences: ["A", "B"] });
  engine.register({ id: "p3", priority: 80, preferences: ["B", "C"] });
  engine.register({ id: "p4", priority: 70, preferences: ["B"] });
  engine.register({ id: "p5", priority: 60, preferences: ["C"] });
  engine.settle();
  assert.equal(engine.assignmentOf("p1"), "A");
  assert.equal(engine.assignmentOf("p2"), "B");
  assert.equal(engine.assignmentOf("p3"), "C");

  const events = engine.revoke("p1");
  const chain = promotes(events).map((e) => `${e.personId}:${e.fromSessionId ?? "-"}->${e.toSessionId}`);
  assert.deepEqual(chain, ["p2:B->A", "p3:C->B", "p5:-->C"]);
  assert.equal(engine.assignmentOf("p2"), "A");
  assert.equal(engine.assignmentOf("p3"), "B");
  assert.equal(engine.assignmentOf("p5"), "C");
  assert.equal(engine.assignmentOf("p4"), undefined);

  // 完整递补链包含撤销事件且步数连续
  const full = engine.getChain();
  assert.equal(full[full.length - 4].kind, "revoke");
  assert.deepEqual(full.map((e) => e.step), full.map((_, i) => i + 1));
});

test("只接受比当前更偏好的场次，防止循环", () => {
  const engine = new WaitlistEngine();
  engine.addSession("X", 1);
  engine.addSession("Y", 1);
  engine.register({ id: "p1", priority: 90, preferences: ["X", "Y"] });
  engine.register({ id: "p2", priority: 90, preferences: ["Y", "X"] }); // 偏好顺序相反
  engine.settle();
  assert.equal(engine.assignmentOf("p1"), "X");
  assert.equal(engine.assignmentOf("p2"), "Y");

  // p1 放弃 X：X 空出，但 p2 已持有更偏好的 Y，必须拒绝 X，名额空置，系统稳定
  const events = engine.revoke("p1");
  assert.equal(promotes(events).length, 0);
  assert.equal(engine.assignmentOf("p2"), "Y");
  assert.deepEqual(engine.occupantsOf("X"), []);
});

test("撤销的场次不会再补给本人（防回环）", () => {
  const engine = new WaitlistEngine();
  engine.addSession("X", 1);
  engine.register({ id: "p1", priority: 10, preferences: ["X"] });
  engine.settle();
  const events = engine.revoke("p1");
  // X 空出后，p1 已拒绝 X，不会被补回，名额保持空置
  assert.equal(promotes(events).length, 0);
  assert.equal(engine.assignmentOf("p1"), undefined);
  assert.deepEqual(engine.occupantsOf("X"), []);
});

test("撤销确认后按原报名序号恢复候补位置，而非重新排尾", () => {
  const engine = new WaitlistEngine();
  engine.addSession("X", 1);
  engine.addSession("Y", 1);
  engine.register({ id: "q1", priority: 100, preferences: ["X"] });
  engine.register({ id: "q2", priority: 90, preferences: ["X", "Y"] });
  engine.register({ id: "q3", priority: 90, preferences: ["Y"] });
  engine.register({ id: "q4", priority: 90, preferences: ["X"] }); // 与 q2 同优先级但序号靠后
  engine.settle();
  assert.equal(engine.assignmentOf("q1"), "X");
  assert.equal(engine.assignmentOf("q2"), "Y");

  // q2 撤销 Y：恢复 X 的候补资格，应排在同优先级但序号更靠后的 q4 之前
  engine.revoke("q2");
  assert.deepEqual(engine.waitlistOf("X"), ["q2", "q4"]);
  assert.equal(engine.assignmentOf("q3"), "Y"); // q2 释放的 Y 由 q3 补上

  // q1 再撤销 X：空额归恢复原位的 q2，而非 q4
  engine.revoke("q1");
  assert.equal(engine.assignmentOf("q2"), "X");
  assert.equal(engine.assignmentOf("q4"), undefined);
});

test("递补链输出包含来源、去向与释放信息", () => {
  const engine = new WaitlistEngine();
  engine.addSession("A", 1);
  engine.addSession("B", 1);
  engine.register({ id: "p1", priority: 10, preferences: ["A"] });
  engine.register({ id: "p2", priority: 9, preferences: ["A", "B"] });
  engine.settle();
  engine.revoke("p1");
  const lines = engine.formatChain();
  assert.ok(lines.some((l) => l.includes("递补 p2") && l.includes("B -> A") && l.includes("释放 B")));
  assert.ok(lines.some((l) => l.includes("撤销 p1")));
});
