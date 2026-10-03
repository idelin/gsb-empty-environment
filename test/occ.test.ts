import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OccEngine } from '../src/occ/engine.ts';
import { OccConflictError } from '../src/occ/errors.ts';

test('三阶段执行：无冲突时写入阶段一次性批量提交，结果对其他事务可见', async () => {
  const engine = new OccEngine<string, number>();
  await engine.run((txn) => {
    txn.set('a', 1);
    txn.set('b', 2);
  });
  assert.equal(engine.store.getCommitted('a')?.value, 1);
  assert.equal(engine.store.getCommitted('b')?.value, 2);
  // 同一事务的批量写入共享同一个提交版本号
  assert.equal(engine.store.versionOf('a'), engine.store.versionOf('b'));
});

test('读取阶段可读到本事务未提交的写入（read-your-writes）', async () => {
  const engine = new OccEngine<string, number>();
  await engine.run((txn) => {
    txn.set('a', 41);
    const v = txn.get('a');
    txn.set('b', (v ?? 0) + 1);
  });
  assert.equal(engine.store.getCommitted('b')?.value, 42);
});

test('验证阶段精准识别 ReadSet 版本过期，回滚干净利落', () => {
  const engine = new OccEngine<string, number>();
  const t1 = engine.begin();
  const t2 = engine.begin();

  t1.get('x'); // 两个事务都读到 x 的版本 0
  t2.get('x');

  t1.set('x', 1);
  t1.set('y', 100);
  engine.commit(t1); // t1 先提交

  t2.set('x', 2);
  t2.set('z', 200);
  let caught: unknown;
  try {
    engine.commit(t2);
  } catch (err) {
    caught = err;
  }

  assert.ok(caught instanceof OccConflictError);
  assert.deepEqual([...(caught as OccConflictError<string>).conflictingKeys], ['x']);
  // 干净回滚：t2 的任何缓冲写入都未生效
  assert.equal(engine.store.getCommitted('x')?.value, 1);
  assert.equal(engine.store.getCommitted('y')?.value, 100);
  assert.equal(engine.store.getCommitted('z'), undefined);
  assert.equal(t2.txnState, 'aborted');
});

test('验证仅报告真正过期的键，不误伤其他键', async () => {
  const engine = new OccEngine<string, number>();
  await engine.run((txn) => {
    txn.set('x', 1);
    txn.set('y', 2);
  });

  const t1 = engine.begin();
  t1.get('x');
  t1.get('y');

  const t2 = engine.begin();
  t2.set('y', 20); // 只改 y
  engine.commit(t2);

  t1.set('w', 9);
  let caught: unknown;
  try {
    engine.commit(t1);
  } catch (err) {
    caught = err;
  }
  assert.ok(caught instanceof OccConflictError);
  assert.deepEqual([...(caught as OccConflictError<string>).conflictingKeys], ['y']);
  assert.equal(engine.store.getCommitted('w'), undefined);
});

test('幻读检测：读取时不存在的键被其他事务创建，验证失败', () => {
  const engine = new OccEngine<string, number>();
  const t1 = engine.begin();
  assert.equal(t1.get('ghost'), undefined); // 记录版本 0

  const t2 = engine.begin();
  t2.set('ghost', 1);
  engine.commit(t2);

  assert.throws(() => engine.commit(t1), OccConflictError);
});

test('run 自动重试：冲突后重放事务并最终提交，冲突率统计准确', async () => {
  const engine = new OccEngine<string, number>();
  let first = true;
  await engine.run(async (txn) => {
    const v = txn.get('n') ?? 0;
    if (first) {
      first = false;
      // 模拟另一个事务在本事务验证前抢先提交
      const other = engine.begin();
      other.set('n', 999);
      engine.commit(other);
    }
    txn.set('n', v + 1);
  });

  assert.equal(engine.store.getCommitted('n')?.value, 1000);
  const stats = engine.getStats();
  assert.equal(stats.attempts, 3);
  assert.equal(stats.commits, 2);
  assert.equal(stats.aborts, 1);
  assert.ok(Math.abs(stats.conflictRate - 1 / 3) < 1e-9);
  assert.equal(stats.conflictsByKey.get('n'), 1);
});

test('只读事务通过验证但不产生新的提交版本', async () => {
  const engine = new OccEngine<string, number>();
  await engine.run((txn) => {
    txn.set('a', 1);
  });
  const versionBefore = engine.store.versionOf('a');
  await engine.run((txn) => {
    txn.get('a');
  });
  assert.equal(engine.store.versionOf('a'), versionBefore);
  assert.equal(engine.getStats().commits, 2);
});
