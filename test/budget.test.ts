import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ReactiveCache,
  BudgetExceededError,
  SubscriptionRejectedError,
  verifyLedger,
} from '../src/index.ts';

test('物理共享块只计一次：结构共享的派生值不重复计费', async () => {
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 100_000 });
  c.defineSource('arr', ['alpha', 'beta', 'gamma']);
  c.defineDerived('identity', ['arr'], ([a]) => a); // 整个值共享同一物理块
  c.defineDerived('wrapped', ['arr'], ([a]) => ({ data: a, tag: 'w' })); // 子结构共享

  await c.read('identity');
  await c.read('wrapped');

  const arrBlock = c.store.cellBlock('arr');
  // cell + identity 缓存项 + wrapped 缓存项（经子图）三方共享
  assert.equal(c.blocks.refsOf(arrBlock), 3);

  // 独立重算缓存足迹：共享块只出现一次
  const reachable = new Set<number>();
  const walk = (id: number): void => {
    if (reachable.has(id)) return;
    reachable.add(id);
    for (const ch of c.childrenOf(id)) walk(ch);
  };
  for (const n of c.derivedNodes()) if (n.entry) walk(n.entry.block);
  let expected = 0;
  for (const id of reachable) expected += c.blocks.sizeOf(id);
  assert.equal(c.cacheBytes, expected);
  assert.ok(verifyLedger(c).ok);
});

test('临时重算预留占额度：重算期间可见，超支则明确拒绝', async () => {
  // 先测量源状态占用，再把预算卡到“源状态 + 少量余量”
  const probe = new ReactiveCache({ budget: 1_000_000, cacheBudget: 0 });
  probe.defineSource('a', 1);
  const sourceBytes = probe.ledger.used;

  // 预算不足以支付重算预留（默认 64B）→ 读取被明确拒绝
  const tight = new ReactiveCache({ budget: sourceBytes + 8, cacheBudget: 0 });
  tight.defineSource('a', 1);
  tight.defineDerived('d', ['a'], ([a]) => (a as number) + 1);
  await assert.rejects(tight.read('d'), BudgetExceededError);
  assert.equal(tight.ledger.pendingReservedBytes, 0); // 失败后无残留预留

  // 预算足够时：重算进行中能观察到预留占额
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 100_000 });
  c.defineSource('a', 1);
  let observedReserved = 0;
  c.defineDerived('d', ['a'], ([a]) => {
    observedReserved = c.ledger.pendingReservedBytes;
    return (a as number) + 1;
  });
  assert.equal(await c.read('d'), 2);
  assert.ok(observedReserved > 0); // 临时重算确实占用了额度
  assert.equal(c.ledger.pendingReservedBytes, 0); // 结束后释放
  assert.ok(verifyLedger(c).ok);
});

test('订阅保留准入：可证明不超预算，否则明确拒绝', async () => {
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 150 });
  c.defineSource('s1', 'x'.repeat(100));
  c.defineSource('s2', 'y'.repeat(100));
  c.defineDerived('d1', ['s1'], ([v]) => v);
  c.defineDerived('d2', ['s2'], ([v]) => v);

  const sub = await c.subscribe('d1'); // 第一个保留（约 104B < 150B）
  assert.ok(c.pinnedBytes > 0);
  assert.ok(c.pinnedBytes <= c.cacheBudget);

  // 第二个保留会使 pinnedBytes 超过缓存预算 → 明确拒绝
  await assert.rejects(c.subscribe('d2'), SubscriptionRejectedError);

  // 被拒绝的值仍可读；但预算已被保留值占满，未订阅值随写随驱
  assert.equal(await c.read('d2'), 'y'.repeat(100));
  assert.equal(c.derivedNodes().find((n) => n.id === 'd2')?.entry, null);
  assert.deepEqual(c.flush(), []);
  // 被保留的 d1 始终在位
  assert.ok(c.derivedNodes().find((n) => n.id === 'd1')?.entry !== null);
  assert.ok(verifyLedger(c).ok);

  sub.unsubscribe();
  assert.equal(c.pinnedBytes, 0);
  assert.ok(verifyLedger(c).ok);
});

test('源状态写入超预算：显式拒绝且状态不变', async () => {
  const probe = new ReactiveCache({ budget: 1_000_000, cacheBudget: 0 });
  probe.defineSource('k', 'v');
  const base = probe.ledger.used;

  const c = new ReactiveCache({ budget: base + 4, cacheBudget: 0 });
  c.defineSource('k', 'v');
  await assert.rejects(c.transact((tx) => tx.set('k', 'z'.repeat(1000))), BudgetExceededError);
  assert.equal(c.store.readCell('k'), 'v');
  assert.ok(verifyLedger(c).ok);
});
