import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AuthOutcome, DenyScope, RuleId } from '../src/index.ts';
import { buildTestGuard, FIXED_DURATION, settle } from './helpers.ts';

/**
 * 枚举流量：批量探测大量账号，混合「存在的账号 + 错误密码」与
 * 「不存在的账号」。断言两类探测在结构与耗时上完全一致。
 */
test('枚举流量：存在账号与不存在账号对外不可区分', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 1000,
    accountMaxAttempts: 1000,
    accountLockoutThreshold: 1000,
    fillDistinctSources: 1000,
    fillMinFailures: 1000,
    accountSprayDistinctSources: 1000,
  });

  const accounts = [
    'alice',
    'does-not-exist-1',
    'bob',
    'does-not-exist-2',
    'carol-not-registered',
    'alice',
    'ghost-3',
    'bob',
  ];

  const durations = new Map<string, number>();
  const results = new Map<string, unknown>();

  for (const account of accounts) {
    const startedAt = clock.now();
    const response = await guard.authenticate({
      account,
      password: 'enumerated-password',
      source: `172.20.0.${accounts.indexOf(account) + 1}`,
    });
    await settle(clock);
    const elapsed = clock.now() - startedAt;
    durations.set(account, elapsed);
    results.set(account, response.result);
  }

  for (const account of accounts) {
    assert.equal(durations.get(account), FIXED_DURATION, `${account} 耗时不一致`);
    assert.deepEqual(
      results.get(account),
      {
        outcome: AuthOutcome.Rejected,
        code: 'invalid_credentials',
        message: '账号或密码错误',
      },
      `${account} 响应结构不一致`,
    );
  }
});

test('枚举流量：低速轮换来源最终被全局锁定，且锁定依据可观测', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 100,
    fillDistinctSources: 6,
    fillMinFailures: 10,
    fillWindowMs: 10 * 60_000,
  });

  let serviceLockedAt = -1;
  for (let i = 0; i < 12; i += 1) {
    const response = await guard.authenticate({
      account: `enum-${i}@example.com`,
      password: 'common-password',
      source: `10.80.0.${i % 8}`,
    });
    await settle(clock);
    if (response.result.code === 'service_locked' && serviceLockedAt === -1) {
      serviceLockedAt = i;
    }
    clock.advance(20_000);
  }

  assert.notEqual(serviceLockedAt, -1);
  const event = guard
    .listLockEvents()
    .find((item) => item.rule === RuleId.RotatingSourceLowSlowFill);
  assert.ok(event);
  assert.equal(event?.scope, DenyScope.Global);
  assert.ok((event?.evidence.failures as number) >= 10);
  assert.ok((event?.evidence.distinctSources as number) >= 6);
  // 事件包含完整的阈值上下文，可直接用于告警与审计。
  assert.ok('failureThreshold' in (event?.evidence ?? {}));
  assert.ok('distinctSourceThreshold' in (event?.evidence ?? {}));
});

test('枚举流量：恒定耗时对限速 / 锁定响应同样生效', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 2,
    accountMaxAttempts: 100,
  });

  const observed: number[] = [];
  for (let i = 0; i < 4; i += 1) {
    const startedAt = clock.now();
    await guard.authenticate({
      account: `x-${i}`,
      password: 'p',
      source: '10.90.0.1',
    });
    await settle(clock);
    observed.push(clock.now() - startedAt);
  }
  assert.deepEqual(observed, [FIXED_DURATION, FIXED_DURATION, FIXED_DURATION, FIXED_DURATION]);
});
