/**
 * 两种接入方式示例：
 * 1. 纯函数接入任意 WebSocket 库（如 ws）；
 * 2. 本仓库自带的零依赖 NegotiatingWebSocketServer。
 */

import { negotiateSubprotocol, safeHeaderForLog } from "../src/negotiate.ts";
import { enforceMessage, type MessagePolicy } from "../src/gate.ts";
import { NegotiatingWebSocketServer } from "../src/server.ts";

const SERVER_PROTOCOLS = ["v1.chat", "v1.binary"];

const policies: Record<string, MessagePolicy> = {
  "v1.chat": { frameKinds: ["text"], appTypes: ["message", "typing"] },
  "v1.binary": { frameKinds: ["binary"] },
};

// ---------------------------------------------------------------------------
// 方式一：纯函数（可直接用于 ws 库的 verifyClient / handleProtocols 钩子）
// ---------------------------------------------------------------------------

function onUpgradeWithAnyLibrary(rawProtocolHeader: string | undefined): void {
  const result = negotiateSubprotocol(rawProtocolHeader, {
    serverProtocols: SERVER_PROTOCOLS,
  });

  if (!result.ok) {
    // result.reason 不含令牌；审计视图也已脱敏，可直接写日志
    console.warn("handshake rejected", result.reason, result.audit);
    return; // 调用方应返回 HTTP 400
  }

  // 无交集时是空对象：不输出 Sec-WebSocket-Protocol 头
  const responseHeaders = result.toResponseHeaders();
  console.log("response headers", responseHeaders);

  // 令牌只在这里按需显式读取，不要写进日志
  const token = result.bearerToken?.reveal();
  void token;

  // 即使拿到原始头，也请用脱敏工具记录
  console.log("raw header for log:", safeHeaderForLog(rawProtocolHeader));
}

function onMessageFromAnyLibrary(
  socket: { close(code?: number, reason?: string): void },
  subprotocol: string | null,
  kind: "text" | "binary",
  payload: string | Uint8Array,
): void {
  const policy = subprotocol === null ? null : policies[subprotocol];
  if (policy === null) {
    socket.close(1008, "no message policy for the negotiated subprotocol");
    return;
  }
  const decision = enforceMessage(socket, kind, payload, policy);
  if (decision.allowed) {
    // 处理合法消息……
  }
}

void onUpgradeWithAnyLibrary;
void onMessageFromAnyLibrary;

// ---------------------------------------------------------------------------
// 方式二：零依赖内置服务（监听端口在受限沙箱中可能被禁用，
// 测试使用内存传输，见 tests/integration.test.ts）
// ---------------------------------------------------------------------------

const server = new NegotiatingWebSocketServer({
  serverProtocols: SERVER_PROTOCOLS,
  policies,
});

server.on("reject", ({ reason, audit }) => {
  console.warn("handshake rejected:", reason, audit);
});

server.on("connection", (connection) => {
  console.log("connected with subprotocol:", connection.subprotocol);

  connection.on("text", (payload) => {
    console.log("allowed text message:", payload);
    connection.sendText(JSON.stringify({ type: "message", body: "ack" }));
  });

  connection.on("binary", (payload) => {
    console.log("allowed binary bytes:", payload.length);
  });

  connection.on("close", (code, reason) => {
    console.log("closed", code, reason);
  });
});

// listen 在允许网络的环境里可用；此处仅在直接运行示例时启动
if (process.argv[1]?.endsWith("usage.ts")) {
  const port = await server.listen(8080);
  console.log(`negotiating WebSocket server listening on ${port}`);
}
