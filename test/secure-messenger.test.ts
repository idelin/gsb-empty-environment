import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SecureMessenger,
  attachHandshakeResponder,
  assertExactOrigin,
  OriginPolicyError,
  HandshakeTimeoutError,
  HELLO_TYPE,
  ACK_TYPE,
  PORT_TYPE,
  PROTOCOL_VERSION,
  type MessageEventLike,
  type WindowLike,
} from "../src/secure-messenger.ts";

type Listener = (event: MessageEventLike) => void;

interface SentEntry {
  source: FakeWindow;
  message: unknown;
  targetOrigin: string;
  transfer?: unknown[];
}

/**
 * 模拟浏览器窗口：postMessage 投递时按目标窗口“当前”来源校验
 * （targetOrigin 不匹配则丢弃），navigate() 模拟换站导航并卸载旧页面。
 */
class FakeWindow {
  readonly name: string;
  origin: string;
  readonly sentLog: SentEntry[] = [];
  private readonly listeners = new Set<Listener>();
  private readonly endpoints = new Map<FakeWindow, WindowLike>();

  constructor(name: string, origin: string) {
    this.name = name;
    this.origin = origin;
  }

  /** 返回 source 指向本窗口的 postMessage 端点（类似 WindowProxy，跨导航保持身份）。 */
  endpointFor(source: FakeWindow): WindowLike {
    let endpoint = this.endpoints.get(source);
    if (!endpoint) {
      endpoint = {
        postMessage: (message: unknown, targetOrigin: string, transfer?: unknown[]) =>
          this.receive(source, message, targetOrigin, transfer),
      };
      this.endpoints.set(source, endpoint);
    }
    return endpoint;
  }

  private receive(source: FakeWindow, message: unknown, targetOrigin: string, transfer?: unknown[]): void {
    this.sentLog.push({ source, message, targetOrigin, transfer });
    queueMicrotask(() => {
      // 浏览器语义：投递时按窗口当前来源校验，不匹配则丢弃。
      if (targetOrigin !== "*" && targetOrigin !== this.origin) return;
      const event: MessageEventLike = {
        data: message,
        origin: source.origin,
        source: source.endpointFor(this),
        ports: transfer ?? [],
      };
      for (const listener of [...this.listeners]) listener(event);
    });
  }

  navigate(origin: string): void {
    this.origin = origin;
    this.listeners.clear(); // 旧页面卸载
  }

  addEventListener(_type: "message", listener: Listener): void {
    this.listeners.add(listener);
  }

  removeEventListener(_type: "message", listener: Listener): void {
    this.listeners.delete(listener);
  }
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 20));

function messageType(entry: SentEntry): unknown {
  return (entry.message as { type?: unknown })?.type;
}

test("targetOrigin 校验：禁用 * 与非精确来源", () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "https://app.example");
  const target = iframe.endpointFor(parent);

  assert.throws(() => new SecureMessenger({ target, targetOrigin: "*" }), OriginPolicyError);
  assert.throws(() => new SecureMessenger({ target, targetOrigin: "" }), OriginPolicyError);
  assert.throws(() => new SecureMessenger({ target, targetOrigin: "https://app.example/path" }), OriginPolicyError);
  assert.throws(() => new SecureMessenger({ target, targetOrigin: "https://app.example/" }), OriginPolicyError);
  assert.throws(() => new SecureMessenger({ target, targetOrigin: "https://app.example:443" }), OriginPolicyError);
  assert.throws(() => new SecureMessenger({ target, targetOrigin: "https://user@app.example" }), OriginPolicyError);
  assert.throws(() => new SecureMessenger({ target, targetOrigin: "javascript:alert(1)" }), OriginPolicyError);
  assert.throws(() => assertExactOrigin("null"), OriginPolicyError);
  assert.equal(assertExactOrigin("https://app.example"), "https://app.example");
});

test("精确来源：未导航时正常投递", async () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "https://app.example");
  const received: unknown[] = [];
  iframe.addEventListener("message", (event) => received.push(event.data));

  const messenger = new SecureMessenger({
    target: iframe.endpointFor(parent),
    targetOrigin: "https://app.example",
  });
  await messenger.post({ token: "t-1" });
  await flush();

  assert.deepEqual(received, [{ token: "t-1" }]);
  assert.ok(iframe.sentLog.every((entry) => entry.targetOrigin === "https://app.example"));
});

test("导航换站：发送前 iframe 被导航，令牌不送达恶意页面", async () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "https://app.example");

  const messenger = new SecureMessenger({
    target: iframe.endpointFor(parent),
    targetOrigin: "https://app.example",
  });

  // 攻击者在发送前把 iframe 导航到恶意站点。
  iframe.navigate("https://evil.example");
  const stolen: unknown[] = [];
  iframe.addEventListener("message", (event) => stolen.push(event.data));

  await messenger.post({ token: "secret-token" });
  await flush();

  assert.equal(stolen.length, 0, "被导航后令牌不得送达");
  assert.ok(
    iframe.sentLog.every((entry) => entry.targetOrigin === "https://app.example"),
    "始终使用精确来源，绝不退化为 *",
  );
});

test("导航换站：postMessage 入队后、投递前发生导航同样被拦截", async () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "https://app.example");

  const messenger = new SecureMessenger({
    target: iframe.endpointFor(parent),
    targetOrigin: "https://app.example",
  });

  const pending = messenger.post({ token: "secret-token" });
  iframe.navigate("https://evil.example"); // 与投递竞争
  const stolen: unknown[] = [];
  iframe.addEventListener("message", (event) => stolen.push(event.data));
  await pending;
  await flush();

  assert.equal(stolen.length, 0);
});

