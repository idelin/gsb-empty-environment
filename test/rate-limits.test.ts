import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AuthOutcome, DenyScope, InternalReason, RuleId } from '../src/index.ts';
import { buildTestGuard, FIXED_DURATION, settle } from './helpers.ts';

test('来源维度独立限速：超限后锁定来源并给出可观测证据', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 3,
    sourceWindowMs: 60_000,
    sourceDenyTtlMs: 120_000,
    accountMaxAttempts: 100,
  });

  for (let i = 0; i < 3; i += 1) {
    const response = await guard.authenticate({
      account: `user-${i}`,
      password: 'x',
      source: '203.0.113.7',
    });
    await settle(clock);
    assert.notEqual(response.result.code, 'rate_limited');
  }

  const blocked = await guard.authenticate({
    account: 'anyone',
    password: 'x',
    source: '203.0.113.7',
  });
  await settle(clock);
  assert.equal(blocked.result.code, 'rate_limited');
  assert.equal(blocked.decision.rule, RuleId.SourceRateLimit);
  assert.equal(blocked.decision.scope, DenyScope.Source);
  assert.equal(blocked.decision.subject, '203.0.113.7');
  assert.equal(blocked.decision.evidence?.maxAttempts, 3);
  assert.equal(blocked.decision.retryAfterSeconds, 120);

  // 后续请求在锁定期内被持续拦截，且事件只记录一次。
  const again = await guard.authenticate({
    account: 'anyone',
    password: 'x',
    source: '203.0.113.7',
  });
  await settle(clock);
  assert.equal(again.result.code, 'rate_limited');

  const sourceEvents = guard
    .listLockEvents()
    .filter((event) => event.rule === RuleId.SourceRateLimit);
  assert.equal(sourceEvents.length, 1);
  assert.equal(sourceEvents[0].evidence.attemptsInWindow, 4);
});

test('账号维度独立限速：不同来源访问同一账号仍被账号限速拦截', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 3,
    accountWindowMs: 60_000,
    accountLockTtlMs: 90_000,
  });

  for (let i = 0; i < 3; i += 1) {
    const response = await guard.authenticate({
      account: 'alice',
      password: 'wrong',
      source: `198.51.100.${i}`,
    });
    await settle(clock);
    assert.equal(response.result.code, 'invalid_credentials');
  }

  const blocked = await guard.authenticate({
    account: 'alice',
    password: 'wrong',
    source: '198.51.100.99',
  });
  await settle(clock);
  assert.equal(blocked.result.code, 'account_locked');
  assert.equal(blocked.decision.rule, RuleId.AccountRateLimit);
  assert.equal(blocked.decision.scope, DenyScope.Account);
  assert.equal(blocked.decision.subject, 'alice');

  // 另一账号不受影响。
  const other = await guard.authenticate({
    account: 'bob',
    password: 'battery-staple',
    source: '198.51.100.99',
  });
  await settle(clock);
  assert.equal(other.result.code, 'ok');
});

test('账号连续失败达到阈值后锁定，成功登录会清零计数', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 100,
    accountLockoutThreshold: 4,
  });

  for (let i = 0; i < 3; i += 1) {
    const response = await guard.authenticate({
      account: 'bob',
      password: 'bad',
      source: `172.16.0.${i}`,
    });
    await settle(clock);
    assert.equal(response.result.code, 'invalid_credentials');
  }

  const success = await guard.authenticate({
    account: 'bob',
    password: 'battery-staple',
    source: '172.16.0.9',
  });
  await settle(clock);
  assert.equal(success.result.outcome, AuthOutcome.Success);

  // 计数已清零，再失败 3 次不会触发阈值为 4 的锁定。
  for (let i = 0; i < 3; i += 1) {
    const response = await guard.authenticate({
      account: 'bob',
      password: 'bad',
      source: `172.17.0.${i}`,
    });
    await settle(clock);
    assert.equal(response.result.code, 'invalid_credentials');
  }
  assert.equal(
    guard.listLockEvents().filter((event) => event.rule === RuleId.AccountLockout).length,
    0,
  );

  // 再失败一次（累计第 4 次）触发连续失败锁定。
  const locked = await guard.authenticate({
    account: 'bob',
    password: 'bad',
    source: '172.17.0.8',
  });
  await settle(clock);
  assert.equal(locked.result.code, 'account_locked');
  assert.equal(locked.decision.rule, RuleId.AccountLockout);
  assert.equal(locked.decision.reason, InternalReason.Locked);
  assert.equal(locked.decision.evidence?.consecutiveFailures, 4);
});

test('限速窗口滑过后恢复，且所有响应仍保持固定耗时', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 2,
    sourceWindowMs: 60_000,
    sourceDenyTtlMs: 30_000,
    accountMaxAttempts: 100,
  });

  for (let i = 0; i < 2; i += 1) {
    await guard.authenticate({ account: 'alice', password: 'bad', source: '10.9.0.1' });
    await settle(clock);
  }
  const blocked = await guard.authenticate({
    account: 'alice',
    password: 'bad',
    source: '10.9.0.1',
  });
  await settle(clock);
  assert.equal(blocked.result.code, 'rate_limited');

  // 拒绝清单 TTL 到期后，且限速窗口也已滑过，来源恢复可用。
  clock.advance(61_000);
  const startedAt = clock.now();
  const recovered = await guard.authenticate({
    account: 'alice',
    password: 'correct-horse',
    source: '10.9.0.1',
  });
  await settle(clock);
  assert.equal(clock.now() - startedAt, FIXED_DURATION);
  assert.equal(recovered.result.code, 'ok');
});
