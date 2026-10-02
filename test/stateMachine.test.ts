import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DeterministicLog } from '../src/log.ts';
import { StateMachine, conflictKeys } from '../src/stateMachine.ts';
import type { PrepareEvent, TxnEvent } from '../src/types.ts';

/** mulberry32：确定性伪随机数，用于可复现的乱序注入。 */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function shuffle<T>(items: T[], rand: () => number): T[] {
  const arr = [...items];
  for (let i = arr.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [arr[i], arr[j]] = [arr[j]!, arr[i]!];
  }
  return arr;
}

function prepare(
  txnId: string,
  lamport: number,
  reads: string[],
  writes: Record<string, string>,
): PrepareEvent {
  return { kind: 'prepare', txnId, lamport, reads, writes };
}

/** 选取落在指定分区上的键（哈希确定，因此测试可复现）。 */
function keyOnPartition(sm: StateMachine, partition: number, prefix: string): string {
  for (let i = 0; ; i++) {
    const key = `${prefix}${i}`;
    if (sm.partitionOf(key) === partition) return key;
  }
}

/** 构造跨 2 个分区的并发事务 T1/T2 的事件序列。 */
function buildInterleavedEvents(sm: StateMachine): {
  events: TxnEvent[];
  keyA: string;
  keyB: string;
  shared: string;
} {
  const keyA = keyOnPartition(sm, 0, 'a'); // 分区 0
  const keyB = keyOnPartition(sm, 1, 'b'); // 分区 1
  const shared = keyOnPartition(sm, 0, 's'); // 双方竞争，分区 0
  const events: TxnEvent[] = [
    prepare('T1', 1, [keyB], { [keyA]: 'T1-a', [shared]: 'T1-s' }),
    { kind: 'commit', txnId: 'T1' },
    prepare('T2', 2, [keyA], { [keyB]: 'T2-b', [shared]: 'T2-s' }),
    { kind: 'commit', txnId: 'T2' },
  ];
  return { events, keyA, keyB, shared };
}

function runAll(events: TxnEvent[], options?: object): StateMachine {
  const sm = new StateMachine({ numPartitions: 2, ...options });
  const log = new DeterministicLog();
  for (const e of events) sm.applyEntry(log.append(e));
  sm.seal(); // 批次边界：完整视图下确定性定局
  return sm;
}

test('跨 2 分区并发事务：所有合法交错产生相同最终状态', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const { events, keyA, keyB, shared } = buildInterleavedEvents(probe);

  // 枚举 T1/T2 事件的全部合法交错（每个事务 prepare 先于 commit，共 6 种）。
  const interleavings: TxnEvent[][] = [];
  const t1 = events.slice(0, 2);
  const t2 = events.slice(2, 4);
  for (let mask = 0; mask < 4; mask++) {
    // 占位，下面用组合方式生成
  }
  const positions = [
    [0, 1], [0, 2], [0, 3], [1, 2], [1, 3], [2, 3],
  ];
  for (const [i, j] of positions) {
    const seq: TxnEvent[] = new Array(4);
    seq[i!] = t1[0]!;
    seq[j!] = t1[1]!;
    const rest = [0, 1, 2, 3].filter((p) => p !== i && p !== j);
    seq[rest[0]!] = t2[0]!;
    seq[rest[1]!] = t2[1]!;
    interleavings.push(seq);
  }
  assert.equal(interleavings.length, 6);

  const expected = runAll(interleavings[0]!).state();
  assert.deepEqual(expected, {
    [keyA]: 'T1-a',
    [keyB]: 'T2-b',
    [shared]: 'T2-s', // T2 排序键更大，后应用，覆盖 shared
  });
  for (const seq of interleavings) {
    assert.deepEqual(runAll(seq).state(), expected);
  }
});

