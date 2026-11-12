import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import {
  NegotiatingWebSocketServer,
  type NegotiatedConnection,
} from "../src/server.ts";
import { MemoryWebSocketClient } from "./helpers/memory-ws-client.ts";

const SECRET_TOKEN = "loopback-secret-token-123";

function createTestServer() {
  const rejects: Array<{ reason: string; audit: unknown }> = [];
  const server = new NegotiatingWebSocketServer({
    serverProtocols: ["v1.binary", "v1.chat"],
    policies: {
      "v1.chat": { frameKinds: ["text"], appTypes: ["message", "typing"] },
      "v1.binary": { frameKinds: ["binary"] },
    },
  });
  server.on("reject", (info) => rejects.push({ reason: info.reason, audit: info.audit }));
  return { server, rejects };
}

let harness = createTestServer();

beforeEach(() => {
  harness = createTestServer();
});

function attach(transport: Parameters<NegotiatingWebSocketServer["attachTransport"]>[0]): void {
  harness.server.attachTransport(transport);
}

test("无交集：101 握手成功但响应不含 Sec-WebSocket-Protocol，连接标记无子协议", async () => {
  const client = await MemoryWebSocketClient.handshake(attach, ["v9.unknown"]);

  assert.match(client.statusLine, /^HTTP\/1\.1 101/);
  assert.equal(client.headers.has("sec-websocket-protocol"), false);
  assert.equal(client.protocol, "");

  const closed = client.waitForClose();
  client.sendText("anything");
  const event = await closed;
  assert.equal(event.code, 1008);
});

test("重复子协议：握手被 HTTP 400 拒绝且不回显协议头", async () => {
  const client = await MemoryWebSocketClient.rejectedHandshake(attach, ["v1.chat", "v1.chat"]);

  assert.match(client.statusLine, /^HTTP\/1\.1 400/);
  assert.equal(client.headers.has("sec-websocket-protocol"), false);
  assert.ok(harness.rejects.some((entry) => entry.reason === "duplicate-element"));
});

test("令牌回显：bearer 不进响应头，子协议原样回显且服务端可取令牌", async () => {
  const connectionReceived = new Promise<NegotiatedConnection>((resolve) => {
    harness.server.once("connection", resolve);
  });

  const client = await MemoryWebSocketClient.handshake(attach, [`bearer.${SECRET_TOKEN}`, "v1.chat"]);

  assert.match(client.statusLine, /^HTTP\/1\.1 101/);
  assert.equal(client.protocol, "v1.chat");
  const allHeaderText = JSON.stringify([...client.headers.entries()]);
  assert.ok(!allHeaderText.includes(SECRET_TOKEN));

  const connection = await connectionReceived;
  assert.equal(connection.bearerToken?.reveal(), SECRET_TOKEN);
  assert.ok(!`${connection.bearerToken}`.includes(SECRET_TOKEN));
  assert.equal(connection.subprotocol, "v1.chat");
  client.destroy();
});

test("越权消息类型：chat 连接发送 binary 立即以 1008 关闭", async () => {
  const client = await MemoryWebSocketClient.handshake(attach, ["v1.chat"]);
  assert.equal(client.protocol, "v1.chat");

  const closed = client.waitForClose();
  client.sendBinary(new Uint8Array([0xde, 0xad]));
  const event = await closed;
  assert.equal(event.code, 1008);
});

test("越权应用层 type：未授权的 admin 消息以 1008 关闭，授权消息可正常处理", async () => {
  const receivedText: string[] = [];
  harness.server.once("connection", (connection) => {
    connection.on("text", (payload: string) => receivedText.push(payload));
  });

  const client = await MemoryWebSocketClient.handshake(attach, ["v1.chat"]);
  client.sendText(JSON.stringify({ type: "message", body: "allowed" }));
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(receivedText, [JSON.stringify({ type: "message", body: "allowed" })]);

  const closed = client.waitForClose();
  client.sendText(JSON.stringify({ type: "admin.shutdown" }));
  const event = await closed;
  assert.equal(event.code, 1008);
});

test("服务端优先级：客户端多个交集时按服务端顺序选择，并回显客户端拼写", async () => {
  const connectionReceived = new Promise<NegotiatedConnection>((resolve) => {
    harness.server.once("connection", resolve);
  });
  const client = await MemoryWebSocketClient.handshake(attach, ["v1.chat", "v1.binary"]);
  assert.equal(client.protocol, "v1.binary");

  const connection = await connectionReceived;
  assert.equal(connection.subprotocol, "v1.binary");

  const closed = client.waitForClose();
  client.sendText("text-is-forbidden-on-binary-protocol");
  const event = await closed;
  assert.equal(event.code, 1008);
});

test("拒绝事件的审计信息中不包含令牌原文", async () => {
  const client = await MemoryWebSocketClient.rejectedHandshake(attach, [
    `bearer.${SECRET_TOKEN}`,
    "bearer.other-token",
  ]);
  assert.match(client.statusLine, /^HTTP\/1\.1 400/);

  const logged = JSON.stringify(harness.rejects);
  assert.ok(!logged.includes(SECRET_TOKEN));
  assert.ok(logged.includes("[REDACTED]"));
});

test("仅提交 bearer 且无协议交集：握手继续、无协议头、策略缺失时数据帧被 1008 拒绝", async () => {
  const client = await MemoryWebSocketClient.handshake(attach, [`bearer.${SECRET_TOKEN}`]);
  assert.match(client.statusLine, /^HTTP\/1\.1 101/);
  assert.equal(client.protocol, "");

  const closed = client.waitForClose();
  client.sendText("no-subprotocol-no-policy");
  const event = await closed;
  assert.equal(event.code, 1008);
});
