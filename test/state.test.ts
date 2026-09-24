import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReactiveCache, Store, Ledger, BlockStore, BudgetExceededError, verifyLedger } from '../src/index.ts';
import { barrier } from './helpers.ts';

test('事务原子提交：读者看不到中间态', async () => {
  const c = new ReactiveCache({ budget: 100_000, cacheBudget: 10_000 });
  c.defineSource('a', 1);
  c.defineSource('b', 2);

  const gate = barrier();
  const txPromise = c.transact(async (tx) => {
    tx.set('a', 10);
    await gate.p; // 事务中途让出事件循环
    tx.set('b', 20);
  });

  // 提交前读取：必须看到完整的旧状态
  const before = c.snapshot();
  assert.equal(before.get('a'), 1);
  assert.equal(before.get('b'), 2);

  gate.release();
  await txPromise;

  // 提交后：两个写入同时可见
  const after = c.snapshot();
  assert.equal(after.get('a'), 10);
  assert.equal(after.get('b'), 20);
  assert.ok(after.version > before.version);
  // 旧快照不受提交影响
  assert.equal(before.get('a'), 1);
});

test('提交失败整体回滚：状态与账本保持不变', async () => {
  const probe = new ReactiveCache({ budget: 1_000_000, cacheBudget: 0 });
  probe.defineSource('small', 'x');
  probe.defineSource('big', '');
  const baseUsed = probe.ledger.used;

  const c = new ReactiveCache({ budget: baseUsed + 16, cacheBudget: 0 });
  c.defineSource('small', 'x');
  c.defineSource('big', '');
  const usedBefore = c.ledger.used;

  await assert.rejects(
    c.transact((tx) => {
      tx.set('small', 'ok');
      tx.set('big', 'y'.repeat(10_000)); // 超预算
    }),
    BudgetExceededError,
  );
  assert.equal(c.store.readCell('small'), 'x'); // 未部分提交
  assert.equal(c.store.readCell('big'), '');
  assert.equal(c.ledger.used, usedBefore); // 账本无残留
  assert.ok(verifyLedger(c).ok);
});

test('操作日志可回放：重建出等价状态', async () => {
  const c = new ReactiveCache({ budget: 100_000, cacheBudget: 10_000 });
  c.defineSource('x', 1);
  c.defineSource('y', [1, 2, 3]);
  await c.transact((tx) => {
    tx.set('x', 42);
    tx.set('y', [4, 5, 6]);
  });
  await c.transact((tx) => tx.set('x', 100));

  const log = c.opLog;
  const ledger2 = new Ledger(100_000);
  const store2 = Store.replay(log, new BlockStore(ledger2));
  assert.deepEqual(store2.snapshot().toObject(), c.store.snapshot().toObject());
  assert.equal(store2.storeVersion, c.store.storeVersion);

  // 系统级回放：派生定义由回调重新声明，派生值一致
  const define = (cc: ReactiveCache): void => {
    cc.defineDerived('sumY', ['y'], ([y]) => (y as number[]).reduce((a, b) => a + b, 0));
    cc.defineDerived('total', ['x', 'sumY'], ([x, s]) => (x as number) + (s as number));
  };
  const c2 = ReactiveCache.replay(log, { budget: 100_000, cacheBudget: 10_000 }, define);
  define(c);
  assert.equal(await c2.read('total'), await c.read('total'));
  assert.ok(verifyLedger(c2).ok);
});

test('每次提交递增被写单元版本', async () => {
  const c = new ReactiveCache({ budget: 100_000, cacheBudget: 10_000 });
  c.defineSource('a', 1);
  c.defineSource('b', 1);
  assert.equal(c.store.cellVersion('a'), 0);
  await c.transact((tx) => tx.set('a', 2));
  assert.equal(c.store.cellVersion('a'), 1);
  assert.equal(c.store.cellVersion('b'), 0);
  await c.transact((tx) => {
    tx.set('a', 3);
    tx.set('b', 3);
  });
  assert.equal(c.store.cellVersion('a'), 2);
  assert.equal(c.store.cellVersion('b'), 1);
});
