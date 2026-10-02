import { test } from "node:test";
import assert from "node:assert/strict";
import { RingBufferPool, PoolExhaustedError } from "../src/pool.ts";
import { mulberry32, fnv1a } from "./helpers.ts";

const POOL_BYTES = 4 * 1024 * 1024; // 4MB 常驻环形空间
const STREAM_BYTES = 100 * 1024 * 1024; // 100MB 随机帧流

test("验收: 100MB 随机帧流穿过 4MB 环形池，借用/释放严格守恒", () => {
  const pool = new RingBufferPool(); // 默认 64 块 x 64KB = 4MB
  assert.equal(pool.buffer.byteLength, POOL_BYTES);

  const rand = mulberry32(0x5eed);
  const heapBefore = process.memoryUsage().heapUsed;
  let streamed = 0;
  let frames = 0;
  let writeHash = 0x811c9dc5;
  let readHash = 0x811c9dc5;
  let maxOutstanding = 0;

  while (streamed < STREAM_BYTES) {
    // 随机帧长 512B ~ 32KB（不超过块大小 64KB）
    const length = 512 + Math.floor(rand() * (32 * 1024 - 512));
    const frame = new Uint8Array(length);
    for (let i = 0; i < length; i++) frame[i] = Math.floor(rand() * 256);

    writeHash = fnv1a(frame, writeHash);
    const slice = pool.write(frame);

    // 零拷贝消费：直接在池内内存上校验，不做任何复制
    readHash = fnv1a(slice.view, readHash);
    slice.release();

    streamed += length;
    frames++;
    maxOutstanding = Math.max(maxOutstanding, pool.stats().outstandingSlices);
  }

  // 数据完整性：写入流与读出流逐字节一致
  assert.equal(readHash, writeHash);

  // 借用/释放严格守恒：无泄漏
  const stats = pool.stats();
  assert.equal(stats.outstandingSlices, 0);
  assert.equal(stats.liveReferences, 0);
  assert.equal(stats.referencedBlocks, 0);
  // 除写头块内尚未写满的部分外，全部空间均已回收
  assert.ok(stats.writableBytes >= POOL_BYTES - stats.blockSize);
  assert.equal(stats.slicesCreated, stats.idleSlices); // 全部对象已归还对象池
  assert.ok(maxOutstanding <= 2, `outstanding 峰值应极小, got ${maxOutstanding}`);

  // GC 开销基本为零：Slice 对象被反复复用，创建数远小于帧数
  assert.ok(
    stats.slicesCreated <= 4,
    `slice 对象应被池化复用, 实际创建 ${stats.slicesCreated} 个 / ${frames} 帧`,
  );
  const heapGrowth = process.memoryUsage().heapUsed - heapBefore;
  assert.ok(
    heapGrowth < 16 * 1024 * 1024,
    `100MB 流量下堆增长应趋近于零, 实际 ${(heapGrowth / 1048576).toFixed(2)}MB`,
  );

  console.log(
    `    ${frames} 帧 / ${(streamed / 1048576).toFixed(1)}MB, ` +
      `slice 对象创建 ${stats.slicesCreated} 个, 堆增长 ${(heapGrowth / 1048576).toFixed(2)}MB`,
  );
});

test("切片是池内存的零拷贝视图", () => {
  const pool = new RingBufferPool({ blockSize: 1024, blockCount: 4 });
  const slice = pool.write(new Uint8Array([1, 2, 3, 4]));
  assert.equal(slice.buffer, pool.buffer); // 同一底层 ArrayBuffer，无拷贝
  assert.deepEqual([...slice.view], [1, 2, 3, 4]);
  assert.equal(slice.at(2), 3);
  slice.release();
});

