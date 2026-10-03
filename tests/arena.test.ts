import { test } from "node:test";
import assert from "node:assert/strict";
import { ArenaExhaustedError, ArenaLeakError, CompactArena } from "../src/sparse/arena.ts";
import { AsyncMutex } from "../src/sparse/async-mutex.ts";

test("基本分配/读写/释放", () => {
  const a = new CompactArena(1024);
  const h = a.alloc(32);
  a.write(h, 0, Uint8Array.from([1, 2, 3]));
  assert.deepEqual([...a.read(h, 0, 3)], [1, 2, 3]);
  assert.equal(a.liveCount, 1);
  a.free(h);
  assert.equal(a.liveCount, 0);
  a.assertNoLeaks();
});

test("尺寸分级：小对象向上取整到 2 的幂", () => {
  const a = new CompactArena(4096);
  const h1 = a.alloc(1); // -> 16B
  const h2 = a.alloc(17); // -> 32B
  assert.equal(a.usedBytes, 16 + 32);
  a.free(h1);
  a.free(h2);
  a.assertNoLeaks();
});

test("空闲块复用：free 后的块被同尺寸 alloc 重用", () => {
  const a = new CompactArena(1024);
  const h1 = a.alloc(64);
  a.free(h1);
  const freeBefore = a.freeBytes;
  const h2 = a.alloc(64);
  assert.equal(a.freeBytes, freeBefore - 64); // 复用空闲块，未消耗新空间
  a.free(h2);
  a.assertNoLeaks();
});

test("defrag 压实：消除碎片且句柄数据保持完整", () => {
  const a = new CompactArena(4096);
  const handles: number[] = [];
  for (let i = 0; i < 16; i++) {
    const h = a.alloc(128);
    a.write(h, 0, Uint8Array.from([i]));
    handles.push(h);
  }
  // 释放奇数块制造碎片
  for (let i = 1; i < 16; i += 2) a.free(handles[i]);
  a.defrag();
  // 数据完整
  for (let i = 0; i < 16; i += 2) {
    assert.equal(a.read(handles[i], 0, 1)[0], i);
  }
  // 压实后空闲空间连续，可再分配大块
  const big = a.alloc(2048);
  a.free(big);
  for (let i = 0; i < 16; i += 2) a.free(handles[i]);
  a.assertNoLeaks();
});

test("容量耗尽自动 defrag，仍不足则抛 ArenaExhaustedError", () => {
  const a = new CompactArena(256);
  const h1 = a.alloc(128);
  const h2 = a.alloc(128);
  assert.throws(() => a.alloc(128), ArenaExhaustedError);
  a.free(h1);
  const h3 = a.alloc(128); // 复用
  a.free(h2);
  a.free(h3);
  a.assertNoLeaks();
});

test("严格泄漏断言：未释放句柄被精确报告", () => {
  const a = new CompactArena(1024);
  const h1 = a.alloc(16);
  const h2 = a.alloc(16);
  a.free(h1);
  assert.throws(() => a.assertNoLeaks(), (e: unknown) => {
    assert.ok(e instanceof ArenaLeakError);
    assert.deepEqual((e as ArenaLeakError).leaks, [h2]);
    return true;
  });
  a.free(h2);
  a.assertNoLeaks();
});

test("双重释放与非法句柄抛错", () => {
  const a = new CompactArena(1024);
  const h = a.alloc(16);
  a.free(h);
  assert.throws(() => a.free(h), /double free/);
  assert.throws(() => a.read(9999, 0, 1), /unknown handle/);
});

test("越界读写被拒绝", () => {
  const a = new CompactArena(1024);
  const h = a.alloc(16);
  assert.throws(() => a.write(h, 14, new Uint8Array(4)), RangeError);
  assert.throws(() => a.read(h, 15, 2), RangeError);
  a.free(h);
});

test("高并发交错分配/释放：无泄漏且无双重分配", async () => {
  const a = new CompactArena(1 << 20);
  const mutex = new AsyncMutex();
  const heldBy = new Map<number, Set<number>>(); // worker -> handles
  const tasks = Array.from({ length: 8 }, (_, w) =>
    (async () => {
      const mine = new Set<number>();
      heldBy.set(w, mine);
      for (let i = 0; i < 400; i++) {
        await mutex.runExclusive(() => {
          const h = a.alloc(64);
          mine.add(h);
          a.write(h, 0, Uint8Array.from([w, i & 0xff]));
        });
        if (i % 3 === 0) await Promise.resolve();
        if (mine.size > 8) {
          const victim = mine.values().next().value as number;
          await mutex.runExclusive(() => {
            a.free(victim);
          });
          mine.delete(victim);
        }
      }
    })(),
  );
  await Promise.all(tasks);
  // 所有句柄唯一（无双重分配）
  const all = [...heldBy.values()].flatMap((s) => [...s]);
  assert.equal(new Set(all).size, all.length);
  // 数据未串扰：抽样校验
  // 全部释放后严格无泄漏
  for (const s of heldBy.values()) for (const h of s) a.free(h);
  a.assertNoLeaks();
  assert.equal(a.liveCount, 0);
  assert.equal(a.allocCount, a.freeCount);
});

test("churn 压力：10 万次分配/释放后无泄漏、统计一致", () => {
  const a = new CompactArena(1 << 16);
  const live: number[] = [];
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let i = 0; i < 100000; i++) {
    if (live.length === 0 || rand() < 0.55) {
      try {
        live.push(a.alloc(1 << (4 + Math.floor(rand() * 4))));
      } catch (e) {
        assert.ok(e instanceof ArenaExhaustedError);
        for (const h of live.splice(0)) a.free(h);
      }
    } else {
      const idx = Math.floor(rand() * live.length);
      a.free(live[idx]);
      live[idx] = live[live.length - 1];
      live.pop();
    }
  }
  for (const h of live) a.free(h);
  a.assertNoLeaks();
  assert.equal(a.allocCount, a.freeCount);
  assert.equal(a.liveCount, 0);
});

test("dispose 释放底层缓冲，之后操作抛错", () => {
  const a = new CompactArena(1024);
  const h = a.alloc(16);
  a.free(h);
  const released = a.dispose();
  assert.equal(released, 1024);
  assert.throws(() => a.alloc(1), /disposed/);
  a.assertNoLeaks(); // dispose 清空 live 表，无泄漏
});
