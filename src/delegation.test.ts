import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OwnershipEngine } from './engine.ts';
import { EventFactory, resetEventSequence } from './test-harness.ts';
import { replayPermutations, assertConvergent } from './replay.ts';
import type { DomainEvent } from './types.ts';

const OWNER = 'acct:owner@origin-a.example';
const EDITOR = 'acct:editor@origin-b.example';

function setupDelegatedObject(windowEnd: number) {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:delegation-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));
  const built = factory.grant(objectId, OWNER, EDITOR, ['summary', 'content'], 100, windowEnd, 10);
  engine.ingest(built.onboarding);
  engine.ingest(built.grant);
  return { engine, factory, objectId, grantId: built.grant.payload.grantId };
}

test('子授权时间窗：生效前挂起，窗口内允许，到期边界（排他）拒绝', () => {
  const { engine, factory, objectId, grantId } = setupDelegatedObject(200);

  const beforeWindow = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'summary', 'early', 99, { grantId });
  const beforeResult = engine.ingest(beforeWindow);
  assert.equal(beforeResult.status, 'pending');
  assert.ok(beforeResult.evidence.some((entry) => entry.code === 'GRANT_NOT_BEFORE'));

  const inside = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'summary', 'ok', 150, { grantId });
  assert.equal(engine.ingest(inside).verdict, 'allow');

  const boundary = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'summary', 'edge', 200, { grantId });
  const boundaryResult = engine.ingest(boundary);
  assert.equal(boundaryResult.status, 'rejected');
  assert.equal(boundaryResult.reason, 'GRANT_EXPIRED_DENY');

  const after = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'content', 'late', 201, { grantId });
  assert.equal(engine.ingest(after).reason, 'GRANT_EXPIRED_DENY');

  // 生效前挂起的事件在时间窗不会“自动变合法”：其 atTime 固定为 99，重排空仍拒绝。
  assert.equal(engine.state.pending.length, 1);
});

test('字段作用域：越权字段拒绝，不影响同对象其他字段', () => {
  const { engine, factory, objectId, grantId } = setupDelegatedObject(500);
  const outOfScope = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'acl', 'elevated', 150, { grantId });
  const result = engine.ingest(outOfScope);
  assert.equal(result.status, 'rejected');
  assert.equal(result.reason, 'GRANT_SCOPE_DENY');

  const inScope = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'content', 'still fine', 151, { grantId });
  assert.equal(engine.ingest(inScope).verdict, 'allow');
});

test('并发撤销：分叉两支上的撤销在恢复后均不可逆，授权不得复活', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:revoke-1';
  const engine = new OwnershipEngine();
  const root = factory.registration(objectId, 'origin-a.example', OWNER, 0);
  engine.ingest(root);
  const rootId = `root:${objectId}`;
  engine.ingestAll([
    factory.onboardActor(objectId, 'acct:hold-a@d1.example', 2, OWNER),
    factory.onboardActor(objectId, 'acct:hold-b@d2.example', 3, OWNER),
  ]);

  // 分叉前签发的委托（锚定根节点），随后分叉为两支。
  const shared = factory.grant(objectId, OWNER, EDITOR, ['summary'], 0, 1000, 5, 'grant:shared');
  engine.ingest(shared.onboarding);
  engine.ingest(shared.grant);
  const grant = shared.grant;
  const branchA = factory.transfer(objectId, rootId, 0, OWNER, 'acct:hold-a@d1.example', 10, 'tx:branch-a');
  const branchB = factory.transfer(objectId, rootId, 0, OWNER, 'acct:hold-b@d2.example', 11, 'tx:branch-b');
  engine.ingestAll([branchA, branchB]);
  assert.equal(engine.isForked(objectId), true);

  // 分叉期间撤销尝试：分叉未决无唯一持有人 -> 拒绝（管理操作不挂起授权语义）。
  const revokeDuringFork = factory.revoke(objectId, grant.payload.grantId, 'acct:hold-a@d1.example', 12);
  const forkRevokeResult = engine.ingest(revokeDuringFork);
  assert.ok(
    forkRevokeResult.reason === 'FORK_UNRESOLVED' || forkRevokeResult.reason === 'REVOKE_NOT_OWNER',
  );

  // 恢复合并。
  const recovery = factory.recovery(
    objectId,
    [branchA.payload.transferId, branchB.payload.transferId],
    ['acct:hold-a@d1.example', 'acct:hold-b@d2.example'],
    OWNER,
    20,
  );
  engine.ingest(recovery);
  assert.equal(engine.isForked(objectId), false);

  // 合并后新持有人撤销，永久生效。
  const revoke = factory.revoke(objectId, grant.payload.grantId, OWNER, 25);
  assert.equal(engine.ingest(revoke).status, 'applied');

  const edit = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'summary', 'post-revoke', 30, {
    grantId: grant.payload.grantId,
  });
  const editResult = engine.ingest(edit);
  assert.equal(editResult.reason, 'GRANT_REVOKED_DENY');

  // 重复撤销被拒（墓碑语义）。
  const again = factory.revoke(objectId, grant.payload.grantId, OWNER, 26);
  assert.equal(engine.ingest(again).reason, 'GRANT_ALREADY_REVOKED');
});

