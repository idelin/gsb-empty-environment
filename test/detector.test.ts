import assert from 'node:assert/strict';
import { test } from 'node:test';
import { DenyScope, InternalReason, RuleId } from '../src/index.ts';
import { buildTestGuard, settle } from './helpers.ts';

test('识别轮换来源的低速填充并触发全局锁定，事件携带判定依据', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 100,
    accountLockoutThreshold: 100,
    fillWindowMs: 10 * 60_000,
    fillDistinctSources: 5,
    fillMinFailures: 8,
    globalLockTtlMs: 5 * 60_000,
  });

  // 每个来源只低速贡献 1~2 次，单来源限速永远不会触发。
  const failureCount = 8;
  let triggered = null;
  for (let i = 0; i < failureCount; i += 1) {
    const source = `10.50.0.${i % 5}`;
    const response = await guard.authenticate({
      account: `probe-${i}`,
      password: 'guess',
      source,
    });
    await settle(clock);
    if (response.decision.rule === RuleId.RotatingSourceLowSlowFill) {
      triggered = response;
    }
    // 让尝试在时间上铺开，模拟「低速」填充。
    clock.advance(30_000);
  }

  assert.ok(triggered, '应在第 8 次失败（5 个唯一来源）时触发全局锁定');
  assert.equal(triggered?.result.code, 'service_locked');
  assert.equal(triggered?.decision.scope, DenyScope.Global);
  assert.equal(triggered?.decision.reason, InternalReason.GlobalLocked);
  assert.equal(triggered?.decision.evidence?.distinctSources, 5);
  assert.equal(triggered?.decision.evidence?.distinctSourceThreshold, 5);
  assert.equal(triggered?.decision.evidence?.failures, 8);
  assert.equal(triggered?.decision.evidence?.failureThreshold, 8);

  // 全局锁定后，任意来源 / 账号都被拦截。
  const locked = await guard.authenticate({
    account: 'alice',
    password: 'correct-horse',
    source: '8.8.8.8',
  });
  await settle(clock);
  assert.equal(locked.result.code, 'service_locked');
  assert.equal(locked.decision.reason, InternalReason.GlobalLocked);

  const event = guard
    .listLockEvents()
    .find((item) => item.rule === RuleId.RotatingSourceLowSlowFill);
  assert.ok(event);
  assert.equal(event?.scope, DenyScope.Global);
  assert.ok(event?.lockedUntil > event?.at);

  // 观测快照暴露全局聚合指标。
  const snapshot = guard.snapshot();
  assert.equal(snapshot.fillDetection.global.distinctSources, 5);
  assert.equal(snapshot.fillDetection.global.failures, 8);
});

test('检测窗口外的失败不计入低速填充', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 100,
    fillWindowMs: 60_000,
    fillDistinctSources: 3,
    fillMinFailures: 4,
  });

  // 先制造一批落在窗口外的旧失败。
  for (let i = 0; i < 4; i += 1) {
    await guard.authenticate({
      account: `old-${i}`,
      password: 'x',
      source: `10.60.0.${i}`,
    });
    await settle(clock);
    clock.advance(20_000);
  }
  // 跳过整个检测窗口。
  clock.advance(61_000);

  // 窗口内只有 2 次失败，不应触发。
  for (let i = 0; i < 2; i += 1) {
    const response = await guard.authenticate({
      account: `new-${i}`,
      password: 'x',
      source: `10.70.0.${i}`,
    });
    await settle(clock);
    assert.equal(response.result.code, 'invalid_credentials');
  }
  assert.equal(
    guard.listLockEvents().filter((e) => e.rule === RuleId.RotatingSourceLowSlowFill).length,
    0,
  );
  assert.deepEqual(guard.snapshot().fillDetection.global, { failures: 2, distinctSources: 2 });
});

test('单账号被多个不同来源喷洒时锁定该账号', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 100,
    accountLockoutThreshold: 100,
    fillWindowMs: 10 * 60_000,
    fillDistinctSources: 1000,
    fillMinFailures: 1000,
    accountSprayDistinctSources: 4,
  });

  let triggered = null;
  for (let i = 0; i < 4; i += 1) {
    const response = await guard.authenticate({
      account: 'alice',
      password: 'spray-guess',
      source: `192.0.2.${i}`,
    });
    await settle(clock);
    if (response.decision.rule === RuleId.AccountSourceSpray) {
      triggered = response;
    }
  }

  assert.ok(triggered, '第 4 个不同来源失败时应触发喷洒锁定');
  assert.equal(triggered?.result.code, 'account_locked');
  assert.equal(triggered?.decision.rule, RuleId.AccountSourceSpray);
  assert.equal(triggered?.decision.scope, DenyScope.Account);
  assert.equal(triggered?.decision.subject, 'alice');
  assert.equal(triggered?.decision.evidence?.distinctSources, 4);

  const snapshot = guard.snapshot();
  assert.equal(snapshot.fillDetection.accounts.alice?.distinctSources, 4);
});

test('同一来源的重复失败不会被误判为多来源喷洒', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 100,
    accountLockoutThreshold: 100,
    fillDistinctSources: 1000,
    fillMinFailures: 1000,
    accountSprayDistinctSources: 3,
  });

  for (let i = 0; i < 6; i += 1) {
    const response = await guard.authenticate({
      account: 'alice',
      password: 'bad',
      source: '198.18.0.1',
    });
    await settle(clock);
    assert.equal(response.result.code, 'invalid_credentials');
  }
  assert.equal(
    guard.listLockEvents().filter((e) => e.rule === RuleId.AccountSourceSpray).length,
    0,
  );
});
