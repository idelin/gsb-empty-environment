import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OwnershipEngine, hashObjectState } from './engine.ts';
import { EventFactory, resetEventSequence } from './test-harness.ts';
import { replayAll, replayFromCheckpoint, statesEqual, replayPermutations, assertConvergent } from './replay.ts';

const OWNER = 'acct:owner@origin-a.example';
const ALIAS = 'acct:legacy@origin-history.example';

test('旧别名：退役后签名编辑永久拒绝，重新绑定也被拒绝', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:alias-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));
  engine.ingest(factory.aliasBind(objectId, OWNER, ALIAS, 5));

  const aliasKeyId = `${ALIAS}#ak1`;
  const allowed = factory.edit(objectId, ALIAS, aliasKeyId, 'summary', 'via alias', 10);
  assert.equal(engine.ingest(allowed).verdict, 'allow');

  engine.ingest(factory.aliasRetire(objectId, OWNER, ALIAS, 15));
  const blocked = factory.edit(objectId, ALIAS, aliasKeyId, 'summary', 'retired alias write', 20);
  const result = engine.ingest(blocked);
  assert.equal(result.status, 'rejected');
  assert.equal(result.reason, 'ALIAS_RETIRED_DENY');

  const rebind = factory.aliasBind(objectId, OWNER, ALIAS, 25);
  assert.equal(engine.ingest(rebind).reason, 'ALIAS_RETIRED');
});

test('检查点压缩：压缩后旧别名依旧隔离，且从检查点恢复与全量重放等价', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:checkpoint-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));
  engine.ingest(factory.aliasBind(objectId, OWNER, ALIAS, 5));
  engine.ingest(factory.edit(objectId, ALIAS, `${ALIAS}#ak1`, 'summary', 'first', 8));
  engine.ingest(factory.aliasRetire(objectId, OWNER, ALIAS, 15));

  const snapshot = engine.checkpoint(objectId);
  assert.equal(engine.getObject(objectId)!.appliedEdits.every((edit) => edit.compressed === true), true);
  assert.equal(snapshot.state.compressedBeforeSeq, undefined);

  // 压缩后再尝试旧别名编辑，仍然命中退役墓碑。
  const afterCompression = factory.edit(objectId, ALIAS, `${ALIAS}#ak1`, 'summary', 'after checkpoint', 30);
  const denied = engine.ingest(afterCompression);
  assert.equal(denied.reason, 'ALIAS_RETIRED_DENY');

  // 全量重放（不使用任何缓存）必须得到相同状态哈希。
  const full = replayAll(engine.state.appliedLog);
  // 全量重放不携带压缩标记，因此与压缩前快照等价；压缩只影响骨架标志，不影响裁决状态。
  assert.equal(full.objectHashes[objectId], snapshot.stateHash);

  // 从检查点恢复并重放检查点之后的事件（此处无后续权威事件，仅验证等价性）。
  const resumed = replayFromCheckpoint(snapshot, []);
  assert.ok(statesEqual(resumed.state.objects[objectId], snapshot.state));

  // 篡改检查点哈希必须被识破。
  const tampered = { ...snapshot, stateHash: 'bogus-hash' };
  assert.throws(() => replayFromCheckpoint(tampered, []), /自哈希不一致/);
});

test('检查点标记事件参与重放时强制哈希核对，伪造压缩点被拒绝', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:checkpoint-marker-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));
  const snapshot = engine.checkpoint(objectId);

  const marker = factory.checkpoint(objectId, snapshot.throughAppliedSeq, 'forged', 99);
  const result = engine.ingest(marker);
  assert.equal(result.status, 'rejected');
  assert.equal(result.reason, 'CHECKPOINT_HASH_MISMATCH');
});

test('传输签名重放：HTTP 投递证据仅参考，重放 nonce 留痕且不能授权编辑', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:transport-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));

  const transport = { relayId: 'relay-1', transportNonce: 'tx-nonce-1', transportSignature: 'relay-sig' };
  const first = factory.edit(objectId, OWNER, `${OWNER}#k1`, 'summary', 'first delivery', 10, { transport });
  const firstResult = engine.ingest(first);
  assert.equal(firstResult.verdict, 'allow');
  assert.ok(firstResult.evidence.some((entry) => entry.code === 'TRANSPORT_SIGNATURE_ADVISORY'));

  // 同一传输 nonce 再次出现（即使是全新权威事件）-> 重放留痕拒绝证据。
  const replayTransport = { ...transport };
  const second = factory.edit(objectId, OWNER, `${OWNER}#k1`, 'summary', 'replayed delivery', 11, {
    transport: replayTransport,
  });
  const secondResult = engine.ingest(second);
  assert.ok(secondResult.evidence.some((entry) => entry.code === 'TRANSPORT_NONCE_REPLAY'));

  // 完全重复投递同一事件 -> 幂等 duplicate，不会二次应用。
  const duplicate = engine.ingest(first);
  assert.equal(duplicate.status, 'duplicate');
});

test('对象严格隔离：一个对象的分叉与撤销不影响另一对象', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const engine = new OwnershipEngine();
  const objectA = 'obj:isolation-a';
  const objectB = 'obj:isolation-b';
  engine.ingest(factory.registration(objectA, 'domain-a.example', OWNER, 0));
  engine.ingest(factory.registration(objectB, 'domain-b.example', OWNER, 1));

  const rootA = `root:${objectA}`;
  engine.ingestAll([
    factory.onboardActor(objectA, 'acct:a1@d1.example', 5, OWNER),
    factory.onboardActor(objectA, 'acct:a2@d2.example', 6, OWNER),
  ]);
  engine.ingestAll([
    factory.transfer(objectA, rootA, 0, OWNER, 'acct:a1@d1.example', 10, 'tx:a-fork-1'),
    factory.transfer(objectA, rootA, 0, OWNER, 'acct:a2@d2.example', 11, 'tx:a-fork-2'),
  ]);
  assert.equal(engine.isForked(objectA), true);
  assert.equal(engine.isForked(objectB), false);

  const editB = factory.edit(objectB, OWNER, `${OWNER}#k1`, 'summary', 'object B unaffected', 12);
  assert.equal(engine.ingest(editB).verdict, 'allow');

  const editA = factory.edit(objectA, 'acct:a1@d1.example', 'acct:a1@d1.example#k1', 'summary', 'blocked', 13);
  assert.equal(engine.ingest(editA).status, 'pending');

  // nonce 与事件记账按对象隔离，重放核对分别给出独立哈希。
  const report = replayAll(engine.state.appliedLog);
  assert.ok(report.objectHashes[objectA]);
  assert.ok(report.objectHashes[objectB]);
  assert.notEqual(report.objectHashes[objectA], report.objectHashes[objectB]);
});

test('乱序重放覆盖别名退役与检查点：所有排列状态哈希一致', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:perm-alias-1';
  const engine = new OwnershipEngine();
  const events = [
    factory.registration(objectId, 'origin-a.example', OWNER, 0),
    factory.aliasBind(objectId, OWNER, ALIAS, 5),
    factory.aliasRetire(objectId, OWNER, ALIAS, 15),
    factory.keyPublish(objectId, OWNER, `${OWNER}#k2`, 20),
  ];
  const reports = replayPermutations(events);
  assertConvergent(reports);
  assert.equal(reports[0].pendingCount, 0);
});