test('恢复合并清除未被合并谱系锚定的授权，且乱序重放结论一致', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:grant-purge-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));
  const rootId = `root:${objectId}`;
  engine.ingestAll([
    factory.onboardActor(objectId, 'acct:hold-a@d1.example', 2, OWNER),
    factory.onboardActor(objectId, 'acct:hold-b@d2.example', 3, OWNER),
  ]);

  const branchA = factory.transfer(objectId, rootId, 0, OWNER, 'acct:hold-a@d1.example', 10, 'tx:pa');
  const branchB = factory.transfer(objectId, rootId, 0, OWNER, 'acct:hold-b@d2.example', 11, 'tx:pb');
  engine.ingestAll([branchA, branchB]);

  // 分叉未决不能签发委托。
  const invalidBuilt = factory.grant(objectId, 'acct:hold-a@d1.example', EDITOR, ['summary'], 0, 1000, 12, 'grant:forked');
  assert.equal(engine.ingest(invalidBuilt.onboarding).status, 'applied');
  const invalidGrantResult = engine.ingest(invalidBuilt.grant);
  assert.equal(invalidGrantResult.status, 'pending');
  assert.equal(invalidGrantResult.reason, 'GRANT_NOT_OWNER');
  const invalidSettle = engine.settle();
  assert.ok(invalidSettle.some((entry) => entry.reason === 'GRANT_NOT_OWNER'));

  const recovery = factory.recovery(
    objectId,
    [branchA.payload.transferId, branchB.payload.transferId],
    ['acct:hold-a@d1.example', 'acct:hold-b@d2.example'],
    OWNER,
    20,
  );
  engine.ingest(recovery);

  const survivingBuilt = factory.grant(objectId, OWNER, EDITOR, ['summary'], 21, 900, 22, 'grant:survives');
  engine.ingest(survivingBuilt.onboarding);
  engine.ingest(survivingBuilt.grant);
  const surviving = survivingBuilt.grant;
  const edit = factory.edit(objectId, EDITOR, `${EDITOR}#k1`, 'summary', 'merged', 30, {
    grantId: surviving.payload.grantId,
  });
  assert.equal(engine.ingest(edit).verdict, 'allow');

  const allEvents: DomainEvent[] = [];
  for (const result of engine.state.appliedLog) {
    allEvents.push(result);
  }
  allEvents.push(edit);
  const reports = replayPermutations(allEvents);
  assertConvergent(reports);
});

test('双签名转让：代次或父节点不匹配、单侧伪造签名均拒绝', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:transfer-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));
  const rootId = `root:${objectId}`;
  engine.ingestAll([
    factory.onboardActor(objectId, 'acct:next@d2.example', 2, OWNER),
    factory.onboardActor(objectId, 'acct:third@d3.example', 3, OWNER),
  ]);

  const wrongGeneration = factory.transfer(objectId, rootId, 9, OWNER, 'acct:next@d2.example', 10, 'tx:gen');
  assert.equal(engine.ingest(wrongGeneration).reason, 'GENERATION_MISMATCH');

  const ok = factory.transfer(objectId, rootId, 0, OWNER, 'acct:next@d2.example', 11, 'tx:ok');
  assert.equal(engine.ingest(ok).status, 'applied');

  // 篡改新接收者签名 -> 旧持有人签名仍有效但双签核验失败。
  const tampered = factory.transfer(objectId, ok.payload.transferId, 1, 'acct:next@d2.example', 'acct:third@d3.example', 12, 'tx:tamper');
  tampered.payload.newHolderSignature = 'AAAA';
  assert.equal(engine.ingest(tampered).reason, 'SIGNATURE_INVALID');
});