test("引用计数：最后一个 release 才回收底层块", () => {
  const pool = new RingBufferPool({ blockSize: 1024, blockCount: 4 });
  const slice = pool.write(new Uint8Array(100).fill(7));
  slice.retain();
  slice.retain();
  assert.equal(slice.refCount, 3);
  assert.equal(pool.stats().liveReferences, 3);

  slice.release();
  slice.release();
  assert.equal(pool.stats().referencedBlocks, 1); // 仍被引用，块不可复写
  assert.equal(slice.released, false);

  slice.release();
  assert.equal(slice.released, true);
  assert.equal(pool.stats().referencedBlocks, 0);
  assert.equal(pool.stats().outstandingSlices, 0);
});

test("重复 release 抛错，释放后访问抛错", () => {
  const pool = new RingBufferPool({ blockSize: 1024, blockCount: 4 });
  const slice = pool.write(new Uint8Array(10));
  slice.release();
  assert.throws(() => slice.release(), /released more times/);
  assert.throws(() => slice.view, /has been released/);
});

test("背压：消费者不释放时写满即抛 PoolExhaustedError，释放后恢复", () => {
  const pool = new RingBufferPool({ blockSize: 1024, blockCount: 4 });
  const held = [];
  // 4 块池恰好容纳 4 个整块帧，随后写头追上仍被引用的块
  held.push(pool.write(new Uint8Array(1024)));
  held.push(pool.write(new Uint8Array(1024)));
  held.push(pool.write(new Uint8Array(1024)));
  held.push(pool.write(new Uint8Array(1024)));
  assert.equal(pool.writableBytes, 0);
  assert.throws(() => pool.write(new Uint8Array(1024)), PoolExhaustedError);

  held.shift()!.release(); // 释放最旧的块，写入恢复
  held.push(pool.write(new Uint8Array(1024)));
  for (const s of held) s.release();
  assert.equal(pool.writableBytes, 3072); // 写头块已写满，其余 3 块全部回收
});

test("超过块大小的帧被拒绝", () => {
  const pool = new RingBufferPool({ blockSize: 1024, blockCount: 4 });
  assert.throws(() => pool.write(new Uint8Array(1025)), RangeError);
  assert.equal(pool.canWrite(1025), false);
});

test("Symbol.dispose 释放（支持 using 语法）", () => {
  const pool = new RingBufferPool({ blockSize: 1024, blockCount: 4 });
  const slice = pool.write(new Uint8Array(8).fill(1));
  assert.equal(slice.length, 8);
  slice[Symbol.dispose]();
  assert.equal(slice.released, true);
  assert.equal(pool.stats().outstandingSlices, 0);
});

test("乱序释放不会覆盖仍被引用的块", () => {
  const pool = new RingBufferPool({ blockSize: 8, blockCount: 4 });
  const a = pool.write(new Uint8Array([1, 1, 1, 1, 1, 1, 1, 1]));
  const b = pool.write(new Uint8Array([2, 2, 2, 2, 2, 2, 2, 2]));
  const c = pool.write(new Uint8Array([3, 3, 3, 3, 3, 3, 3, 3]));
  b.release(); // 乱序：先释放中间的块（块 1 空闲但不可跳过）
  const d = pool.write(new Uint8Array([4, 4, 4, 4, 4, 4, 4, 4])); // 写入块 3
  // 写头追上 a 所在的块 0：必须背压，而不是跳过到空闲的块 1
  assert.throws(() => pool.write(new Uint8Array(8)), PoolExhaustedError);
  assert.deepEqual([...a.view], [1, 1, 1, 1, 1, 1, 1, 1]); // a 未被破坏
  a.release();
  const e = pool.write(new Uint8Array([5, 5, 5, 5, 5, 5, 5, 5])); // a 释放后写入恢复
  assert.deepEqual([...d.view], [4, 4, 4, 4, 4, 4, 4, 4]);
  assert.deepEqual([...e.view], [5, 5, 5, 5, 5, 5, 5, 5]);
  c.release();
  d.release();
  e.release();
  assert.equal(pool.stats().liveReferences, 0);
});
