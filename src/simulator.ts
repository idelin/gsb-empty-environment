import { CursorError, decodeCursor } from './cursor.ts';
import { EventLog } from './event-log.ts';
import { EventServer } from './server.ts';
import { ResumeClient } from './client.ts';
import { matchesFilter, stateKey, type Filter } from './types.ts';

const FILTER: Filter = { topics: ['orders'] };

function assert(condition: boolean, message: string): void {
  if (!condition) throw new Error(`断言失败: ${message}`);
  console.log(`  ✓ ${message}`);
}

async function main(): Promise<void> {
  const log = new EventLog();
  // 快照生成期间继续产生 3 个事件，模拟慢快照与并发写入。
  const server = new EventServer(log, {
    onSnapshotBuild: () => {
      for (let i = 0; i < 3; i += 1) {
        log.append('orders', `order-snap-${i}`, { status: 'created' });
      }
    },
  });
  const client = new ResumeClient(FILTER, 3); // 每 3 条持久化一次游标

  console.log('阶段 1：客户端订阅（空日志快照），消费 10 条事件后断线');
  const consuming = client.consume(server, 11); // 1 条快照 + 10 条事件
  for (let i = 0; i < 20; i += 1) {
    log.append('orders', `order-${i}`, { status: 'created' });
    log.append('metrics', `cpu-${i}`, i);
  }
  await consuming;
  const lastSeqAtDisconnect = client.lastSeq;
  const persistedAtDisconnect = decodeCursor(client.persistedCursor!).seq;
  console.log(
    `  断线时 lastSeq=${lastSeqAtDisconnect}，持久化游标 seq=${persistedAtDisconnect}`,
  );
  assert(
    persistedAtDisconnect < lastSeqAtDisconnect,
    '持久化游标落后于已应用位置（崩溃前未检查点）',
  );

  console.log('阶段 2：持陈旧但有效的游标重连 → 精确重放，重复可识别');
  await client.consumeUntilLive(server);
  assert(client.gaps.length === 0, '重放无缺口');
  assert(
    client.duplicates.length > 0,
    `重复事件被明确识别（seq=${client.duplicates
      .map((d) => d.seq)
      .join(',')}）`,
  );
  assert(
    client.duplicates.every((d) => d.seq <= lastSeqAtDisconnect),
    '重复事件的 seq 均不超过断线时已应用的位置',
  );

  console.log('阶段 3：服务端压缩旧记录，持久化游标随之过旧');
  const staleSeq = decodeCursor(client.persistedCursor!).seq;
  log.compact(staleSeq + 5);
  console.log(
    `  持久化游标 seq=${staleSeq}，压缩水位线=${log.getCompactedBefore()}`,
  );
  assert(
    staleSeq < log.getCompactedBefore(),
    '游标已落在压缩水位线之前（过旧）',
  );
  for (let i = 20; i < 30; i += 1) {
    log.append('orders', `order-${i}`, { status: 'created' });
  }

  console.log('阶段 4：持过旧游标重连 → 快照 + 增量起点');
  const gapsBefore = client.gaps.length;
  const dupsBefore = client.duplicates.length;
  await client.consumeUntilLive(server);
  assert(client.gaps.length === gapsBefore, '快照/增量切换处无缺口');
  assert(
    client.duplicates.length === dupsBefore,
    '快照路径不产生无法解释的重复',
  );
  assert(
    client.lastSeq === log.getLatestSeq(),
    `客户端已追平至最新 seq=${log.getLatestSeq()}`,
  );

  console.log('阶段 5：校验客户端状态与服务端日志一致');
  const expected = log.stateAt(log.getLatestSeq());
  for (const entry of expected.values()) {
    if (!matchesFilter(entry, FILTER)) continue;
    const actual = client.state.get(stateKey(entry.topic, entry.key));
    assert(
      actual !== undefined &&
        JSON.stringify(actual.value) === JSON.stringify(entry.value),
      `状态一致 ${entry.topic}:${entry.key}`,
    );
  }
  const expectedCount = [...expected.values()].filter((e) =>
    matchesFilter(e, FILTER),
  ).length;
  assert(client.state.size === expectedCount, '客户端状态条目数与服务端一致');

  console.log('阶段 6：游标不可跨订阅条件复用');
  const staleCursor = client.persistedCursor!;
  let rejected = false;
  try {
    const stream = server.subscribe({ topics: ['metrics'] }, staleCursor);
    await stream.next();
  } catch (error) {
    rejected =
      error instanceof CursorError && error.code === 'CURSOR_FILTER_MISMATCH';
  }
  assert(rejected, '跨订阅条件复用游标被拒绝（CURSOR_FILTER_MISMATCH）');

  console.log('\n全部断言通过。');
}

await main();