test('乱序网络包注入：100 个随机种子的投递顺序产生相同最终状态与应用顺序', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const { events } = buildInterleavedEvents(probe);
  // 额外加入跨分区事务 T3，与 T1/T2 均冲突。
  const k0 = keyOnPartition(probe, 0, 'c');
  const k1 = keyOnPartition(probe, 1, 'd');
  const extra: TxnEvent[] = [
    prepare('T3', 3, [k0], { [k1]: 'T3-d' }),
    { kind: 'commit', txnId: 'T3' },
    prepare('T4', 0, [], { [k0]: 'T4-c', [k1]: 'T4-d1' }), // lamport 最小
    { kind: 'commit', txnId: 'T4' },
  ];
  const all = [...events, ...extra];

  const canonical = runAll(all);
  const expectedState = canonical.state();
  const expectedOrder = canonical.getAppliedOrder();

  for (let seed = 1; seed <= 100; seed++) {
    const shuffled = shuffle(all, rng(seed));
    const sm = runAll(shuffled);
    assert.deepEqual(sm.state(), expectedState, `seed=${seed} 状态不一致`);
    assert.deepEqual(
      sm.getAppliedOrder(),
      expectedOrder,
      `seed=${seed} 应用顺序不一致`,
    );
  }
  assert.deepEqual(expectedOrder, ['T4', 'T1', 'T2', 'T3']);
});

test('未决冲突输出可审计决胜证据，且证据内容与到达顺序无关', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const { events } = buildInterleavedEvents(probe);
  const [p1, c1, p2, c2] = events as [PrepareEvent, TxnEvent, PrepareEvent, TxnEvent];

  const forward = runAll([p1, p2, c1, c2]);
  const backward = runAll([p2, p1, c2, c1]);

  const evForward = forward.audit.ofType('tie-break');
  const evBackward = backward.audit.ofType('tie-break');
  assert.equal(evForward.length, 1);
  assert.deepEqual(evBackward, evForward);

  const ev = evForward[0]!;
  assert.equal(ev.txnA, 'T1');
  assert.equal(ev.txnB, 'T2');
  assert.equal(ev.winner, 'T1'); // (1,'T1') < (2,'T2')，T1 先应用
  assert.deepEqual(ev.orderKeyA, [1, 'T1']);
  assert.deepEqual(ev.orderKeyB, [2, 'T2']);
  assert.ok(ev.conflictKeys.length > 0);
  assert.match(ev.rule, /lamport/);
  // 冲突键确实来自读写集合交集。
  assert.deepEqual(ev.conflictKeys, conflictKeys(p1, p2));
});

test('冲突事务的拓扑排序：被阻塞事务在前驱落定后才应用', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const k = keyOnPartition(probe, 0, 'x');
  // T2 的 commit 先到，但与未决的 T1 冲突 => 必须等待 T1。
  const sm = runAll([
    prepare('T2', 2, [], { [k]: 'T2' }),
    { kind: 'commit', txnId: 'T2' },
    prepare('T1', 1, [], { [k]: 'T1' }),
    { kind: 'commit', txnId: 'T1' },
  ]);
  assert.deepEqual(sm.getAppliedOrder(), ['T1', 'T2']);
  assert.deepEqual(sm.state(), { [k]: 'T2' });
});

test('abort 不写入，且解除对冲突事务的阻塞', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const k = keyOnPartition(probe, 0, 'x');
  const sm = runAll([
    prepare('T1', 1, [], { [k]: 'T1' }),
    prepare('T2', 2, [], { [k]: 'T2' }),
    { kind: 'abort', txnId: 'T1' },
    { kind: 'commit', txnId: 'T2' },
  ]);
  assert.deepEqual(sm.state(), { [k]: 'T2' });
  assert.deepEqual(sm.getAppliedOrder(), ['T1', 'T2']);
  assert.equal(sm.audit.ofType('abort-applied').length, 1);
  assert.equal(sm.audit.ofType('apply').length, 1);
});

test('commit 先于 prepare 到达（乱序）也能正确落定', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const k = keyOnPartition(probe, 0, 'x');
  const sm = runAll([
    { kind: 'commit', txnId: 'T1' },
    prepare('T1', 1, [], { [k]: 'v1' }),
  ]);
  assert.deepEqual(sm.state(), { [k]: 'v1' });
  assert.deepEqual(sm.getAppliedOrder(), ['T1']);
});

