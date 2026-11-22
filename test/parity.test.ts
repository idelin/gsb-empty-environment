import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AuthOutcome, DenyScope, InternalReason } from '../src/index.ts';
import { buildTestGuard, FIXED_DURATION, settle } from './helpers.ts';

test('不存在账号与错误密码返回同构结果', async () => {
  const { guard, clock } = await buildTestGuard();

  const unknown = await guard.authenticate({
    account: 'ghost@example.com',
    password: 'whatever',
    source: '10.0.0.1',
  });
  await settle(clock);
  const wrongPassword = await guard.authenticate({
    account: 'alice',
    password: 'wrong-password',
    source: '10.0.0.2',
  });
  await settle(clock);

  assert.deepEqual(unknown.result, wrongPassword.result);
  assert.equal(unknown.result.outcome, AuthOutcome.Rejected);
  assert.equal(unknown.result.code, 'invalid_credentials');

  // 内部判定仍可区分真实原因，供审计使用。
  assert.equal(unknown.decision.reason, InternalReason.NoSuchAccount);
  assert.equal(wrongPassword.decision.reason, InternalReason.BadCredentials);
});

test('成功、错误密码、不存在账号三类路径耗时完全一致', async () => {
  const { guard, clock } = await buildTestGuard();

  const cases: Array<[string, string, string]> = [
    ['alice', 'correct-horse', '10.0.1.1'],
    ['alice', 'wrong', '10.0.1.2'],
    ['nobody', 'wrong', '10.0.1.3'],
  ];

  for (const [account, password, source] of cases) {
    const startedAt = clock.now();
    const response = await guard.authenticate({ account, password, source });
    await settle(clock);
    assert.equal(
      clock.now() - startedAt,
      FIXED_DURATION,
      `路径 ${account}/${password} 未保持固定耗时`,
    );
    assert.ok(response.result);
  }
});

test('账号归一化后大小写 / 空白等价', async () => {
  const { guard, clock } = await buildTestGuard();
  const response = await guard.authenticate({
    account: '  ALICE  ',
    password: 'correct-horse',
    source: '10.0.2.1',
  });
  await settle(clock);
  assert.equal(response.result.code, 'ok');
  assert.equal(response.decision.reason, InternalReason.Authenticated);
});

test('不存在账号不会创建账号级锁定状态', async () => {
  const { guard, clock } = await buildTestGuard({
    accountMaxAttempts: 200,
    sourceMaxAttempts: 200,
    accountLockoutThreshold: 2,
  });

  for (let i = 0; i < 5; i += 1) {
    const response = await guard.authenticate({
      account: 'missing-user',
      password: 'x',
      source: `10.1.0.${i}`,
    });
    await settle(clock);
    assert.equal(response.result.code, 'invalid_credentials');
  }

  const accountLocks = guard
    .activeLocks()
    .filter((lock) => lock.scope === DenyScope.Account);
  assert.equal(accountLocks.length, 0);
});
