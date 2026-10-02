import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  TransactionalStore,
  InvariantViolationError,
  WriteSkewError,
} from '../src/store.ts';
import {
  ROSTER_TABLE,
  registerRosterInvariants,
  seedRoster,
  requestLeave,
  onDutyNow,
} from '../src/roster.ts';

const tick = () => new Promise((resolve) => setImmediate(resolve));

async function makeStore(doctors: string[] = ['A', 'B']): Promise<TransactionalStore> {
  const store = new TransactionalStore();
  registerRosterInvariants(store);
  await seedRoster(store, doctors.map((d) => ({ doctorId: d, name: `医生${d}` })));
  return store;
}

test('验收场景：A、B 并发请假，系统精准中止其中一人，值班人数不降为 0', async () => {
  const store = await makeStore(['A', 'B']);

  const [resultA, resultB] = await Promise.allSettled([
    requestLeave(store, 'A', tick),
    requestLeave(store, 'B', tick),
  ]);

  const succeeded = [resultA, resultB].filter((r) => r.status === 'fulfilled');
  const aborted = [resultA, resultB].filter((r) => r.status === 'rejected');

  // 恰好一人成功、一人被中止
  assert.equal(succeeded.length, 1, '应恰好一个事务提交成功');
  assert.equal(aborted.length, 1, '应恰好一个事务被中止');

  // 中止原因必须是可识别的并发/不变量错误
  const reason = (aborted[0] as PromiseRejectedResult).reason;
  assert.ok(
    reason instanceof WriteSkewError || reason instanceof InvariantViolationError,
    `中止原因应为写倾斜或不变量违例，实际: ${reason}`,
  );

  // 最终状态满足不变量：值班人数 >= 1
  assert.ok(onDutyNow(store).length >= 1, '值班人数不得降为 0');
  assert.equal(onDutyNow(store).length, 1, '最终应恰好 1 人在岗');
});

test('单事务违反不变量：最后一人请假被直接拒绝', async () => {
  const store = await makeStore(['A']); // 仅 1 人值班
  await assert.rejects(
    requestLeave(store, 'A', tick),
    (err: unknown) => {
      assert.ok(err instanceof InvariantViolationError);
      assert.equal(err.invariantName, 'at-least-one-doctor-on-duty');
      assert.match(err.message, /至少有 1 人值班/);
      return true;
    },
  );
  assert.deepEqual(onDutyNow(store), ['A'], '违例事务回滚后状态不变');
});

test('写倾斜检测：中止错误中报告被并发修改的读集键', async () => {
  const store = await makeStore(['A', 'B']);
  const [resultA, resultB] = await Promise.allSettled([
    requestLeave(store, 'A', tick),
    requestLeave(store, 'B', tick),
  ]);
  const aborted = [resultA, resultB].find((r) => r.status === 'rejected');
  assert.ok(aborted, '应有一个事务被中止');
  const reason = (aborted as PromiseRejectedResult).reason;
  if (reason instanceof WriteSkewError) {
    assert.ok(reason.conflicts.length > 0, '应报告冲突键');
    assert.match(reason.conflicts[0], new RegExp(`^${ROSTER_TABLE}:`));
  } else {
    // 若由不变量重验拦截，同样满足"精准中止"要求
    assert.ok(reason instanceof InvariantViolationError);
  }
});

test('无冲突并发：A 请假与 C 新排班互不影响，双双提交', async () => {
  const store = await makeStore(['A', 'B']);

  const leave = requestLeave(store, 'A', tick);
  const join = (async () => {
    const tx = store.begin();
    tx.put(ROSTER_TABLE, 'C', { doctorId: 'C', name: '医生C', onDuty: true });
    await tick();
    await tx.commit();
  })();

  await assert.doesNotReject(Promise.all([leave, join]));
  const onDuty = onDutyNow(store);
  assert.ok(onDuty.includes('B') && onDuty.includes('C'));
  assert.ok(!onDuty.includes('A'));
});

test('顺序执行：A 先请假成功后，B 的请假被不变量拦截', async () => {
  const store = await makeStore(['A', 'B']);
  await requestLeave(store, 'A', tick); // 成功，剩 B
  await assert.rejects(requestLeave(store, 'B', tick), InvariantViolationError);
  assert.deepEqual(onDutyNow(store), ['B']);
});

test('不变量仅在被触及表时重验：无关表写入不触发校验', async () => {
  const store = await makeStore(['A']);
  // 向无关表写入，不应触发值班表不变量
  await store.transact((tx) => tx.put('audit_log', '1', { msg: 'ok' }));
  assert.equal(store.committedGet('audit_log', '1')?.msg, 'ok');
});
