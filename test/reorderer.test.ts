import { test } from "node:test";
import assert from "node:assert/strict";
import { solve, validate, type Problem, type SolveResult } from "../src/reorderer.ts";

function unsat(result: SolveResult) {
  assert.equal(result.status, "unsat");
  return result as Extract<SolveResult, { status: "unsat" }>;
}

test("无约束时按字典序输出", () => {
  const r = solve({
    events: [
      { id: "C", object: "o1", op: "update" },
      { id: "A", object: "o2", op: "update" },
      { id: "B", object: "o3", op: "update" },
    ],
  });
  assert.deepEqual(r, { status: "ok", order: ["A", "B", "C"] });
});

test("先后约束影响字典序", () => {
  const r = solve({
    events: [
      { id: "A", object: "o1", op: "update" },
      { id: "B", object: "o2", op: "update" },
      { id: "C", object: "o3", op: "update" },
    ],
    precedence: [["B", "A"]],
  });
  assert.deepEqual(r, { status: "ok", order: ["B", "A", "C"] });
});

test("状态约束融入构造：delete 不能抢在 update 前", () => {
  // 字典序 A(create) < B(delete) < C(update)，但 delete 后对象已死，
  // update 必须排在 delete 之前。
  const r = solve({
    events: [
      { id: "A", object: "o1", op: "create" },
      { id: "B", object: "o1", op: "delete" },
      { id: "C", object: "o1", op: "update" },
    ],
  });
  assert.deepEqual(r, { status: "ok", order: ["A", "C", "B"] });
});

test("update 不能排在 create 前（状态约束不是事后检查）", () => {
  const r = solve({
    events: [
      { id: "A", object: "o1", op: "update" },
      { id: "B", object: "o1", op: "create" },
    ],
  });
  assert.deepEqual(r, { status: "ok", order: ["B", "A"] });
});

test("预言机避免局部合法的死路：跳过更小的 delete", () => {
  // o1 初始存活（无 create）。A=delete 字典序更小且局部合法，
  // 但放下 A 后 B(update) 必死，因此正确解是 [B, A]。
  const r = solve({
    events: [
      { id: "A", object: "o1", op: "delete" },
      { id: "B", object: "o1", op: "update" },
    ],
  });
  assert.deepEqual(r, { status: "ok", order: ["B", "A"] });
});

test("无解：先后约束强制 delete 在 update 前，输出阻塞前缀与约束", () => {
  const r = unsat(
    solve({
      events: [
        { id: "1", object: "o1", op: "delete" },
        { id: "2", object: "o1", op: "update" },
      ],
      precedence: [["1", "2"]],
    }),
  );
  assert.deepEqual(r.blockingPrefix, ["1"]);
  const b2 = r.blocked.find((b) => b.event === "2")!;
  assert.ok(b2.reasons.some((s) => s.includes("already deleted")), b2.reasons.join("; "));
});

test("无解：create 被排在 delete 之后，空前缀即阻塞", () => {
  const r = unsat(
    solve({
      events: [
        { id: "A", object: "o1", op: "create" },
        { id: "B", object: "o1", op: "delete" },
      ],
      precedence: [["B", "A"]],
    }),
  );
  assert.deepEqual(r.blockingPrefix, []);
  const bA = r.blocked.find((b) => b.event === "A")!;
  const bB = r.blocked.find((b) => b.event === "B")!;
  assert.ok(bA.reasons.some((s) => s.startsWith("precedence:")), bA.reasons.join("; "));
  assert.ok(bB.reasons.some((s) => s.includes("not created yet")), bB.reasons.join("; "));
});

test("无解：先后约束成环，报告环", () => {
  const r = unsat(
    solve({
      events: [
        { id: "A", object: "o1", op: "update" },
        { id: "B", object: "o2", op: "update" },
      ],
      precedence: [
        ["A", "B"],
        ["B", "A"],
      ],
    }),
  );
  assert.deepEqual(r.blockingPrefix, []);
  assert.ok(r.cycle !== null && r.cycle.length >= 2);
});

test("无解：阻塞前缀取字典序最小的极大局部合法前缀", () => {
  // o2 没有 create（初始存活）；约束 D(delete o2) 在 C(update o2) 前，
  // 但 D 放下后 o2 已删除，C 永远无法合法 => 无解。
  const problem: Problem = {
    events: [
      { id: "A", object: "o1", op: "create" },
      { id: "B", object: "o1", op: "delete" },
      { id: "C", object: "o2", op: "update" },
      { id: "D", object: "o2", op: "delete" },
    ],
    precedence: [["D", "C"], ["A", "B"]],
  };
  const r = unsat(solve(problem));
  // 局部贪心路径：A -> B -> D，然后 C 被“已删除”阻塞
  assert.deepEqual(r.blockingPrefix, ["A", "B", "D"]);
  const bC = r.blocked.find((b) => b.event === "C")!;
  assert.ok(bC.reasons.some((s) => s.includes("already deleted")), bC.reasons.join("; "));
});

test("22 个事件的链式约束", () => {
  const events = Array.from({ length: 22 }, (_, i) => ({
    id: `e${String(i).padStart(2, "0")}`,
    object: `o${i}`,
    op: "update" as const,
  }));
  // 逆序链：e21 < e20 < ... < e00，强制唯一全序
  const precedence: Array<[string, string]> = [];
  for (let i = 21; i > 0; i--) {
    precedence.push([`e${String(i).padStart(2, "0")}`, `e${String(i - 1).padStart(2, "0")}`]);
  }
  const r = solve({ events, precedence });
  assert.equal(r.status, "ok");
  const order = (r as Extract<SolveResult, { status: "ok" }>).order;
  // 链式约束强制唯一全序：e21 -> e20 -> ... -> e00
  assert.deepEqual(
    order,
    Array.from({ length: 22 }, (_, i) => `e${String(21 - i).padStart(2, "0")}`),
  );
});

test("22 个事件无约束", () => {
  const events = Array.from({ length: 22 }, (_, i) => ({
    id: `e${String(21 - i).padStart(2, "0")}`,
    object: `o${i}`,
    op: "update" as const,
  }));
  const r = solve({ events });
  assert.equal(r.status, "ok");
});

test("输入校验", () => {
  assert.ok(validate({ events: [] }).length === 0);
  assert.ok(
    validate({
      events: Array.from({ length: 23 }, (_, i) => ({ id: `e${i}`, object: "o", op: "update" as const })),
    }).some((s) => s.includes("too many")),
  );
  assert.ok(
    validate({
      events: [
        { id: "A", object: "o", op: "create" },
        { id: "A", object: "o", op: "update" },
      ],
    }).some((s) => s.includes("duplicate")),
  );
  assert.ok(
    validate({
      events: [
        { id: "A", object: "o", op: "create" },
        { id: "B", object: "o", op: "create" },
      ],
    }).some((s) => s.includes("multiple create")),
  );
  assert.ok(
    validate({
      events: [{ id: "A", object: "o", op: "update" }],
      precedence: [["A", "Z"]],
    }).some((s) => s.includes("unknown event")),
  );
});
