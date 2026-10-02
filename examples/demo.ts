/**
 * 运行样例：模拟高性能网络 IO 场景 ——
 * 100MB 随机帧流持续写入 4MB 环形内存池，消费者以零拷贝切片读取并即时释放。
 *
 * 运行：node examples/demo.ts
 */
import { RingBufferPool } from "../src/pool.ts";
import { mulberry32, fnv1a } from "../tests/helpers.ts";

const STREAM_BYTES = 100 * 1024 * 1024;

const pool = new RingBufferPool(); // 64 块 x 64KB = 4MB 常驻
const rand = mulberry32(Date.now() & 0xffffffff);

let streamed = 0;
let frames = 0;
let writeHash = 0x811c9dc5;
let readHash = 0x811c9dc5;
const started = performance.now();

while (streamed < STREAM_BYTES) {
  const length = 512 + Math.floor(rand() * (32 * 1024 - 512));
  const frame = new Uint8Array(length);
  for (let i = 0; i < length; i++) frame[i] = Math.floor(rand() * 256);

  writeHash = fnv1a(frame, writeHash);

  // 生产者：写入环形池，得到零拷贝切片（网络框架中可将其交给协议解析器）
  const slice = pool.write(frame);

  // 消费者：直接在池内内存上读取，无需拷贝；用完即释放
  readHash = fnv1a(slice.view, readHash);
  slice.release();

  streamed += length;
  frames++;
}

const elapsed = performance.now() - started;
const stats = pool.stats();
const mbps = (streamed / 1048576 / (elapsed / 1000)).toFixed(0);

console.log(`流量:        ${frames} 帧 / ${(streamed / 1048576).toFixed(1)} MB`);
console.log(`耗时:        ${elapsed.toFixed(0)} ms (${mbps} MB/s)`);
console.log(`池常驻内存:  ${(stats.totalBytes / 1048576).toFixed(0)} MB (固定)`);
console.log(`数据完整性:  ${readHash === writeHash ? "OK (哈希一致)" : "FAIL"}`);
console.log(`未释放切片:  ${stats.outstandingSlices} (引用 ${stats.liveReferences})`);
console.log(`Slice 对象:  创建 ${stats.slicesCreated} 个, 池中复用 ${stats.idleSlices} 个`);
console.log(`可回收空间:  ${(stats.writableBytes / 1048576).toFixed(0)} MB (应等于池容量)`);
