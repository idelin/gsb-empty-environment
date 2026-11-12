import { test } from "node:test";
import assert from "node:assert/strict";
import { decideFrame, enforceMessage, type CloseableLike, type MessagePolicy } from "../src/gate.ts";

const chatPolicy: MessagePolicy = {
  frameKinds: ["text"],
  appTypes: ["message", "typing"],
};

test("允许策略内的帧类型与应用层消息类型", () => {
  const allowed = decideFrame("text", JSON.stringify({ type: "message", body: "hi" }), chatPolicy);
  assert.equal(allowed.allowed, true);

  const binaryPolicy: MessagePolicy = { frameKinds: ["binary"] };
  assert.equal(decideFrame("binary", new Uint8Array([1, 2]), binaryPolicy).allowed, true);
});

test("越权帧类型返回 1008 决策", () => {
  const decision = decideFrame("binary", new Uint8Array([1]), chatPolicy);
  assert.equal(decision.allowed, false);
  if (decision.allowed) throw new Error("expected denial");
  assert.equal(decision.closeCode, 1008);
});

test("越权应用层 type 返回 1008 决策", () => {
  const decision = decideFrame("text", JSON.stringify({ type: "admin.shutdown" }), chatPolicy);
  assert.equal(decision.allowed, false);
  if (decision.allowed) throw new Error("expected denial");
  assert.equal(decision.closeCode, 1008);

  const malformed = decideFrame("text", "not-json", chatPolicy);
  assert.equal(malformed.allowed, false);
});

test("enforceMessage 拒绝时立即关闭连接", () => {
  const closed: Array<{ code: number; reason: string }> = [];
  const socket: CloseableLike = {
    close(code, reason) {
      closed.push({ code: code ?? 0, reason: reason ?? "" });
    },
  };

  const denied = enforceMessage(socket, "binary", new Uint8Array([9]), chatPolicy);
  assert.equal(denied.allowed, false);
  assert.equal(closed.length, 1);
  assert.equal(closed[0]?.code, 1008);

  const allowed = enforceMessage(socket, "text", JSON.stringify({ type: "typing" }), chatPolicy);
  assert.equal(allowed.allowed, true);
  assert.equal(closed.length, 1);
});
