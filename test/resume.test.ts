import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CursorError,
  decodeCursor,
  encodeCursor,
  validateCursorForFilter,
} from '../src/cursor.ts';
import { EventLog } from '../src/event-log.ts';
import { EventServer, type ServerMessage } from '../src/server.ts';
import { ResumeClient } from '../src/client.ts';
import { matchesFilter, stateKey, type Filter } from '../src/types.ts';

const ORDERS: Filter = { topics: ['orders'] };

function makeLog(): EventLog {
  const log = new EventLog();
  for (let i = 0; i < 10; i += 1) {
    log.append('orders', `order-${i}`, { status: 'created' });
    log.append('metrics', `cpu-${i}`, i);
  }
  return log;
}

async function collect(
  server: EventServer,
  filter: Filter,
  cursor: string | undefined,
  stopAtLive = true,
): Promise<ServerMessage[]> {
  const messages: ServerMessage[] = [];
  for await (const message of server.subscribe(filter, cursor)) {
    messages.push(message);
    if (stopAtLive && message.type === 'live') break;
  }
  return messages;
}

test('有效游标：resumed + 精确重放缺失事件，不发快照', async () => {
  const log = makeLog();
  const server = new EventServer(log);
  const cursor = encodeCursor(ORDERS, 5);
  const messages = await collect(server, ORDERS, cursor);

  assert.equal(messages[0].type, 'resumed');
  assert.equal((messages[0] as { fromSeq: number }).fromSeq, 5);
  assert.ok(messages.every((m) => m.type !== 'snapshot'));
  const events = messages.filter((m) => m.type === 'event');
  const ordersSeqs = [7, 9, 11, 13, 15, 17, 19]; // seq>5 的 orders 事件
  assert.deepEqual(
    events.map((m) => (m as { event: { seq: number } }).event.seq),
    ordersSeqs,
  );
  // 校验链连续：首条 prevSeq = 游标 seq，之后依次衔接
  let expectedPrev = 5;
  for (const message of events) {
    const { prevSeq, event } = message as {
      prevSeq: number;
      event: { seq: number };
    };
    assert.equal(prevSeq, expectedPrev);
    expectedPrev = event.seq;
  }
  assert.equal(messages.at(-1)!.type, 'live');
});

test('过旧游标：返回快照 + 增量起点，快照期间事件不遗漏', async () => {
  const log = makeLog();
  // 快照生成期间继续产生 2 个 orders 事件
  const server = new EventServer(log, {
    onSnapshotBuild: () => {
      log.append('orders', 'order-snap-0', { status: 'created' });
      log.append('orders', 'order-snap-1', { status: 'created' });
    },
  });
  log.compact(15); // 压缩掉 seq<15，游标 seq=3 过旧
  const cursor = encodeCursor(ORDERS, 3);
  const messages = await collect(server, ORDERS, cursor);

  const snapshot = messages[0];
  assert.equal(snapshot.type, 'snapshot');
  const { atSeq, entries } = snapshot as {
    atSeq: number;
    entries: { topic: string; key: string }[];
    cursor: string;
  };
  assert.ok(atSeq >= log.getCompactedBefore());
  assert.ok(entries.every((e) => e.topic === 'orders'));
  // 快照期间产生的事件不在快照里，必须出现在增量中
  assert.ok(!entries.some((e) => e.key === 'order-snap-0'));
  const events = messages.filter((m) => m.type === 'event');
  const seqs = events.map((m) => (m as { event: { seq: number } }).event.seq);
  assert.deepEqual(seqs, [21, 22]); // 快照期间追加的两个事件
  // 校验链从快照 atSeq 开始连续
  let expectedPrev = atSeq;
  for (const message of events) {
    assert.equal((message as { prevSeq: number }).prevSeq, expectedPrev);
    expectedPrev = (message as { event: { seq: number } }).event.seq;
  }
  // 快照游标可用于下次续传
  assert.equal(decodeCursor((snapshot as { cursor: string }).cursor).seq, atSeq);
});

