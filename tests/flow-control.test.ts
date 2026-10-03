import { test } from "node:test";
import assert from "node:assert/strict";
import { once } from "node:events";
import { createHash } from "node:crypto";
import { FlowEndpoint } from "../src/endpoint.ts";
import { MemoryDuplex } from "../src/transport.ts";

const KB = 1024;

function sha256(buf: Buffer): string {
  return createHash("sha256").update(buf).digest("hex");
}

/** 构造确定性伪随机“文件”内容 */
function makeFile(size: number): Buffer {
  const buf = Buffer.alloc(size);
  for (let i = 0; i < size; i++) buf[i] = (i * 2654435761) % 251;
  return buf;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

interface Pair {
  a: FlowEndpoint;
  b: FlowEndpoint;
}

function connect(recvCapacityB: number, recvCapacityA = 64 * KB, latencyMs = 1): Pair {
  const [ta, tb] = MemoryDuplex.pair(latencyMs);
  const a = new FlowEndpoint(recvCapacityA, { mss: 1024, probeIntervalMs: 15 });
  const b = new FlowEndpoint(recvCapacityB, { mss: 1024, probeIntervalMs: 15 });
  a.attach(ta);
  b.attach(tb);
  return { a, b };
}

/** 不变式监控：任何时刻 在途字节数 <= 对端通告窗口 */
function watchInvariant(ep: FlowEndpoint, violations: string[]): void {
  const check = () => {
    if (ep.sender.bytesInFlight > ep.sender.peerWindow) {
      violations.push(
        `bytesInFlight=${ep.sender.bytesInFlight} > peerWindow=${ep.sender.peerWindow}`,
      );
    }
  };
  ep.sender.on("frame", check);
  ep.sender.on("suspend", check);
  ep.sender.on("resume", check);
}

test("窗口受限传输：在途字节严格不越窗，数据完整按序交付", async () => {
  const { a, b } = connect(8 * KB);
  const violations: string[] = [];
  watchInvariant(a, violations);

  const file = makeFile(64 * KB);
  const received: Buffer[] = [];
  b.on("data", () => received.push(b.consume(KB))); // 接收端即时消费

  a.send(file);
  await once(a.sender, "idle");
  await sleep(20);

  const got = Buffer.concat(received);
  assert.equal(got.length, file.length);
  assert.equal(sha256(got), sha256(file));
  assert.deepEqual(violations, []);
  a.close();
  b.close();
});

test("窗口归零：发送端挂起并进入探测模式，窗口扩大 ACK 后恢复", async () => {
  const { a, b } = connect(4 * KB);
  const violations: string[] = [];
  watchInvariant(a, violations);

  const events: string[] = [];
  const resumeWindows: number[] = [];
  a.on("suspend", () => events.push("suspend"));
  a.on("resume", () => {
    events.push("resume");
    resumeWindows.push(a.sender.peerWindow);
  });
  a.on("probe", () => events.push("probe"));

  const file = makeFile(32 * KB);
  const suspended = once(a, "suspend"); // 先挂监听，suspend 可能同步触发
  a.send(file); // 接收端完全不消费 -> 窗口必然归零
  await suspended;

  // 覆盖多个零窗口探测周期
  await sleep(150);
  assert.ok(events.includes("probe"), "零窗口下应发出探测帧");
  assert.equal(a.sender.peerWindow, 0);
  assert.ok(a.sender.isSuspended, "窗口归零时必须处于挂起状态");
  assert.ok(a.sender.pendingBytes > 0, "窗口归零时仍有数据等待发送");
  assert.ok(
    resumeWindows.every((w) => w > 0),
    "只有窗口扩大 ACK 才能触发恢复",
  );

  // 接收端开始消费 -> 窗口扩大 ACK -> 发送端恢复
  const received: Buffer[] = [];
  const drain = setInterval(() => {
    const chunk = b.consume(2 * KB);
    if (chunk.length > 0) received.push(chunk);
  }, 5);

  await once(a, "resume");
  await once(a.sender, "idle");
  clearInterval(drain);
  const rest = b.consume(1 << 30);
  if (rest.length > 0) received.push(rest);

  assert.ok(events.indexOf("suspend") < events.indexOf("resume"), "必须先挂起后恢复");
  const got = Buffer.concat(received);
  assert.equal(sha256(got), sha256(file));
  assert.deepEqual(violations, []);
  a.close();
  b.close();
});

test("验收场景：快速注入大文件 + 慢速消费 + 窗口缩小通知 -> 挂起 -> 窗口扩大后续传", async () => {
  const { a, b } = connect(32 * KB);
  const violations: string[] = [];
  watchInvariant(a, violations);

  const stats = { suspends: 0, resumes: 0, probes: 0, minWindowSeen: Infinity };
  a.on("suspend", () => stats.suspends++);
  a.on("resume", () => stats.resumes++);
  a.on("probe", () => stats.probes++);
  a.sender.on("frame", () => {
    stats.minWindowSeen = Math.min(stats.minWindowSeen, a.sender.peerWindow);
  });

  // 接收端：慢速消费（每 10ms 消费 2KB）
  const received: Buffer[] = [];
  const consumer = setInterval(() => {
    const chunk = b.consume(2 * KB);
    if (chunk.length > 0) received.push(chunk);
  }, 10);

  // 接收端：150ms 后下发窗口缩小通知（32KB -> 8KB），500ms 后恢复
  setTimeout(() => b.resizeWindow(8 * KB), 150);
  setTimeout(() => b.resizeWindow(32 * KB), 500);

  // 发送端：快速注入 256KB 大文件
  const file = makeFile(256 * KB);
  a.send(file);

  await once(a.sender, "idle", { signal: AbortSignal.timeout(15000) });
  clearInterval(consumer);
  const rest = b.consume(1 << 30);
  if (rest.length > 0) received.push(rest);

  // 验收断言
  assert.ok(stats.suspends >= 1, "在途数据填满窗口后必须挂起");
  assert.ok(stats.resumes >= 1, "收到窗口扩大 ACK 后必须恢复");
  assert.ok(stats.probes >= 1, "窗口归零期间必须发出探测帧");
  assert.ok(stats.minWindowSeen <= 8 * KB, "应观察到窗口缩小通知生效");
  const got = Buffer.concat(received);
  assert.equal(got.length, file.length);
  assert.equal(sha256(got), sha256(file), "文件内容必须完整一致");
  assert.deepEqual(violations, [], "任何时刻在途字节数不得超过通告窗口");
  a.close();
  b.close();
});

test("乱序/重复段被丢弃并回告当前信标", async () => {
  const [ta, tb] = MemoryDuplex.pair(0);
  const a = new FlowEndpoint(64 * KB, { mss: 512 });
  const b = new FlowEndpoint(4 * KB, { mss: 512 });

  // 人为制造重复投递：30% 的帧发送两次
  const origSend = ta.send.bind(ta);
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
  ta.send = (data: Buffer) => {
    origSend(data);
    if (rand() < 0.3) origSend(data);
  };

  a.attach(ta);
  b.attach(tb);

  const file = makeFile(16 * KB);
  const received: Buffer[] = [];
  b.on("data", () => received.push(b.consume(1 << 30)));

  a.send(file);
  await once(a.sender, "idle");
  await sleep(20);

  const got = Buffer.concat(received);
  assert.equal(sha256(got), sha256(file));
  a.close();
  b.close();
});
