import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Channel, ChannelClosedError, go } from '../src/channel.ts';
import { RingBuffer } from '../src/ring-buffer.ts';

/** 让出事件循环一轮，使已 resolve 的协程继续推进。 */
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

test('验收边界：容量2写3个数据，第3次写挂起，读1个后精确唤醒，关闭后读协程收到EOF', async () => {
  const ch = new Channel<number>(2);
  const sent: number[] = [];
  let producerDone = false;

  // 协程 P：连续写入 3 个数据
  const producer = go(async () => {
    for (const v of [1, 2, 3]) {
      await ch.send(v);
      sent.push(v);
    }
    producerDone = true;
  });

  await tick();
  // 前 2 个进入缓冲区，第 3 次 send 时缓冲区满，P 自动挂起
  assert.deepEqual(sent, [1, 2]);
  assert.equal(producerDone, false);
  assert.equal(ch.pendingSends, 1);

  // 消费者 C 读取 1 个数据 → P 被精确唤醒并完成第 3 次写入
  const first = await ch.recv();
  assert.deepEqual(first, { value: 1, ok: true });
  await tick();
  assert.equal(producerDone, true);
  assert.deepEqual(sent, [1, 2, 3]);
  assert.equal(ch.pendingSends, 0);

  // 排空剩余数据
  assert.deepEqual(await ch.recv(), { value: 2, ok: true });
  assert.deepEqual(await ch.recv(), { value: 3, ok: true });

  // 两个读协程在空通道上挂起
  const eofResults: Array<{ value: unknown; ok: boolean }> = [];
  const reader1 = go(async () => { eofResults.push(await ch.recv()); });
  const reader2 = go(async () => { eofResults.push(await ch.recv()); });
  await tick();
  assert.equal(ch.pendingRecvs, 2);

  // 关闭通道 → 所有等待读协程收到 EOF 广播
  ch.close();
  await Promise.all([producer, reader1, reader2]);
  assert.equal(eofResults.length, 2);
  assert.ok(eofResults.every((r) => r.ok === false && r.value === undefined));
});

test('无缓冲通道：send 与 recv  rendezvous 交接', async () => {
  const ch = new Channel<string>(0);
  const order: string[] = [];

  const sender = go(async () => {
    order.push('send:start');
    await ch.send('hello');
    order.push('send:done');
  });

  await tick();
  // 无缓冲且无接收者：send 挂起
  assert.deepEqual(order, ['send:start']);
  assert.equal(ch.pendingSends, 1);

  const received = await ch.recv();
  assert.equal(received.value, 'hello');
  await sender;
  assert.deepEqual(order, ['send:start', 'send:done']);
});

test('精确唤醒：多个阻塞发送者按 FIFO 顺序逐个唤醒', async () => {
  const ch = new Channel<number>(1);
  await ch.send(0); // 占满缓冲区

  const wakeOrder: number[] = [];
  const senders = [1, 2, 3].map((v) =>
    go(async () => {
      await ch.send(v);
      wakeOrder.push(v);
    }),
  );
  await tick();
  assert.equal(ch.pendingSends, 3);

  // 每次 recv 只唤醒队首一个发送者
  for (const expected of [0, 1, 2]) {
    const r = await ch.recv();
    assert.equal(r.value, expected);
    await tick();
  }
  await Promise.all(senders);
  assert.deepEqual(wakeOrder, [1, 2, 3]);
  assert.equal((await ch.recv()).value, 3);
});

test('精确唤醒：多个阻塞接收者按 FIFO 获得数据，无惊群', async () => {
  const ch = new Channel<number>(0);
  const got: Array<[number, number]> = [];

  const readers = [0, 1, 2].map((id) =>
    go(async () => {
      const r = await ch.recv();
      if (r.ok) got.push([id, r.value as number]);
    }),
  );
  await tick();
  assert.equal(ch.pendingRecvs, 3);

  await ch.send(100);
  await tick();
  assert.equal(ch.pendingRecvs, 2); // 只唤醒一个
  await ch.send(200);
  await ch.send(300);
  await Promise.all(readers);
  assert.deepEqual(got, [[0, 100], [1, 200], [2, 300]]);
});

test('关闭后：缓冲区数据仍可读出，排空后才返回 EOF', async () => {
  const ch = new Channel<number>(2);
  await ch.send(1);
  await ch.send(2);
  ch.close();

  assert.deepEqual(await ch.recv(), { value: 1, ok: true });
  assert.deepEqual(await ch.recv(), { value: 2, ok: true });
  assert.deepEqual(await ch.recv(), { value: undefined, ok: false });
  assert.deepEqual(await ch.recv(), { value: undefined, ok: false }); // 幂等 EOF
});

test('关闭语义：向已关闭通道 send 抛错，阻塞中的 send 被唤醒并抛错，重复 close 抛错', async () => {
  const ch = new Channel<number>(1);
  await ch.send(1); // 占满

  let blockedError: unknown;
  const blocked = go(async () => {
    try {
      await ch.send(2);
    } catch (err) {
      blockedError = err;
    }
  });
  await tick();
  assert.equal(ch.pendingSends, 1);

  ch.close();
  await blocked;
  assert.ok(blockedError instanceof ChannelClosedError);
  await assert.rejects(ch.send(3), ChannelClosedError);
  assert.throws(() => ch.close(), ChannelClosedError);
});

test('无锁环形队列：回绕读写保持 FIFO 且满时拒绝入队', () => {
  const rb = new RingBuffer<number>(2);
  assert.equal(rb.push(1), true);
  assert.equal(rb.push(2), true);
  assert.equal(rb.push(3), false); // 满
  assert.equal(rb.isFull(), true);

  assert.equal(rb.pop(), 1);
  assert.equal(rb.push(3), true); // 回绕复用槽位
  assert.equal(rb.pop(), 2);
  assert.equal(rb.pop(), 3);
  assert.equal(rb.pop(), undefined); // 空
  assert.equal(rb.isEmpty(), true);

  // 多轮回绕后仍保持顺序
  for (let round = 0; round < 100; round++) {
    assert.equal(rb.push(round), true);
    assert.equal(rb.pop(), round);
  }
});

test('无锁环形队列：容量 0 恒满（支撑无缓冲通道）', () => {
  const rb = new RingBuffer<number>(0);
  assert.equal(rb.isFull(), true);
  assert.equal(rb.push(1), false);
  assert.equal(rb.pop(), undefined);
});
