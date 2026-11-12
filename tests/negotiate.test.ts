import { test } from "node:test";
import assert from "node:assert/strict";
import {
  BearerToken,
  negotiateSubprotocol,
  redactBearer,
  safeHeaderForLog,
  splitProtocolHeader,
} from "../src/negotiate.ts";

const SERVER = ["v1.binary", "v1.chat", "v1.telemetry"];

test("无交集：握手成功但不回显 Sec-WebSocket-Protocol，subprotocol 为 null", () => {
  const result = negotiateSubprotocol("v9.chat, v9.other", { serverProtocols: SERVER });

  assert.equal(result.ok, true);
  if (!result.ok) throw new Error("expected ok");
  assert.equal(result.subprotocol, null);
  assert.deepEqual(result.toResponseHeaders(), {});
  assert.equal(result.bearerToken, null);
});

test("按服务端优先级只选一个，并原样回显客户端拼写", () => {
  const result = negotiateSubprotocol("v1.telemetry, v1.chat", { serverProtocols: SERVER });

  assert.equal(result.ok, true);
  if (!result.ok) throw new Error("expected ok");
  assert.equal(result.subprotocol, "v1.chat");
  assert.deepEqual(result.toResponseHeaders(), { "Sec-WebSocket-Protocol": "v1.chat" });
});

test("逗号分隔并忽略每个元素前后的空白", () => {
  assert.deepEqual(splitProtocolHeader("  v1.chat ,  v1.binary "), ["v1.chat", "v1.binary"]);
  assert.deepEqual(splitProtocolHeader(undefined), []);
});

test("重复的子协议名拒绝握手（含带空格的重复）", () => {
  const result = negotiateSubprotocol("v1.chat, v1.chat", { serverProtocols: SERVER });
  assert.equal(result.ok, false);
  if (result.ok) throw new Error("expected rejection");
  assert.equal(result.reason, "duplicate-element");

  const spaced = negotiateSubprotocol("v1.chat,  v1.chat ", { serverProtocols: SERVER });
  assert.equal(spaced.ok, false);
});

test("非 token 字符的子协议名拒绝握手", () => {
  const result = negotiateSubprotocol("v1 chat", { serverProtocols: SERVER });
  assert.equal(result.ok, false);
  if (result.ok) throw new Error("expected rejection");
  assert.equal(result.reason, "invalid-token");

  const empty = negotiateSubprotocol("v1.chat, , v1.binary", { serverProtocols: SERVER });
  assert.equal(empty.ok, false);
});

test("bearer 令牌可提取，且不参与子协议选择或回显", () => {
  const secret = "eyJhbGciOiJIUzI1NiJ9.payload.sig";
  const result = negotiateSubprotocol(`bearer.${secret}, v1.chat`, { serverProtocols: SERVER });

  assert.equal(result.ok, true);
  if (!result.ok) throw new Error("expected ok");
  assert.equal(result.subprotocol, "v1.chat");
  assert.ok(result.bearerToken instanceof BearerToken);
  assert.equal(result.bearerToken?.reveal(), secret);
  assert.deepEqual(result.toResponseHeaders(), { "Sec-WebSocket-Protocol": "v1.chat" });

  const headerValue = JSON.stringify(result.toResponseHeaders());
  assert.ok(!headerValue.includes(secret));
  assert.ok(!JSON.stringify(result.audit).includes(secret));
});

test("只有 bearer 且无协议交集：无 Sec-WebSocket-Protocol 头", () => {
  const result = negotiateSubprotocol("bearer.secret-token", { serverProtocols: SERVER });
  assert.equal(result.ok, true);
  if (!result.ok) throw new Error("expected ok");
  assert.equal(result.subprotocol, null);
  assert.deepEqual(result.toResponseHeaders(), {});
  assert.equal(result.bearerToken?.reveal(), "secret-token");
});

test("重复的 bearer 元素与多个令牌都拒绝握手", () => {
  const duplicate = negotiateSubprotocol("bearer.a, bearer.a, v1.chat", { serverProtocols: SERVER });
  assert.equal(duplicate.ok, false);
  if (duplicate.ok) throw new Error("expected rejection");
  assert.equal(duplicate.reason, "duplicate-element");

  const multiple = negotiateSubprotocol("bearer.a, bearer.b", { serverProtocols: SERVER });
  assert.equal(multiple.ok, false);
  if (multiple.ok) throw new Error("expected rejection");
  assert.equal(multiple.reason, "multiple-bearer-tokens");
  assert.ok(!JSON.stringify(multiple.audit).includes("bearer.a"));
  assert.deepEqual(multiple.audit.offered, ["bearer.[REDACTED]", "bearer.[REDACTED]"]);
});

test("空令牌或非法令牌字符拒绝握手", () => {
  const empty = negotiateSubprotocol("bearer., v1.chat", { serverProtocols: SERVER });
  assert.equal(empty.ok, false);
  if (empty.ok) throw new Error("expected rejection");
  assert.equal(empty.reason, "empty-bearer-token");

  const illegal = negotiateSubprotocol("bearer.bad token", { serverProtocols: SERVER });
  assert.equal(illegal.ok, false);
});

test("审计视图与日志工具对令牌脱敏", () => {
  const secret = "super-secret-value";
  const result = negotiateSubprotocol(`bearer.${secret}, v1.chat`, { serverProtocols: SERVER });
  if (!result.ok) throw new Error("expected ok");

  assert.deepEqual(result.audit.offered, ["bearer.[REDACTED]", "v1.chat"]);
  assert.equal(result.audit.hasBearerToken, true);
  assert.equal(`${result.bearerToken}`, "[REDACTED]");
  assert.equal(redactBearer(`bearer.${secret}`), "bearer.[REDACTED]");
  assert.equal(
    safeHeaderForLog(`v1.chat, bearer.${secret}`),
    "v1.chat, bearer.[REDACTED]",
  );

  const serialized = JSON.stringify({ token: result.bearerToken, audit: result.audit });
  assert.ok(!serialized.includes(secret));
});

test("服务端配置非法时直接抛出程序员错误", () => {
  assert.throws(() => negotiateSubprotocol("v1.chat", { serverProtocols: [] }));
  assert.throws(() =>
    negotiateSubprotocol("v1.chat", { serverProtocols: ["bad name"] }),
  );
  assert.throws(() =>
    negotiateSubprotocol("v1.chat", { serverProtocols: ["bearer.x"] }),
  );
});
