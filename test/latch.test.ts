import { test } from "node:test";
import assert from "node:assert/strict";
import { ReadWriteLatch } from "../src/latch.ts";

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

test("写闩互斥：任意时刻最多一个写者", async () => {
  const latch = new ReadWriteLatch();
  let active = 0;
  let maxActive = 0;
  await Promise.all(
    Array.from({ length: 8 }, async () => {
      await latch.acquireWrite();
      active++;
      maxActive = Math.max(maxActive, active);
      await sleep(5);
      active--;
      latch.releaseWrite();
    }),
  );
  assert.equal(maxActive, 1);
  assert.ok(latch.isIdle);
});

test("读闩共享：多个读者可同时持有", async () => {
  const latch = new ReadWriteLatch();
  let active = 0;
  let maxActive = 0;
  await Promise.all(
    Array.from({ length: 8 }, async () => {
      await latch.acquireRead();
      active++;
      maxActive = Math.max(maxActive, active);
      await sleep(5);
      active--;
      latch.releaseRead();
    }),
  );
  assert.ok(maxActive > 1, `expected concurrent readers, got ${maxActive}`);
  assert.ok(latch.isIdle);
});

test("FIFO 公平：写者排队后，后来的读者不得插队", async () => {
  const latch = new ReadWriteLatch();
  const order: string[] = [];
  await latch.acquireRead(); // 主线程持有读闩

  const writer = (async () => {
    await latch.acquireWrite();
    order.push("writer");
    latch.releaseWrite();
  })();
  const reader = (async () => {
    await latch.acquireRead();
    order.push("reader");
    latch.releaseRead();
  })();

  await sleep(20); // 让写者、读者先后入队
  assert.equal(latch.waitingCount, 2);
  latch.releaseRead(); // 释放后应先唤醒写者
  await Promise.all([writer, reader]);
  assert.deepEqual(order, ["writer", "reader"]);
  assert.ok(latch.isIdle);
});

test("读写互斥：写者持闩期间读者必须等待", async () => {
  const latch = new ReadWriteLatch();
  const events: string[] = [];
  await latch.acquireWrite();
  const reader = (async () => {
    await latch.acquireRead();
    events.push("read-acquired");
    latch.releaseRead();
  })();
  await sleep(20);
  events.push("write-held");
  assert.deepEqual(events, ["write-held"]); // 读者仍被阻塞
  latch.releaseWrite();
  await reader;
  assert.deepEqual(events, ["write-held", "read-acquired"]);
});
