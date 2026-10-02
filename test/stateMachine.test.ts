import { test } from 'node:test';
import assert from 'node:assert/strict';

import { StateMachine, TIE_BREAK_RULE } from '../src/stateMachine.ts';
import { buildLog, shuffledDelivery, PARTITION_COUNT, SNAPSHOT_AT } from '../src/scenario.ts';
import type { LogEvent, TieBreakEvidence } from '../src/types.ts';

const OPTS = { partitionCount: PARTITION_COUNT };

function replay(events: LogEvent[]): StateMachine {
  const sm = new StateMachine(OPTS);
  for (const e of events) sm.ingest(e);
  return sm;
}

test('乱序网络包：并发交错的 T1/T2 产生相同最终状态', () => {
  const log = buildLog();
  const inOrder = replay(log);

  for (const seed of [1, 7, 42, 2024, 999983]) {
    const shuffled = replay(shuffledDelivery(log, seed));
    assert.equal(
      shuffled.canonicalState(),
      inOrder.canonicalState(),
      `seed=${seed} 的最终键值状态必须一致`,
    );
    assert.deepEqual(shuffled.committed, inOrder.committed, '提交顺序必须确定');
    assert.deepEqual(
      shuffled.evidence.map((e) => e.kind),
      inOrder.evidence.map((e) => e.kind),
      '证据流必须确定',
    );
  }
  // 具体终态断言：T1 胜出，T2 被决胜中止。
  assert.equal(inOrder.get(0, 'shared'), 'T1');
  assert.equal(inOrder.get(0, 'b'), undefined, 'T2 的写不得生效');
  assert.equal(inOrder.get(1, 'x'), '1');
  assert.equal(inOrder.get(1, 'z'), '3');
});

test('未决冲突：输出可审计决胜证据且内容确定', () => {
  const sm = replay(buildLog());
  const tieBreaks = sm.evidence.filter(
    (e): e is TieBreakEvidence => e.kind === 'conflict-tiebreak',
  );
  assert.equal(tieBreaks.length, 2, 'T1/T2 与 T4/T5 各产生一次决胜');

  const first = tieBreaks[0];
  assert.equal(first.winner, 'T1');
  assert.deepEqual(first.losers, ['T2']);
  assert.equal(first.rule, TIE_BREAK_RULE);
  assert.deepEqual(first.topoOrder, ['T1', 'T2'], '拓扑序按 (seq,txnId) 确定');
  // 冲突明细可审计：P0:shared 写-写 + 读-写，P1:x 写-读。
  const keys = first.conflicts.map((c) => `${c.partition}:${c.key}:${c.kind}`).sort();
  assert.deepEqual(keys, [
    '0:shared:read-write',
    '0:shared:write-write',
    '1:x:write-read',
  ]);

  const second = tieBreaks[1];
  assert.equal(second.winner, 'T5');
  assert.deepEqual(second.losers, ['T4']);
  assert.deepEqual(second.topoOrder, ['T4', 'T5']);
});

test('快照切片点重放：快照 + 后续日志 == 全量回放', () => {
  const log = buildLog();
  const full = replay(log);

  // 在线实例：回放到切片点后生成快照，再继续回放剩余日志。
  const live = new StateMachine(OPTS);
  for (const e of log.slice(0, SNAPSHOT_AT)) live.ingest(e);
  const snapshot = live.createSnapshot();
  const committedBeforeSnapshot = live.committed.length;
  const evidenceBeforeSnapshot = live.evidence.length;
  assert.equal(snapshot.nextSeq, SNAPSHOT_AT);
  assert.equal(live.retainedLog.length, 0, '快照后已回放日志被截断（内存有界）');
  for (const e of log.slice(SNAPSHOT_AT)) live.ingest(e);

  // 崩溃恢复：新实例仅从快照 + 切片点之后的乱序日志恢复。
  const recovered = StateMachine.recover(
    snapshot,
    shuffledDelivery(log.slice(SNAPSHOT_AT), 31337),
    OPTS,
  );

  assert.equal(recovered.canonicalState(), full.canonicalState());
  assert.equal(live.canonicalState(), full.canonicalState());
  assert.deepEqual(live.committed, full.committed);
  assert.deepEqual(recovered.committed, full.committed.slice(committedBeforeSnapshot));
  // 恢复实例的决胜证据与在线实例切片点之后的一致。
  assert.deepEqual(
    recovered.evidence.map((e) => e.kind),
    live.evidence.slice(evidenceBeforeSnapshot).map((e) => e.kind),
  );
});

test('崩溃恢复：空快照 + 全量乱序日志 == 顺序回放', () => {
  const log = buildLog();
  const inOrder = replay(log);
  const recovered = StateMachine.recover(null, shuffledDelivery(log, 777), OPTS);
  assert.equal(recovered.canonicalState(), inOrder.canonicalState());
});

test('内存有界：未决事务表超限确定性驱逐并留证', () => {
  const sm = new StateMachine({ partitionCount: 1, maxPending: 2 });
  for (let i = 0; i < 5; i++) {
    sm.ingest({
      seq: i,
      txnId: `E${i}`,
      kind: 'prepare',
      partitions: [0],
      reads: {},
      writes: { 0: { [`k${i}`]: `${i}` } },
    });
  }
  assert.equal(sm.pendingCount, 2, '未决表不超上限');
  assert.equal(sm.stats.evictions, 3);
  const evicted = sm.evidence
    .filter((e) => e.kind === 'memory-bound-eviction')
    .map((e) => (e.kind === 'memory-bound-eviction' ? e.evicted : ''));
  assert.deepEqual(evicted, ['E0', 'E1', 'E2'], '按 seq 最旧者确定性驱逐');

  // 被驱逐事务的 commit 是幂等空操作，结果与乱序回放一致。
  const prepares: LogEvent[] = [0, 1, 2, 3, 4].map((i) => ({
    seq: i,
    txnId: `E${i}`,
    kind: 'prepare',
    partitions: [0],
    reads: {},
    writes: { 0: { [`k${i}`]: `${i}` } },
  }));
  const tail: LogEvent[] = [3, 4, 0, 1, 2].map((i) => ({
    seq: 5 + i,
    txnId: `E${i}`,
    kind: 'commit',
    partitions: [0],
    reads: {},
    writes: {},
  }));
  const a = new StateMachine({ partitionCount: 1, maxPending: 2 });
  const b = new StateMachine({ partitionCount: 1, maxPending: 2 });
  for (const e of [...prepares, ...tail]) a.ingest(e);
  for (const e of [...prepares, ...[...tail].reverse()]) b.ingest(e);
  assert.equal(a.canonicalState(), b.canonicalState());
  assert.equal(a.get(0, 'k3'), '3');
  assert.equal(a.get(0, 'k0'), undefined, '被驱逐事务的写不得生效');
});

test('重复与过期事件幂等', () => {
  const log = buildLog();
  const sm = replay(log);
  const before = sm.canonicalState();
  for (const e of log) sm.ingest(e); // 全部重投一遍
  assert.equal(sm.canonicalState(), before);
  assert.ok(sm.stats.duplicates >= log.length);
});

test('显式 abort 不产生写', () => {
  const sm = replay(buildLog());
  assert.equal(sm.get(1, 'ghost'), undefined);
});
