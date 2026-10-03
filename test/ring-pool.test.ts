import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { RingPool, Slice } from '../src/ring-pool.ts';

/** 确定性伪随机数（mulberry32），保证测试可复现。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomFrame(rand: () => number, len: number): Uint8Array {
  const buf = new Uint8Array(len);
  for (let i = 0; i < len; i++) buf[i] = (rand() * 256) | 0;
  return buf;
}

/** FNV-1a 32bit 校验和。 */
function fnv1a(data: Uint8Array): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < data.length; i++) {
    h ^= data[i];
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

test('单帧写入/读出，内容零拷贝一致', () => {
  const pool = new RingPool(1024);
  const frame = new Uint8Array([10, 20, 30, 40, 50]);
  assert.equal(pool.writeFrame(frame), true);
  assert.equal(pool.pendingFrames, 1);

  const slice = pool.readFrame();
  assert.ok(slice instanceof Slice);
  assert.equal(slice.byteLength, 5);
  assert.deepEqual(Array.from(slice.view()), [10, 20, 30, 40, 50]);
  assert.equal(slice.get(2), 30);
  slice.release();
  assert.equal(pool.activeSlices, 0);
  assert.equal(pool.used, 0);
});

test('零拷贝：切片视图与环形内存共享同一物理缓冲', () => {
  const pool = new RingPool(4096);
  pool.writeFrame(new Uint8Array([1, 2, 3]));
  const slice = pool.readFrame() as Slice;
  const view = slice.view();
  assert.equal(view.buffer, pool.arena.buffer, '视图必须共享 arena 的 ArrayBuffer（无拷贝）');
  assert.equal(view.byteLength, 3);
  slice.release();
});

test('物理回绕：帧跨界时自动 padding，内容保持正确', () => {
  const pool = new RingPool(1024);
  const rand = mulberry32(7);
  const frames: Uint8Array[] = [];
  // 3 帧 × 300B = 900B；第 4 帧在物理偏移 900 处跨界，应 padding 124B 后从 0 写入
  for (let i = 0; i < 3; i++) {
    const f = randomFrame(rand, 300);
    frames.push(f);
    assert.equal(pool.writeFrame(f), true);
  }
  for (let i = 0; i < 3; i++) {
    const s = pool.readFrame() as Slice;
    assert.equal(fnv1a(s.view()), fnv1a(frames[i]));
    s.release();
  }
  const f4 = randomFrame(rand, 300);
  assert.equal(pool.writeFrame(f4), true);
  assert.equal(pool.paddingBytes, 124, '环尾应插入 1024-900=124 字节 padding');
  const s4 = pool.readFrame() as Slice;
  assert.equal(fnv1a(s4.view()), fnv1a(f4));
  s4.release();
  assert.equal(pool.used, 0);
});

test('背压：空间不足返回 false，释放切片后空间回收', () => {
  const pool = new RingPool(512);
  assert.equal(pool.writeFrame(new Uint8Array(400)), true);
  assert.equal(pool.writeFrame(new Uint8Array(200)), false, '剩余 112B 不足，背压');

  const slice = pool.readFrame() as Slice;
  // 切片未释放，400B 被钉住，仍不可覆盖
  assert.equal(pool.writeFrame(new Uint8Array(200)), false);
  slice.release();
  assert.equal(pool.free, 512);
  assert.equal(pool.writeFrame(new Uint8Array(200)), true, '回收后可继续写入');
  (pool.readFrame() as Slice).release();
});

test('引用计数：retain/release 严格守恒，重复释放抛错', () => {
  const pool = new RingPool(1024);
  pool.writeFrame(new Uint8Array([1, 2, 3]));
  const slice = pool.readFrame() as Slice;
  assert.equal(pool.activeSlices, 1);

  slice.retain();
  slice.release();
  assert.equal(pool.activeSlices, 1, '计数未归零，切片仍在途');
  assert.equal(pool.used, 3, '钉住区间不可回收');

  slice.release();
  assert.equal(pool.activeSlices, 0);
  assert.equal(pool.used, 0);
  assert.throws(() => slice.release(), /released/, '重复释放必须抛错');
  assert.throws(() => slice.get(0), /released/, '释放后访问必须抛错');
  assert.throws(() => slice.view(), /released/);
});

test('乱序释放：水位不越过最老未释放切片', () => {
  const pool = new RingPool(1024);
  pool.writeFrame(new Uint8Array(100).fill(1));
  pool.writeFrame(new Uint8Array(100).fill(2));
  pool.writeFrame(new Uint8Array(100).fill(3));
  const a = pool.readFrame() as Slice;
  const b = pool.readFrame() as Slice;
  const c = pool.readFrame() as Slice;

  c.release();
  b.release();
  assert.equal(pool.used, 300, '最老切片 a 未释放，水位不推进');
  assert.equal(pool.free, 724);
  assert.equal(pool.writeFrame(new Uint8Array(725)), false, '超出可用空间，背压');

  a.release();
  assert.equal(pool.used, 0, '最老切片释放后水位一次性推进');
  assert.equal(pool.free, 1024);
  assert.equal(pool.writeFrame(new Uint8Array(700)), true, '回收后可继续写入');
  (pool.readFrame() as Slice).release();
  assert.equal(pool.used, 0);
});

test('非法输入：空帧与超容量帧抛错', () => {
  const pool = new RingPool(256);
  assert.throws(() => pool.writeFrame(new Uint8Array(0)), /empty/);
  assert.throws(() => pool.writeFrame(new Uint8Array(257)), /exceeds/);
  assert.throws(() => new RingPool(0), /positive integer/);
});

test('验收：100MB 随机帧流经过 4MB 环形池，切片守恒且对象分配有界', () => {
  const CAPACITY = 4 * 1024 * 1024;
  const TOTAL = 100 * 1024 * 1024;
  const pool = new RingPool(CAPACITY);
  const rand = mulberry32(0xc0ffee);

  let produced = 0;
  let frameCount = 0;
  let peakUsed = 0;
  const expected: Buffer[] = []; // 在途帧原件，用于零拷贝校验

  while (pool.bytesRead < TOTAL) {
    // 生产者：尽量写满，背压时转消费
    while (produced < TOTAL) {
      const len = Math.min(64 + ((rand() * 65536) | 0), TOTAL - produced); // 64B ~ 64KB 随机帧
      const frame = randomBytes(len);
      if (!pool.writeFrame(frame)) break;
      expected.push(frame);
      produced += len;
    }
    peakUsed = Math.max(peakUsed, pool.used);
    // 消费者：切出、与原件逐字节比对（零拷贝校验）、释放
    let drained = false;
    let slice: Slice | null;
    while ((slice = pool.readFrame()) !== null) {
      drained = true;
      const want = expected.shift() as Buffer;
      assert.equal(slice.byteLength, want.length);
      assert.deepEqual(slice.view(), want, '帧内容校验失败');
      slice.release();
      frameCount++;
    }
    assert.ok(drained || produced >= TOTAL, '既写不进也读不出：死锁');
  }

  // 守恒校验
  assert.equal(pool.bytesRead, TOTAL, '流出字节必须等于 100MB');
  assert.equal(pool.bytesWritten, TOTAL, '流入字节必须等于 100MB');
  assert.equal(pool.activeSlices, 0, '所有切片必须释放归零');
  assert.equal(pool.framesRead, pool.slicesReleased, '借出与释放严格守恒');
  assert.equal(pool.used, 0, '环形空间全部回收');
  assert.ok(peakUsed <= CAPACITY, `常驻占用 ${peakUsed} 不得超过 4MB`);
  // 对象池复用：Slice 真实分配次数应远小于帧数（稳态在途数级别）
  assert.ok(
    pool.slicesAllocated < 4096,
    `Slice 对象分配 ${pool.slicesAllocated} 次，帧数 ${frameCount}，复用失效`,
  );
  console.log(
    `    [stress] frames=${frameCount} peakUsed=${(peakUsed / 1048576).toFixed(2)}MiB` +
      ` slicesAllocated=${pool.slicesAllocated} padding=${pool.paddingBytes}B`,
  );
});
