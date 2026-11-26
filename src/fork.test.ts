import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OwnershipEngine } from './engine.ts';
import { EventFactory, resetEventSequence } from './test-harness.ts';
import { replayAll, replayPermutations, assertConvergent, builtinPermutations } from './replay.ts';
import type { DomainEvent } from './types.ts';

function buildThreeWayFork(): { events: DomainEvent[]; factory: EventFactory; ids: Record<string, string> } {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:note-1';
  const events: DomainEvent[] = [];

  events.push(factory.registration(objectId, 'origin-a.example', 'acct:root@origin-a.example', 10));
  const rootId = `root:${objectId}`;

  for (const holder of [
    'acct:aaa@d1.example',
    'acct:mmm@d2.example',
    'acct:zzz@d3.example',
    'acct:a1a@d1.example',
    'acct:a1b@d1.example',
    'acct:rec@origin-a.example',
  ]) {
    events.push(factory.onboardActor(objectId, holder, 15, 'acct:root@origin-a.example'));
  }

  // 同一父节点上的三个子转让构成三路分叉（不依赖任何域名顺序）。
  const t1 = factory.transfer(objectId, rootId, 0, 'acct:root@origin-a.example', 'acct:aaa@d1.example', 20, 'tx:a1');
  const t2 = factory.transfer(objectId, rootId, 0, 'acct:root@origin-a.example', 'acct:mmm@d2.example', 21, 'tx:b1');
  const t3 = factory.transfer(objectId, rootId, 0, 'acct:root@origin-a.example', 'acct:zzz@d3.example', 22, 'tx:c1');
  events.push(t1, t2, t3);

  return {
    events,
    factory,
    ids: { objectId, rootId, t1: t1.payload.transferId, t2: t2.payload.transferId, t3: t3.payload.transferId },
  };
}

test('三路分叉：分叉未决时编辑挂起，域名排序不产生唯一持有人', () => {
  const { events, factory, ids } = buildThreeWayFork();
  const engine = new OwnershipEngine();
  engine.ingestAll(events);

  assert.equal(engine.isForked(ids.objectId), true);
  assert.equal(engine.currentOwner(ids.objectId), null);
  assert.deepEqual(engine.activeTipIds(ids.objectId), [ids.t1, ids.t2, ids.t3]);

  const edit = factory.edit(
    ids.objectId,
    'acct:aaa@d1.example',
    'acct:aaa@d1.example#k1',
    'summary',
    'forked write',
    30,
  );
  const result = engine.ingest(edit);
  assert.equal(result.status, 'pending');
  assert.equal(result.verdict, 'suspend');
  assert.ok(result.evidence.some((entry) => entry.code === 'FORK_UNRESOLVED'));
  assert.equal(engine.state.pending.length, 1);
});

test('三路分叉恢复：必须精确覆盖全部活跃末端并满足多数门槛', () => {
  const { events, factory, ids } = buildThreeWayFork();
  const engine = new OwnershipEngine();
  engine.ingestAll(events);

  // 分叉期间的编辑挂起。
  const suspendedEdit = factory.edit(
    ids.objectId,
    'acct:aaa@d1.example',
    'acct:aaa@d1.example#k1',
    'summary',
    'forked write',
    30,
  );
  assert.equal(engine.ingest(suspendedEdit).status, 'pending');

  // 只覆盖两个末端 -> 时序挂起；终结阶段在完整事件集下仍不可满足 -> 拒绝。
  const incomplete = factory.recovery(
    ids.objectId,
    [ids.t1, ids.t2],
    ['acct:aaa@d1.example', 'acct:mmm@d2.example'],
    'acct:rec@origin-a.example',
    40,
  );
  const bad = engine.ingest(incomplete);
  assert.equal(bad.status, 'pending');
  assert.equal(bad.reason, 'RECOVERY_TIP_SET_MISMATCH');

  // 末端集合正确但缺少一个末端持有人签名 -> 硬错误，立即永久拒绝。
  const twoSigners = factory.recovery(
    ids.objectId,
    [ids.t1, ids.t2, ids.t3],
    ['acct:aaa@d1.example', 'acct:mmm@d2.example'],
    'acct:rec@origin-a.example',
    41,
  );
  const quorumBad = engine.ingest(twoSigners);
  assert.equal(quorumBad.status, 'rejected');
  assert.equal(quorumBad.reason, 'RECOVERY_SIGNER_NOT_TIP_HOLDER');

  // 全末端三签名 -> 恢复成功；同时解锁挂起缓冲。
  const recovery = factory.recovery(
    ids.objectId,
    [ids.t1, ids.t2, ids.t3],
    ['acct:aaa@d1.example', 'acct:mmm@d2.example', 'acct:zzz@d3.example'],
    'acct:rec@origin-a.example',
    42,
  );
  const ok = engine.ingest(recovery);
  assert.equal(ok.status, 'applied');
  assert.equal(engine.isForked(ids.objectId), false);
  assert.equal(engine.currentOwner(ids.objectId), 'acct:rec@origin-a.example');

  // 终结挂起：错误恢复拒绝；分叉编辑因编辑者已非持有人且无委托拒绝。
  const settled = engine.settle();
  assert.ok(settled.some((entry) => entry.reason === 'RECOVERY_TIP_SET_MISMATCH'));
  assert.equal(engine.state.pending.length, 0);
  assert.ok(Object.values(engine.state.rejected).some((entry) => entry.reason === 'NO_DELEGATION'));

  // 历史末端上的延伸转让因 STALE_PARENT 被拒绝。
  const stale = factory.transfer(
    ids.objectId,
    ids.t2,
    1,
    'acct:mmm@d2.example',
    'acct:late@d9.example',
    50,
    'tx:stale',
  );
  const staleResult = engine.ingest(stale);
  assert.equal(staleResult.status, 'pending');
  const staleSettle = engine.settle();
  assert.ok(staleSettle.some((entry) => entry.reason === 'STALE_PARENT'));
});