test("opaque 来源（无来源 sandbox iframe）：先握手再经 MessagePort 传敏感数据", async () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "null"); // sandbox 无 allow-same-origin
  const received: unknown[] = [];

  attachHandshakeResponder({
    messageEvents: iframe,
    allowedOrigin: "https://parent.example",
    onChannel: (port) => {
      (port as unknown as { unref?: () => void }).unref?.();
      (port as unknown as { on(t: string, cb: (m: unknown) => void): void }).on("message", (m) =>
        received.push(m),
      );
    },
  });

  const messenger = new SecureMessenger({
    target: iframe.endpointFor(parent),
    targetOrigin: "null",
    messageEvents: parent,
    handshakeTimeoutMs: 500,
  });
  await messenger.post({ token: "secret-token" });
  await flush();
  messenger.close();

  assert.deepEqual(received, [{ token: "secret-token" }], "令牌经 MessagePort 送达");

  const hello = iframe.sentLog.find((entry) => messageType(entry) === HELLO_TYPE);
  assert.ok(hello, "应发出握手 hello");
  assert.match((hello!.message as { challenge: string }).challenge, /^[0-9a-f]{32}$/, "握手含随机挑战");

  const portMessage = iframe.sentLog.find((entry) => messageType(entry) === PORT_TYPE);
  assert.ok(portMessage, "握手后应转移 MessagePort");
  assert.equal(portMessage!.transfer?.length, 1);

  for (const entry of iframe.sentLog) {
    assert.ok(
      !JSON.stringify(entry.message).includes("secret-token"),
      "敏感数据不得出现在 window.postMessage 负载中",
    );
  }
});

test("重复握手：相同挑战只接受一次，重放被忽略", async () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "null");
  let channelCount = 0;

  attachHandshakeResponder({
    messageEvents: iframe,
    allowedOrigin: "https://parent.example",
    onChannel: (port) => {
      (port as unknown as { unref?: () => void }).unref?.();
      channelCount += 1;
    },
  });

  const messenger = new SecureMessenger({
    target: iframe.endpointFor(parent),
    targetOrigin: "null",
    messageEvents: parent,
    handshakeTimeoutMs: 500,
  });
  await messenger.post({ token: "first" });
  await flush();

  const hello = iframe.sentLog.find((entry) => messageType(entry) === HELLO_TYPE);
  const challenge = (hello!.message as { challenge: string }).challenge;

  // 重放相同的 hello（相同挑战）：响应方不应再次应答。
  iframe.endpointFor(parent).postMessage({ v: PROTOCOL_VERSION, type: HELLO_TYPE, challenge }, "*");
  await flush();
  const ackCount = parent.sentLog.filter((entry) => messageType(entry) === ACK_TYPE).length;
  assert.equal(ackCount, 1, "重复挑战不应再次应答");

  // 重放相同的 ack 到父页面：发送方不应再次转移 port。
  parent.endpointFor(iframe).postMessage({ v: PROTOCOL_VERSION, type: ACK_TYPE, challenge }, "https://parent.example");
  await flush();
  const portMessages = iframe.sentLog.filter((entry) => messageType(entry) === PORT_TYPE).length;
  assert.equal(portMessages, 1, "重放 ack 不应触发第二次 port 转移");
  assert.equal(channelCount, 1, "响应方只应建立一次通道");
  messenger.close();
});

test("并发发送共用一次握手（single-flight）", async () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "null");
  const received: unknown[] = [];

  attachHandshakeResponder({
    messageEvents: iframe,
    allowedOrigin: "https://parent.example",
    onChannel: (port) => {
      (port as unknown as { unref?: () => void }).unref?.();
      (port as unknown as { on(t: string, cb: (m: unknown) => void): void }).on("message", (m) =>
        received.push(m),
      );
    },
  });

  const messenger = new SecureMessenger({
    target: iframe.endpointFor(parent),
    targetOrigin: "null",
    messageEvents: parent,
    handshakeTimeoutMs: 500,
  });
  await Promise.all([messenger.post({ n: 1 }), messenger.post({ n: 2 })]);
  await flush();
  messenger.close();

  const hellos = iframe.sentLog.filter((entry) => messageType(entry) === HELLO_TYPE);
  assert.equal(hellos.length, 1, "并发发送只应触发一次握手");
  assert.equal(received.length, 2);
});

test("握手超时：无响应时拒绝且不泄露敏感数据", async () => {
  const parent = new FakeWindow("parent", "https://parent.example");
  const iframe = new FakeWindow("iframe", "null"); // 无响应方

  const messenger = new SecureMessenger({
    target: iframe.endpointFor(parent),
    targetOrigin: "null",
    messageEvents: parent,
    handshakeTimeoutMs: 50,
  });

  await assert.rejects(messenger.post({ token: "secret-token" }), HandshakeTimeoutError);
  assert.ok(
    iframe.sentLog.every((entry) => !JSON.stringify(entry.message).includes("secret-token")),
    "超时路径不得泄露敏感数据",
  );
});

test("响应方校验父页面精确来源，伪造来源的 hello 被忽略", async () => {
  const attacker = new FakeWindow("attacker", "https://evil.example");
  const iframe = new FakeWindow("iframe", "null");

  attachHandshakeResponder({
    messageEvents: iframe,
    allowedOrigin: "https://parent.example",
    onChannel: () => assert.fail("不应与攻击者建立通道"),
  });

  iframe.endpointFor(attacker).postMessage({ v: PROTOCOL_VERSION, type: HELLO_TYPE, challenge: "a".repeat(32) }, "*");
  await flush();

  assert.equal(attacker.sentLog.length, 0, "不应向伪造来源回包");
});
