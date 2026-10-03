/**
 * 运行样例：100MB 随机帧流经过 4MB 环形内存池。
 *
 * 运行：node --expose-gc examples/demo.ts   （或 npm run demo）
 *
 * 观测方法：先跑一轮 16MiB 预热（摊销分配器与对象池的一次性初始化），
 * 强制 GC 后记录基线，再跑 100MiB 正式流量，观测稳态下的：
 * - 常驻内存：环形区固定 4MiB，不随流量增长；
 * - 零拷贝：切片视图直接引用环形区，与原件逐字节比对；
 * - 守恒：借出切片数 == 释放切片数，终态在途为 0；
 * - GC 开销：Slice/描述符对象池复用，统计正式运行期的 GC 次数与堆变化。
 */
import { randomBytes } from 'node:crypto';
import { PerformanceObserver, constants } from 'node:perf_hooks';
import { RingPool, Slice } from '../src/ring-pool.ts';

const CAPACITY = 4 * 1024 * 1024;   // 4MiB 环形空间
const WARMUP = 16 * 1024 * 1024;    // 16MiB 预热流量
const TOTAL = 100 * 1024 * 1024;    // 100MiB 正式流量
const MiB = 1024 * 1024;

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---- GC 观测 ----
const gcCount = { major: 0, minor: 0 };
const observer = new PerformanceObserver((list) => {
  for (const entry of list.getEntries()) {
    const kind = (entry as unknown as { detail?: { kind?: number } }).detail?.kind;
    if (kind === constants.NODE_PERFORMANCE_GC_MAJOR) gcCount.major++;
    else if (kind === constants.NODE_PERFORMANCE_GC_MINOR) gcCount.minor++;
  }
});
observer.observe({ entryTypes: ['gc'] });

const gc = (globalThis as { gc?: () => void }).gc?.bind(globalThis);

interface RunStats {
  frames: number;
  peakUsed: number;
  peakActive: number;
  elapsedMs: number;
}

/** 让 total 字节的随机帧流经过环形池：写入(背压重试) -> 零拷贝切出 -> 逐字节校验 -> 释放。 */
function runStream(pool: RingPool, total: number, seed: number): RunStats {
  const rand = mulberry32(seed);
  const expected: Buffer[] = [];
  let produced = 0;
  let frames = 0;
  let peakUsed = 0;
  let peakActive = 0;
  const t0 = performance.now();

  while (pool.bytesRead < total) {
    // 生产者：随机帧（64B ~ 64KiB）尽量写满，背压时转消费
    while (produced < total) {
      const len = Math.min(64 + ((rand() * 65536) | 0), total - produced);
      const frame = randomBytes(len);
      if (!pool.writeFrame(frame)) break;
      expected.push(frame);
      produced += len;
    }
    peakUsed = Math.max(peakUsed, pool.used);
    // 消费者：流水线批量切出（模拟网络 IO 的批量处理），统一校验后批量释放
    const batch: Slice[] = [];
    let slice: Slice | null;
    while (batch.length < 64 && (slice = pool.readFrame()) !== null) {
      batch.push(slice);
    }
    peakActive = Math.max(peakActive, pool.activeSlices);
    for (const s of batch) {
      const want = expected.shift() as Buffer;
      const view = s.view(); // 零拷贝视图，直接引用环形内存
      if (view.length !== want.length || !view.equals(want)) {
        throw new Error(`帧校验失败 @frame=${frames}`);
      }
      frames++;
    }
    for (const s of batch) {
      s.release();
    }
  }
  return { frames, peakUsed, peakActive, elapsedMs: performance.now() - t0 };
}

// ---- 预热：摊销一次性初始化，进入稳态 ----
const pool = new RingPool(CAPACITY);
runStream(pool, WARMUP, 1);
pool.bytesWritten = 0;
pool.bytesRead = 0;
gc?.();
await new Promise((r) => setImmediate(r));

// ---- 基线 ----
const baseHeap = process.memoryUsage().heapUsed;
const baseRss = process.memoryUsage().rss;
const baseGc = { ...gcCount };
const baseSlicesAllocated = pool.slicesAllocated;
const baseFramesRead = pool.framesRead;
const baseSlicesReleased = pool.slicesReleased;
const baseExternal = process.memoryUsage().external;

// ---- 正式运行：100MiB ----
const stats = runStream(pool, TOTAL, 20261004);

// ---- 终态：强制 GC 后测量，排除在途垃圾 ----
gc?.();
await new Promise((r) => setImmediate(r));
await new Promise((r) => setImmediate(r));
const endHeap = process.memoryUsage().heapUsed;
const endRss = process.memoryUsage().rss;
const endExternal = process.memoryUsage().external;

// ---- 守恒断言 ----
if (pool.bytesRead !== TOTAL || pool.bytesWritten !== TOTAL) throw new Error('流量不守恒');
if (pool.activeSlices !== 0) throw new Error('存在未释放切片');
if (pool.framesRead === 0 || pool.framesRead % 1 !== 0) throw new Error('unreachable');
if (pool.used !== 0) throw new Error('环形空间未完全回收');

const runGcMajor = gcCount.major - baseGc.major;
const runGcMinor = gcCount.minor - baseGc.minor;
const runSlicesAllocated = pool.slicesAllocated - baseSlicesAllocated;
const fmt = (n: number) => (n / MiB).toFixed(2) + ' MiB';

console.log('========== 环形内存池 · 运行样例 ==========');
console.log(`流量总量        : ${fmt(TOTAL)} (${stats.frames} 帧, 64B~64KiB 随机)`);
console.log(`环形常驻空间    : ${fmt(pool.capacity)} (运行峰值占用 ${fmt(stats.peakUsed)}, padding ${pool.paddingBytes} B)`);
console.log(`吞吐            : ${(TOTAL / MiB / (stats.elapsedMs / 1000)).toFixed(0)} MiB/s (耗时 ${stats.elapsedMs.toFixed(0)} ms, 含随机帧生成与校验)`);
console.log('---------- 零拷贝与守恒 ----------');
console.log(`帧校验          : ${fmt(TOTAL)} 逐字节比对通过（切片视图引用环形区，全程无拷贝）`);
console.log(`切片借出/释放   : ${pool.framesRead - baseFramesRead} / ${pool.slicesReleased - baseSlicesReleased} (终态在途 ${pool.activeSlices})`);
console.log(`切片对象复用    : 正式运行新分配 ${runSlicesAllocated} 个 Slice 对象服务 ${stats.frames} 帧 (峰值在途 ${stats.peakActive})`);
console.log('---------- GC 与堆开销（稳态） ----------');
console.log(`运行期 GC       : major +${runGcMajor}, minor +${runGcMinor} / 100MiB 流量`);
console.log(`heapUsed 变化   : ${fmt(baseHeap)} -> ${fmt(endHeap)} (Δ ${((endHeap - baseHeap) / MiB).toFixed(2)} MiB)`);
console.log(`external 变化   : ${fmt(baseExternal)} -> ${fmt(endExternal)} (Δ ${((endExternal - baseExternal) / MiB).toFixed(2)} MiB, Buffer 堆外内存)`);
console.log(`rss 变化        : ${fmt(baseRss)} -> ${fmt(endRss)} (Δ ${((endRss - baseRss) / MiB).toFixed(2)} MiB)`);
console.log('========== 全部验收断言通过 ==========');
observer.disconnect();
