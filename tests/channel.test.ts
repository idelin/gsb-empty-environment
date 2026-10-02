import { test } from "node:test";
import assert from "node:assert/strict";
import { Channel, chan } from "../src/channel.ts";
import { RingBuffer } from "../src/ring-buffer.ts";

/** 让出事件循环若干轮，使所有已就绪的协程推进到下一个挂起点。 */
function tick(times = 1): Promise<void> {
  return new Promise((resolve) => {
    const step = (n: number) => (n <= 0 ? resolve() : setImmediate(() => step(n - 1)));
    step(times);
  });
}

test("验收场景：容量2写3个，P挂起，C读1个后P被精确唤醒，close广播EOF", async () => {
  const ch = new Channel<number>(2);
  const events: string[] = [];

  // 生产者 P：连续写入 1、2、3
  const P = (async () => {
    for (const v of [1, 2, 3]) {
      await ch.send(v);
      events.push(`P sent ${v}`);
    }
    events.push("P done");
  })();

  await tick();
  // 前两个进入缓冲区，第三个使缓冲区满 -> P 自动挂起
  assert.deepEqual(events, ["P sent 1", "P sent 2"]);
  assert.equal(ch.length, 2);
  assert.equal(ch.pendingSenders, 1);

  // 消费者 C：读取一个数据
  const C = (async () => {
    const r = await ch.recv();
    assert.equal(r.done, false);
    events.push(`C recv ${r.value}`);
  })();

  await tick();
  // C 读到 1；P 被立即唤醒完成第 3 次写入
  assert.ok(events.includes("C recv 1"), "C 应读到 1");
  assert.ok(events.includes("P sent 3"), "P 应被唤醒并完成第 3 次写入");
  assert.ok(events.includes("P done"), "P 协程应执行完毕");
  assert.equal(ch.pendingSenders, 0);
  await Promise.all([P, C]);

  // 主协程先耗尽缓冲区剩余数据（2、3）
  const d1 = await ch.recv();
  const d2 = await ch.recv();
  assert.deepEqual([d1, d2], [
    { value: 2, done: false },
    { value: 3, done: false },
  ]);
  assert.equal(ch.length, 0);

  // 再启动两个等待读的协程，close 后都应收到 EOF
  const r1Promise = (async () => {
    const r = await ch.recv();
    return r.done ? "R1 EOF" : "R1 got data";
  })();
  const r2Promise = (async () => {
    const r = await ch.recv();
    return r.done ? "R2 EOF" : "R2 got data";
  })();
  await tick();
  assert.equal(ch.pendingReceivers >= 1, true);

  ch.close();
  const [r1, r2] = await Promise.all([r1Promise, r2Promise]);
  assert.equal(r1, "R1 EOF");
  assert.equal(r2, "R2 EOF");

  // 关闭后再读，立即返回 EOF
  const after = await ch.recv();
  assert.equal(after.done, true);
});

test("无缓冲 Channel：发送方挂起直到接收方就绪（直接交接）", async () => {
  const ch = chan<string>(); // capacity = 0
  const events: string[] = [];

  const sender = (async () => {
    await ch.send("hello");
    events.push("sent");
  })();

  await tick();
  assert.deepEqual(events, []); // 无缓冲且无接收者 -> 挂起
  assert.equal(ch.pendingSenders, 1);

  const received = (async () => {
    const r = await ch.recv();
    events.push(`recv ${r.value}`);
  })();

  await tick();
  // 双方均完成：接收者拿到值，发送者从挂起中被唤醒
  assert.equal(events.length, 2);
  assert.ok(events.includes("sent"));
  assert.ok(events.includes("recv hello"));
  await Promise.all([sender, received]);
});

test("无缓冲 Channel：接收方先挂起，发送方到达时精确唤醒", async () => {
  const ch = chan<number>();
  const order: string[] = [];

  const receiver = (async () => {
    const r = await ch.recv();
    order.push(`recv=${r.value}`);
  })();
  await tick();
  assert.equal(ch.pendingReceivers, 1);

  const sender = (async () => {
    await ch.send(42);
    order.push("send done");
  })();
  await tick();

  assert.deepEqual(order, ["send done", "recv=42"]);
  await Promise.all([sender, receiver]);
});

test("FIFO：多个等待接收者按序精确唤醒，一值只交付一人", async () => {
  const ch = chan<number>();
  const got: string[] = [];
  const mk = (name: string) =>
    (async () => {
      const r = await ch.recv();
      got.push(`${name}:${r.value}`);
    })();
  const a = mk("A");
  const b = mk("B");
  await tick();
  assert.equal(ch.pendingReceivers, 2);

  await ch.send(1);
  await ch.send(2);
  await tick();
  assert.deepEqual(got, ["A:1", "B:2"]);
  await Promise.all([a, b]);
});

test("close 广播：所有等待读协程同时收到 EOF", async () => {
  const ch = chan<number>();
  const results: string[] = [];
  const waiters = [1, 2, 3].map((i) =>
    (async () => {
      const r = await ch.recv();
      results.push(r.done ? `w${i}:EOF` : `w${i}:data`);
    })(),
  );
  await tick();
  assert.equal(ch.pendingReceivers, 3);

  ch.close();
  await Promise.all(waiters);
  assert.deepEqual(results.sort(), ["w1:EOF", "w2:EOF", "w3:EOF"]);
});

test("close 后缓冲区剩余数据仍可消费，之后才 EOF", async () => {
  const ch = new Channel<number>(2);
  await ch.send(1);
  await ch.send(2);
  ch.close();

  const r1 = await ch.recv();
  const r2 = await ch.recv();
  const r3 = await ch.recv();
  assert.deepEqual([r1, r2], [
    { value: 1, done: false },
    { value: 2, done: false },
  ]);
  assert.equal(r3.done, true);
});

test("向已关闭的 Channel 发送抛错", async () => {
  const ch = chan<number>(1);
  ch.close();
  await assert.rejects(() => ch.send(1), /send on closed channel/);
});

test("for await 迭代至通道关闭自动结束", async () => {
  const ch = new Channel<number>(3);
  const collected: number[] = [];
  const consumer = (async () => {
    for await (const v of ch) collected.push(v);
  })();

  await ch.send(1);
  await ch.send(2);
  await ch.send(3);
  ch.close();
  await consumer;
  assert.deepEqual(collected, [1, 2, 3]);
});

test("RingBuffer：定容、回绕、满/空边界", () => {
  const rb = new RingBuffer<number>(3);
  assert.equal(rb.isEmpty, true);
  assert.equal(rb.shift(), undefined);

  assert.equal(rb.push(1), true);
  assert.equal(rb.push(2), true);
  assert.equal(rb.push(3), true);
  assert.equal(rb.isFull, true);
  assert.equal(rb.push(4), false); // 满时不覆盖

  assert.equal(rb.shift(), 1);
  assert.equal(rb.push(4), true); // 回绕复用槽位
  assert.deepEqual([rb.shift(), rb.shift(), rb.shift()], [2, 3, 4]);
  assert.equal(rb.isEmpty, true);
  assert.equal(rb.size, 0);
});