test('快照切片点回放：快照后重放后续日志，状态完全一致', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const { events } = buildInterleavedEvents(probe);
  const k0 = keyOnPartition(probe, 0, 'c');
  const k1 = keyOnPartition(probe, 1, 'd');
  const all: TxnEvent[] = [
    ...events,
    prepare('T3', 3, [k0], { [k1]: 'T3-d' }),
    prepare('T4', 4, [], { [k0]: 'T4-c' }),
    { kind: 'commit', txnId: 'T3' },
    { kind: 'abort', txnId: 'T4' },
    prepare('T5', 5, [], { [k0]: 'T5-c', [k1]: 'T5-d' }),
    { kind: 'commit', txnId: 'T5' },
  ];
  // 注入乱序后再写入日志。
  const shuffled = shuffle(all, rng(42));

  // 完整回放作为基准。
  const fullLog = new DeterministicLog();
  const full = new StateMachine({ numPartitions: 2 });
  for (const e of shuffled) full.applyEntry(fullLog.append(e));
  full.seal();

  // 在多个切片点生成快照，模拟崩溃恢复：快照 + 重放剩余日志。
  for (const cut of [0, 1, 3, 5, 7, shuffled.length]) {
    const log = new DeterministicLog();
    const before = new StateMachine({ numPartitions: 2 });
    for (const e of shuffled.slice(0, cut)) before.applyEntry(log.append(e));

    const snapshotJson = JSON.stringify(before.snapshot()); // 模拟持久化
    const restored = StateMachine.restore(JSON.parse(snapshotJson));
    assert.equal(restored.consumedSeq, log.slicePoint());

    for (const entry of log.replayFrom(restored.consumedSeq)) {
      restored.applyEntry(entry); // 切片点之间无剩余日志
    }
    for (const e of shuffled.slice(cut)) restored.applyEntry(log.append(e));
    restored.seal();

    assert.deepEqual(restored.state(), full.state(), `cut=${cut} 状态不一致`);
    assert.deepEqual(
      restored.getAppliedOrder(),
      full.getAppliedOrder(),
      `cut=${cut} 应用顺序不一致`,
    );
  }
});

test('内存有界：未决表满时拒绝 prepare 并输出证据；已决缓存 FIFO 逐出', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const k = keyOnPartition(probe, 0, 'x');

  const sm = new StateMachine({ numPartitions: 2, maxPending: 2 });
  sm.applyEvent(prepare('T1', 1, [], { [k]: '1' }));
  sm.applyEvent(prepare('T2', 2, [], { [k]: '2' }));
  sm.applyEvent(prepare('T3', 3, [], { [k]: '3' })); // 被拒绝
  assert.equal(sm.pendingCount(), 2);
  const rejects = sm.audit.ofType('reject-prepare');
  assert.equal(rejects.length, 1);
  assert.equal(rejects[0]!.txnId, 'T3');
  assert.equal(rejects[0]!.reason, 'max-pending-exceeded');

  const sm2 = new StateMachine({ numPartitions: 2, resolvedCacheSize: 2 });
  for (let i = 0; i < 5; i++) {
    sm2.applyEvent(prepare(`T${i}`, i, [], { [`k${i}`]: 'v' }));
    sm2.applyEvent({ kind: 'commit', txnId: `T${i}` });
  }
  sm2.seal();
  assert.ok(sm2.resolvedCount() <= 2);
  assert.equal(sm2.pendingCount(), 0);
  assert.deepEqual(sm2.state(), {
    k0: 'v', k1: 'v', k2: 'v', k3: 'v', k4: 'v',
  });
});

test('重复事件被去重并记录审计', () => {
  const probe = new StateMachine({ numPartitions: 2 });
  const k = keyOnPartition(probe, 0, 'x');
  const p = prepare('T1', 1, [], { [k]: 'v' });
  const sm = runAll([p, p, { kind: 'commit', txnId: 'T1' }, { kind: 'commit', txnId: 'T1' }]);
  assert.deepEqual(sm.state(), { [k]: 'v' });
  assert.deepEqual(sm.getAppliedOrder(), ['T1']);
  assert.ok(sm.audit.ofType('duplicate-event').length >= 2);
});
