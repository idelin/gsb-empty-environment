/**
 * 运行样例：双向流量控制信标 + 滑动窗口协调
 *
 * 场景：发送端 A 快速注入 1MB 大文件；接收端 B 慢速消费，
 * 并在传输中途下发窗口缩小通知（64KB -> 16KB），随后恢复。
 * 观察 A 在在途数据填满窗口时挂起、窗口归零时探测、
 * 收到窗口扩大 ACK 后恢复发送的全过程。
 */
import { createHash } from "node:crypto";
import { FlowEndpoint } from "../src/endpoint.ts";
import { MemoryDuplex } from "../src/transport.ts";

const KB = 1024;
const FILE_SIZE = 1024 * KB;

function makeFile(size: number): Buffer {
  const pattern = Buffer.alloc(251);
  for (let i = 0; i < pattern.length; i++) pattern[i] = (i * 31 + 7) & 0xff;
  const buf = Buffer.alloc(size);
  for (let off = 0; off < size; off += pattern.length) {
    pattern.copy(buf, off, 0, Math.min(pattern.length, size - off));
  }
  return buf;
}

// 预生成文件，再启动计时，保证时间轴只反映传输过程
const file = makeFile(FILE_SIZE);
const fileHash = createHash("sha256").update(file).digest("hex");

const t0 = Date.now();
function log(msg: string): void {
  console.log(`[${String(Date.now() - t0).padStart(5)}ms] ${msg}`);
}

// 链路：单向时延 2ms
const [ta, tb] = MemoryDuplex.pair(2);
const a = new FlowEndpoint(64 * KB, { mss: 1460, probeIntervalMs: 50 });
const b = new FlowEndpoint(64 * KB, { mss: 1460, probeIntervalMs: 50 });
a.attach(ta);
b.attach(tb);

const stats = { frames: 0, suspends: 0, resumes: 0, probes: 0, maxInFlight: 0 };
a.sender.on("frame", () => {
  stats.frames++;
  stats.maxInFlight = Math.max(stats.maxInFlight, a.sender.bytesInFlight);
});
a.on("suspend", (s) => {
  stats.suspends++;
  if (stats.suspends <= 3 || stats.suspends % 50 === 0) {
    log(`A 挂起 #${stats.suspends}：在途 ${s.bytesInFlight}B / 对端窗口 ${s.peerWindow}B，暂停发送`);
  }
});
a.on("resume", (s) => {
  stats.resumes++;
  if (stats.resumes <= 3 || stats.resumes % 50 === 0) {
    log(`A 恢复 #${stats.resumes}：收到窗口扩大 ACK（窗口=${s.peerWindow}B），继续发送`);
  }
});
a.on("probe", () => {
  stats.probes++;
  log(`A 零窗口探测 #${stats.probes}（1 字节 PROBE 帧）`);
});

// 接收端 B：慢速消费，每 25ms 消费 4KB
let consumed = 0;
const sink = createHash("sha256");
const consumer = setInterval(() => {
  const chunk = b.consume(4 * KB);
  sink.update(chunk);
  consumed += chunk.length;
  if (consumed >= FILE_SIZE) finish();
}, 25);

// 接收端 B：1s 时下发窗口缩小通知，4s 时恢复
setTimeout(() => {
  log("B 下发窗口缩小通知：接收容量 64KB -> 16KB");
  b.resizeWindow(16 * KB);
}, 1000);
setTimeout(() => {
  log("B 恢复接收容量：16KB -> 64KB");
  b.resizeWindow(64 * KB);
}, 4000);

// 发送端 A：快速注入 1MB 大文件
log(`A 注入 ${FILE_SIZE / KB}KB 文件，sha256=${fileHash.slice(0, 16)}...`);
a.send(file);

function finish(): void {
  clearInterval(consumer);
  const rest = b.consume(1 << 30);
  sink.update(rest);
  consumed += rest.length;
  const ok = consumed === FILE_SIZE && sink.digest("hex") === fileHash;
  log(`传输完成：${consumed / KB}KB，完整性校验 ${ok ? "通过 ✓" : "失败 ✗"}`);
  console.log("\n===== 统计 =====");
  console.log(`发送帧数        : ${stats.frames}`);
  console.log(`挂起次数        : ${stats.suspends}`);
  console.log(`恢复次数        : ${stats.resumes}`);
  console.log(`零窗口探测次数  : ${stats.probes}`);
  console.log(`峰值在途字节    : ${stats.maxInFlight}B（不超过通告窗口）`);
  a.close();
  b.close();
  process.exit(ok ? 0 : 1);
}
