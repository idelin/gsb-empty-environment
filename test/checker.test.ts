import { describe, expect, test } from "bun:test";
import { checkCompatibility } from "../src/checker";
import { indexProtocol, parseProtocol, ProtocolJson } from "../src/protocol";
import { demoScenarios } from "../src/examples";

const build = (json: ProtocolJson) => indexProtocol(parseProtocol(json));
const scenario = (id: string) => {
  const s = demoScenarios.find((d) => d.id === id);
  if (!s) throw new Error(`missing scenario ${id}`);
  return s;
};

describe("内置演示场景", () => {
  test("兼容的新版客户端可以完成全部会话", () => {
    const s = scenario("compatible");
    const v = checkCompatibility(s.newProtocol, s.oldProtocol, 2);
    expect(v.kind).toBe("compatible");
    if (v.kind === "compatible") {
      expect(v.explored).toBeGreaterThan(0);
      expect(v.terminalCount).toBeGreaterThan(0);
    }
  });

  test("双方都在等待 → 死锁（both-waiting），最短轨迹长度为 0", () => {
    const s = scenario("both-waiting");
    const v = checkCompatibility(s.newProtocol, s.oldProtocol, 2);
    expect(v.kind).toBe("deadlock");
    if (v.kind === "deadlock") {
      expect(v.info.category).toBe("both-waiting");
      expect(v.trace.length).toBe(0);
    }
  });

  test("意外接收：旧版在 Idle 收到 bye，最短轨迹长度为 1", () => {
    const s = scenario("unexpected-reception");
    const v = checkCompatibility(s.newProtocol, s.oldProtocol, 2);
    expect(v.kind).toBe("unexpected-reception");
    if (v.kind === "unexpected-reception") {
      expect(v.party).toBe("B");
      expect(v.message).toBe("bye");
      expect(v.trace.length).toBe(1);
    }
  });

  test("活锁：存在永远无法正常终止的循环", () => {
    const s = scenario("non-terminating");
    const v = checkCompatibility(s.newProtocol, s.oldProtocol, 2);
    expect(v.kind).toBe("non-terminating");
    if (v.kind === "non-terminating") {
      expect(v.cycle).not.toBeNull();
      expect(v.cycle!.length).toBeGreaterThan(0);
    }
  });
});

describe("组合语义", () => {
  const sender3 = build({
    name: "sender",
    initial: "S0",
    finals: ["S3"],
    transitions: [
      { from: "S0", label: "!m1", to: "S1" },
      { from: "S1", label: "!m2", to: "S2" },
      { from: "S2", label: "!m3", to: "S3" },
    ],
  });
  const receiver3 = build({
    name: "receiver",
    initial: "R0",
    finals: ["R3"],
    transitions: [
      { from: "R0", label: "?m1", to: "R1" },
      { from: "R1", label: "?m2", to: "R2" },
      { from: "R2", label: "?m3", to: "R3" },
    ],
  });

  test("容量 2 的流水线：连发三条消息可以正常终止", () => {
    expect(checkCompatibility(sender3, receiver3, 2).kind).toBe("compatible");
  });

  test("容量 1 时同样兼容（只是交错更受限）", () => {
    expect(checkCompatibility(sender3, receiver3, 1).kind).toBe("compatible");
  });

  test("一方已终止、另一方空等 → 死锁（peer-terminated），区别于正常终止", () => {
    const a = build({
      name: "a",
      initial: "A0",
      finals: ["A1"],
      transitions: [{ from: "A0", label: "!x", to: "A1" }],
    });
    const b = build({
      name: "b",
      initial: "B0",
      finals: ["B2"],
      transitions: [
        { from: "B0", label: "?x", to: "B1" },
        { from: "B1", label: "?y", to: "B2" },
      ],
    });
    const v = checkCompatibility(a, b, 2);
    expect(v.kind).toBe("deadlock");
    if (v.kind === "deadlock") {
      expect(v.info.category).toBe("peer-terminated");
      expect(v.trace.length).toBe(2); // !x 然后 ?x
    }
  });

  test("多发无人接收的消息 → 意外接收而非正常终止", () => {
    const a = build({
      name: "a",
      initial: "A0",
      finals: ["A2"],
      transitions: [
        { from: "A0", label: "!x", to: "A1" },
        { from: "A1", label: "!extra", to: "A2" },
      ],
    });
    const b = build({
      name: "b",
      initial: "B0",
      finals: ["B1"],
      transitions: [{ from: "B0", label: "?x", to: "B1" }],
    });
    const v = checkCompatibility(a, b, 2);
    expect(v.kind).toBe("unexpected-reception");
    if (v.kind === "unexpected-reception") {
      expect(v.message).toBe("extra");
    }
  });

  test("发送受队列容量约束：容量 1 时连发两条会被阻塞直到对方接收", () => {
    const a = build({
      name: "a",
      initial: "A0",
      finals: ["A2"],
      transitions: [
        { from: "A0", label: "!x", to: "A1" },
        { from: "A1", label: "!y", to: "A2" },
      ],
    });
    // b 只在收到 x 之后才能收到 y；容量 1 下 a 必须等待
    const b = build({
      name: "b",
      initial: "B0",
      finals: ["B2"],
      transitions: [
        { from: "B0", label: "?x", to: "B1" },
        { from: "B1", label: "?y", to: "B2" },
      ],
    });
    expect(checkCompatibility(a, b, 1).kind).toBe("compatible");
  });
});

describe("协议解析", () => {
  test("tau / τ 都解析为内部动作", () => {
    const p = parseProtocol({
      name: "p",
      initial: "S0",
      transitions: [
        { from: "S0", label: "tau", to: "S1" },
        { from: "S1", label: "τ", to: "S0" },
      ],
    });
    expect(p.transitions.every((t) => t.action.kind === "internal")).toBe(true);
  });

  test("非法标签抛出错误", () => {
    expect(() =>
      parseProtocol({
        name: "p",
        initial: "S0",
        transitions: [{ from: "S0", label: "hello", to: "S1" }],
      })
    ).toThrow();
  });
});
