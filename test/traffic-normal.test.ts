import assert from 'node:assert/strict';
import { test } from 'node:test';
import { AuthOutcome, DenyScope, RuleId } from '../src/index.ts';
import { buildTestGuard, FIXED_DURATION, settle } from './helpers.ts';

/**
 * 正常登录流量：多个合法用户在不同来源、不同时间正常登录，
 * 夹杂少量偶发的密码输错。断言不产生任何锁定，且响应表现一致。
 */
test('正常流量：合法用户登录全部成功且不触发任何防护', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 20,
    accountMaxAttempts: 10,
    accountLockoutThreshold: 5,
    fillDistinctSources: 50,
    fillMinFailures: 50,
    accountSprayDistinctSources: 10,
  });

  const sessions: Array<{ account: string; source: string }> = [
    { account: 'alice', source: '203.0.113.10' },
    { account: 'bob', source: '203.0.113.11' },
    { account: 'alice', source: '203.0.113.12' },
    { account: 'bob', source: '203.0.113.13' },
  ];
  const credentials: Record<string, string> = {
    alice: 'correct-horse',
    bob: 'battery-staple',
  };

  const durations: number[] = [];
  for (const session of sessions) {
    const startedAt = clock.now();
    const response = await guard.authenticate({
      account: session.account,
      password: credentials[session.account],
      source: session.source,
    });
    await settle(clock);
    durations.push(clock.now() - startedAt);
    assert.equal(response.result.outcome, AuthOutcome.Success);
    assert.equal(response.result.code, 'ok');
    clock.advance(120_000);
  }

  assert.deepEqual(durations, sessions.map(() => FIXED_DURATION));
  assert.equal(guard.listLockEvents().length, 0);
  assert.equal(guard.activeLocks().length, 0);
});

test('正常流量：偶发输错后再输对，不会锁定且最终成功', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 20,
    accountMaxAttempts: 20,
    accountLockoutThreshold: 3,
  });

  const typo = await guard.authenticate({
    account: 'alice',
    password: 'typo',
    source: '203.0.113.20',
  });
  await settle(clock);
  assert.equal(typo.result.code, 'invalid_credentials');

  const ok = await guard.authenticate({
    account: 'alice',
    password: 'correct-horse',
    source: '203.0.113.20',
  });
  await settle(clock);
  assert.equal(ok.result.code, 'ok');
  assert.equal(guard.listLockEvents().length, 0);
});

test('两类流量对比：枚举失败与正常输错的对外结果完全相同', async () => {
  const { guard: enumGuard, clock: enumClock } = await buildTestGuard();
  const { guard: normalGuard, clock: normalClock } = await buildTestGuard();

  const enumStart = enumClock.now();
  const enumResponse = await enumGuard.authenticate({
    account: 'no-such-user',
    password: 'guess',
    source: '10.100.0.1',
  });
  settle(enumClock);
  const enumElapsed = enumClock.now() - enumStart;

  const normalStart = normalClock.now();
  const normalResponse = await normalGuard.authenticate({
    account: 'alice',
    password: 'wrong-password',
    source: '10.100.0.2',
  });
  settle(normalClock);
  const normalElapsed = normalClock.now() - normalStart;

  assert.deepEqual(enumResponse.result, normalResponse.result);
  assert.equal(enumElapsed, normalElapsed);
  assert.equal(enumElapsed, FIXED_DURATION);
});

test('正常流量不受其它账号被锁定的影响', async () => {
  const { guard, clock } = await buildTestGuard({
    sourceMaxAttempts: 100,
    accountMaxAttempts: 2,
  });

  // 把 alice 打到账号限速锁定。
  for (let i = 0; i < 3; i += 1) {
    await guard.authenticate({ account: 'alice', password: 'bad', source: `10.101.0.${i}` });
    await settle(clock);
  }
  const aliceLocked = guard
    .activeLocks()
    .some((lock) => lock.scope === DenyScope.Account && lock.subject === 'alice');
  assert.ok(aliceLocked);

  const bob = await guard.authenticate({
    account: 'bob',
    password: 'battery-staple',
    source: '10.101.0.9',
  });
  await settle(clock);
  assert.equal(bob.result.code, 'ok');

  const events = guard.listLockEvents();
  assert.ok(events.every((event) => event.rule !== RuleId.RotatingSourceLowSlowFill));
});