test('嵌套分叉：分叉子分支再次分叉，恢复仍需覆盖全部活跃末端', () => {
  const { events, factory, ids } = buildThreeWayFork();
  const engine = new OwnershipEngine();
  engine.ingestAll(events);

  // 在 t1 下再开两个子分支（嵌套分叉），活跃末端变为 t1a, t1b, t2, t3。
  const t1a = factory.transfer(ids.objectId, ids.t1, 1, 'acct:aaa@d1.example', 'acct:a1a@d1.example', 30, 'tx:a1a');
  const t1b = factory.transfer(ids.objectId, ids.t1, 1, 'acct:aaa@d1.example', 'acct:a1b@d1.example', 31, 'tx:a1b');
  engine.ingestAll([t1a, t1b]);
  assert.deepEqual(engine.activeTipIds(ids.objectId), [
    t1a.payload.transferId,
    t1b.payload.transferId,
    ids.t2,
    ids.t3,
  ]);

  // 试图只恢复外层三个老末端 -> 集合不匹配。
  const staleRecovery = factory.recovery(
    ids.objectId,
    [ids.t1, ids.t2, ids.t3],
    ['acct:aaa@d1.example', 'acct:mmm@d2.example', 'acct:zzz@d3.example'],
    'acct:rec@origin-a.example',
    35,
  );
  assert.equal(engine.ingest(staleRecovery).reason, 'RECOVERY_TIP_SET_MISMATCH');

  const full = factory.recovery(
    ids.objectId,
    [t1a.payload.transferId, t1b.payload.transferId, ids.t2, ids.t3],
    ['acct:a1a@d1.example', 'acct:a1b@d1.example', 'acct:mmm@d2.example', 'acct:zzz@d3.example'],
    'acct:rec@origin-a.example',
    36,
  );
  assert.equal(engine.ingest(full).status, 'applied');
  assert.equal(engine.currentOwner(ids.objectId), 'acct:rec@origin-a.example');
});

test('乱序排列重放：三路分叉与恢复在所有排列下收敛到同一状态哈希', () => {
  const { events, factory, ids } = buildThreeWayFork();
  const recovery = factory.recovery(
    ids.objectId,
    [ids.t1, ids.t2, ids.t3],
    ['acct:aaa@d1.example', 'acct:mmm@d2.example', 'acct:zzz@d3.example'],
    'acct:rec@origin-a.example',
    42,
  );
  const all = [...events, recovery];
  const reports = replayPermutations(all, builtinPermutations(all.length));
  assertConvergent(reports);
  for (const report of reports) {
    assert.equal(report.pendingCount, 0);
    assert.equal(report.rejectedCount, 0);
  }

  // 再与直接全量重放核对。
  const direct = replayAll(all);
  assert.equal(direct.stateHash, reports[0].stateHash);
});
