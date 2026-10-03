/**
 * ChunkPool 单元测试：预分配、复用、耗尽拒绝扩容。
 */
import assert from "node:assert/strict";
import { test } from "node:test";
import { CHUNK_SIZE, ChunkPool } from "../src/chunk.ts";

test("构造期一次性预分配全部 Chunk", () => {
  const pool = new ChunkPool(8);
  const s = pool.stats();
  assert.equal(s.capacity, 8);
  assert.equal(s.freeCount, 8);
  assert.equal(s.inUseCount, 0);
  assert.equal(s.heapAllocations, 8); // 每 Chunk 计一次堆分配
});

test("Chunk 底层存储为定长 Float64Array", () => {
  const pool = new ChunkPool(2);
  const c = pool.acquire();
  assert.equal(c.data.length, CHUNK_SIZE);
  pool.release(c);
});

test("归还后槽位被直接复用，不产生新分配", () => {
  const pool = new ChunkPool(4);
  const first = [pool.acquire(), pool.acquire(), pool.acquire(), pool.acquire()];
  for (const c of first) pool.release(c);

  const again = [pool.acquire(), pool.acquire(), pool.acquire(), pool.acquire()];
  const firstIds = new Set(first.map((c) => c.id));
  for (const c of again) assert.ok(firstIds.has(c.id), "复用的应是原有 Chunk");

  assert.equal(pool.stats().heapAllocations, 4); // 分配计数冻结
  for (const c of again) pool.release(c);
});

test("acquire 时逻辑长度被重置", () => {
  const pool = new ChunkPool(1);
  const c = pool.acquire();
  c.length = 512;
  pool.release(c);
  const c2 = pool.acquire();
  assert.equal(c2.length, 0);
  pool.release(c2);
});

test("池耗尽时抛错而非悄悄扩容（零分配承诺的机制保障）", () => {
  const pool = new ChunkPool(2);
  pool.acquire();
  pool.acquire();
  assert.throws(() => pool.acquire(), /耗尽/);
  assert.equal(pool.stats().heapAllocations, 2); // 未发生任何新分配
});

test("借还计数平衡", () => {
  const pool = new ChunkPool(3);
  const a = pool.acquire();
  const b = pool.acquire();
  pool.release(a);
  pool.release(b);
  const s = pool.stats();
  assert.equal(s.acquires, 2);
  assert.equal(s.releases, 2);
  assert.equal(s.inUseCount, 0);
  assert.equal(s.freeCount, 3);
});

test("非法容量被拒绝", () => {
  assert.throws(() => new ChunkPool(0), RangeError);
  assert.throws(() => new ChunkPool(-1), RangeError);
  assert.throws(() => new ChunkPool(1.5), RangeError);
});
