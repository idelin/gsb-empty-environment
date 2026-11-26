import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OwnershipEngine } from './engine.ts';
import { EventFactory, resetEventSequence } from './test-harness.ts';
import { replayAll, replayPermutations, assertConvergent } from './replay.ts';

const OWNER = 'acct:owner@origin-a.example';

test('历史公钥版本：轮换后旧版本仍可核验历史签名，撤销后旧签名权利不复活', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:key-history-1';
  const engine = new OwnershipEngine();
  engine.ingest(factory.registration(objectId, 'origin-a.example', OWNER, 0));

  // 用 k1 完成一次历史编辑。
  const historical = factory.edit(objectId, OWNER, `${OWNER}#k1`, 'summary', 'signed with k1', 10);
  assert.equal(engine.ingest(historical).verdict, 'allow');

  // 轮换到 k2，再撤销 k1；密钥历史全部保留。
  engine.ingest(factory.keyPublish(objectId, OWNER, `${OWNER}#k2`, 20));
  engine.ingest(factory.keyRevoke(objectId, OWNER, `${OWNER}#k1`, 25));

  const state = engine.getObject(objectId)!;
  assert.ok(state.actors[OWNER].keys[`${OWNER}#k1`].revokedAt !== undefined);
  assert.ok(state.actors[OWNER].keys[`${OWNER}#k2`].revokedAt === undefined);

  // 撤销后再用 k1 发编辑 -> 拒绝；k2 正常工作。
  const staleKey = factory.edit(objectId, OWNER, `${OWNER}#k1`, 'summary', 'revoked key', 30);
  assert.equal(engine.ingest(staleKey).reason, 'KEY_REVOKED_DENY');
  const freshKey = factory.edit(objectId, OWNER, `${OWNER}#k2`, 'summary', 'rotated key', 31);
  assert.equal(engine.ingest(freshKey).verdict, 'allow');

  // 重复撤销不可逆。
  assert.equal(engine.ingest(factory.keyRevoke(objectId, OWNER, `${OWNER}#k1`, 32)).reason, 'KEY_ALREADY_REVOKED');
});

test('乱序到达：编辑先于登记/授权事件时挂起，依赖满足后确定性排空', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const objectId = 'obj:out-of-order-1';
  const editor = 'acct:editor@origin-b.example';

  const registration = factory.registration(objectId, 'origin-a.example', OWNER, 0);
  const built = factory.grant(objectId, OWNER, editor, ['summary'], 0, 500, 5, 'grant:ooo');
  const grant = built.grant;
  const edit = factory.edit(objectId, editor, `${editor}#k1`, 'summary', 'arrives first', 10, {
    grantId: grant.payload.grantId,
  });

  // 先投递编辑与授权，最后才登记：前两者进入挂起缓冲，登记后全部收敛。
  const engine = new OwnershipEngine();
  const first = engine.ingest(edit);
  assert.equal(first.status, 'pending');
  const secondOnboarding = engine.ingest(built.onboarding);
  assert.equal(secondOnboarding.status, 'pending');
  const second = engine.ingest(grant);
  assert.equal(second.status, 'pending');
  assert.equal(engine.state.pending.length, 3);

  const third = engine.ingest(registration);
  assert.equal(third.status, 'applied');
  assert.equal(engine.state.pending.length, 0);
  assert.ok(engine.getObject(objectId)!.appliedEdits.some((entry) => entry.editId === edit.payload.editId));

  // 全量重放与乱序重放收敛一致。
  const ordered = [registration, grant, edit];
  const reports = replayPermutations(ordered);
  assertConvergent(reports);
  const direct = replayAll(ordered);
  assert.equal(direct.stateHash, reports[0].stateHash);
});

test('nonce 重放：同对象复用 nonce 拒绝，不同对象互不干扰', () => {
  resetEventSequence();
  const factory = new EventFactory();
  const engine = new OwnershipEngine();
  const objectA = 'obj:nonce-a';
  const objectB = 'obj:nonce-b';
  engine.ingest(factory.registration(objectA, 'origin-a.example', OWNER, 0));
  engine.ingest(factory.registration(objectB, 'origin-b.example', OWNER, 1));

  const first = factory.edit(objectA, OWNER, `${OWNER}#k1`, 'summary', 'a', 10);
  assert.equal(engine.ingest(first).verdict, 'allow');

  const replay = factory.edit(objectA, OWNER, `${OWNER}#k1`, 'summary', 'a replay', 11);
  replay.payload.nonce = first.payload.nonce;
  replay.payload.signature = factory.wallet.sign(
    OWNER,
    `${OWNER}#k1`,
    'field.edit',
    { ...replay.payload, signature: undefined },
  );
  assert.equal(engine.ingest(replay).reason, 'NONCE_REPLAY');

  // 另一对象使用相同 nonce 字符串仍允许（隔离记账）。
  const other = factory.edit(objectB, OWNER, `${OWNER}#k1`, 'summary', 'b', 12);
  other.payload.nonce = first.payload.nonce;
  other.payload.signature = factory.wallet.sign(
    OWNER,
    `${OWNER}#k1`,
    'field.edit',
    { ...other.payload, signature: undefined },
  );
  assert.equal(engine.ingest(other).verdict, 'allow');
});