test('切换处无遗漏：客户端链校验为空，状态与日志一致', async () => {
  const log = makeLog();
  const server = new EventServer(log, {
    onSnapshotBuild: () => {
      for (let i = 0; i < 5; i += 1) {
        log.append('orders', `order-snap-${i}`, i);
      }
    },
  });
  const client = new ResumeClient(ORDERS);
  await client.consumeUntilLive(server);
  log.compact(client.lastSeq + 1); // 压缩掉客户端已见的全部事件
  for (let i = 0; i < 5; i += 1) {
    log.append('orders', `order-late-${i}`, i);
  }
  await client.consumeUntilLive(server); // 过旧游标 → 快照恢复

  assert.deepEqual(client.gaps, []);
  assert.equal(client.lastSeq, log.getLatestSeq());
  const expected = log.stateAt(log.getLatestSeq());
  for (const entry of expected.values()) {
    if (!matchesFilter(entry, ORDERS)) continue;
    const actual = client.state.get(stateKey(entry.topic, entry.key));
    assert.ok(actual, `缺少状态 ${entry.topic}:${entry.key}`);
    assert.deepEqual(actual.value, entry.value);
  }
});

test('重复事件可明确识别：陈旧游标重连产生重复，且不重复应用', async () => {
  const log = makeLog();
  const server = new EventServer(log);
  const client = new ResumeClient(ORDERS, 3); // 每 3 条检查点一次
  // 订阅后并发产生 5 个事件：快照 + 5 条事件后断线
  const consuming = client.consume(server, 6);
  for (let i = 10; i < 15; i += 1) {
    log.append('orders', `order-${i}`, i);
  }
  await consuming;
  const lastSeq = client.lastSeq;
  const persistedSeq = decodeCursor(client.persistedCursor!).seq;
  assert.ok(persistedSeq < lastSeq); // 持久化游标落后

  await client.consumeUntilLive(server); // 重连
  assert.deepEqual(client.gaps, []);
  assert.ok(client.duplicates.length > 0);
  for (const dup of client.duplicates) {
    assert.ok(dup.seq <= lastSeq); // 重复 = 已应用过的 seq
  }
  // 已应用事件无重复 seq
  const appliedSeqs = client.applied.map((e) => e.seq);
  assert.equal(new Set(appliedSeqs).size, appliedSeqs.length);
});

test('游标不可跨订阅条件复用', async () => {
  const log = makeLog();
  const server = new EventServer(log);
  const cursor = encodeCursor(ORDERS, 10);
  await assert.rejects(
    async () => {
      for await (const message of server.subscribe({ topics: ['metrics'] }, cursor)) {
        void message;
      }
    },
    (error: unknown) =>
      error instanceof CursorError && error.code === 'CURSOR_FILTER_MISMATCH',
  );
  // 等价条件的不同字面量（顺序不同）可以复用
  const reordered = encodeCursor({ topics: ['a', 'b'] }, 1);
  assert.equal(validateCursorForFilter(reordered, { topics: ['b', 'a'] }, 0), 1);
});

test('畸形与不支持版本的游标被拒绝', async () => {
  const log = makeLog();
  const server = new EventServer(log);
  for (const bad of ['not-a-cursor', '%%%', Buffer.from('{}').toString('base64url')]) {
    await assert.rejects(
      async () => {
        for await (const message of server.subscribe(ORDERS, bad)) {
          void message;
        }
      },
      (error: unknown) =>
        error instanceof CursorError && error.code === 'CURSOR_MALFORMED',
    );
  }
  const futureVersion = Buffer.from(
    JSON.stringify({ v: 999, filterHash: 'x', seq: 1 }),
  ).toString('base64url');
  await assert.rejects(
    async () => {
      for await (const message of server.subscribe(ORDERS, futureVersion)) {
        void message;
      }
    },
    (error: unknown) =>
      error instanceof CursorError &&
      error.code === 'CURSOR_UNSUPPORTED_VERSION',
  );
});

test('压缩水位线校验：CURSOR_TOO_OLD 由服务端转为快照恢复', () => {
  const log = makeLog();
  log.compact(10);
  const cursor = encodeCursor(ORDERS, 5);
  assert.throws(
    () => validateCursorForFilter(cursor, ORDERS, log.getCompactedBefore()),
    (error: unknown) =>
      error instanceof CursorError && error.code === 'CURSOR_TOO_OLD',
  );
  // 水位线边界：seq == compactedBefore 仍有效
  const edge = encodeCursor(ORDERS, 10);
  assert.equal(validateCursorForFilter(edge, ORDERS, 10), 10);
});
