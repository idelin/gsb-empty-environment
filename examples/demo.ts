/**
 * 运行样例：派生值缓存的完整生命周期演示。
 * 运行：node --experimental-strip-types examples/demo.ts
 */
import { ReactiveCache, SubscriptionRejectedError, verifyLedger, formatReport } from '../src/index.ts';

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// ---------- 1. 基本派生 + 物理共享块计费 ----------
show('1. 派生计算与物理共享块计费');
const c = new ReactiveCache({ budget: 4_096, cacheBudget: 512 });
c.defineSource('items', [3, 1, 2]);
c.defineSource('factor', 10);
c.defineDerived('sorted', ['items'], ([a]) => [...(a as number[])].sort((x, y) => x - y));
c.defineDerived('scaled', ['sorted', 'factor'], ([s, f]) => (s as number[]).map((v) => v * (f as number)));
c.defineDerived('report', ['sorted'], ([s]) => ({ data: s, n: (s as number[]).length }));

console.log('sorted =', await c.read('sorted'));
console.log('scaled =', await c.read('scaled'));
console.log('report =', JSON.stringify(await c.read('report')));
const itemsBlock = c.store.cellBlock('items');
console.log(`物理账本: ${c.ledger.used}B / ${c.ledger.budget}B, 缓存足迹: ${c.cacheBytes}B / ${c.cacheBudget}B`);
const sortedBlock = c.derivedNodes().find((n) => n.id === 'sorted')?.entry?.block as number;
console.log(
  `sorted 数组块 #${sortedBlock} 引用数 = ${c.blocks.refsOf(sortedBlock)}（sorted 缓存项 + report 子图共享同一物理块，账本只计一次）`,
);

// ---------- 2. 订阅保留 + 固定策略驱逐 ----------
show('2. 订阅保留与驱逐');
const sub = await c.subscribe('sorted');
console.log('已订阅 sorted（保留，不参与驱逐）');
console.log('flush 驱逐:', c.flush(), '（sorted 被保留）');
console.log(`sorted 仍在缓存: ${c.derivedNodes().find((n) => n.id === 'sorted')?.entry !== null}`);
console.log(`订阅保留占用: ${c.pinnedBytes}B <= 缓存预算 ${c.cacheBudget}B（可证明上界）`);

// ---------- 3. 驱逐后首次读重算且结果相同 ----------
show('3. 驱逐后重算结果一致');
const before = await c.read('scaled');
const evictedCount = c.flush().length;
const after = await c.read('scaled');
console.log(`驱逐 ${evictedCount} 项后重读 scaled: ${JSON.stringify(before)} -> ${JSON.stringify(after)}（相同）`);

// ---------- 4. 依赖变更不命中旧缓存 + 异步重算与驱逐交错 ----------
show('4. 异步重算期间依赖变化：旧结果绝不重新发布');
const c2 = new ReactiveCache({ budget: 4_096, cacheBudget: 512 });
c2.defineSource('x', 1);
let resume!: () => void;
const gate = new Promise<void>((r) => (resume = r));
c2.defineDerived('y', ['x'], async ([x]) => {
  await gate; // 计算中途挂起，模拟慢计算
  return (x as number) * 10;
});
const pending = c2.read('y');
await new Promise((r) => setImmediate(r)); // 让重算开始
await c2.transact((tx) => tx.set('x', 2)); // 计算期间依赖变化
c2.flush(); // 同时发生驱逐
resume();
console.log('y =', await pending, '（基于新依赖 x=2，旧结果 10 被丢弃, staleDiscards =', c2.stats.staleDiscards, '）');
await c2.scheduler.drain();
console.log('调度器静默性检查:', c2.scheduler.checkQuiescent().length === 0 ? '通过（借用与预留全部释放）' : '失败');

// ---------- 5. 订阅准入：超预算明确拒绝 ----------
show('5. 订阅保留超预算时明确拒绝');
const c3 = new ReactiveCache({ budget: 4_096, cacheBudget: 150 });
c3.defineSource('big1', 'x'.repeat(100));
c3.defineSource('big2', 'y'.repeat(100));
c3.defineDerived('d1', ['big1'], ([v]) => v);
c3.defineDerived('d2', ['big2'], ([v]) => v);
await c3.subscribe('d1');
console.log(`d1 保留成功（pinned=${c3.pinnedBytes}B）`);
try {
  await c3.subscribe('d2');
  console.log('d2 保留成功（不该发生）');
} catch (err) {
  console.log('d2 保留被拒绝:', err instanceof SubscriptionRejectedError ? (err as Error).message : err);
}

// ---------- 6. 缓存 0 容量仍功能正确 ----------
show('6. 缓存 0 容量');
const c0 = new ReactiveCache({ budget: 4_096, cacheBudget: 0 });
c0.defineSource('a', 2);
c0.defineSource('b', 5);
c0.defineDerived('combo', ['a', 'b'], ([a, b]) => ({ sum: (a as number) + (b as number), prod: (a as number) * (b as number) }));
console.log('combo =', JSON.stringify(await c0.read('combo')));
await c0.transact((tx) => tx.set('a', 4));
console.log('变更后 combo =', JSON.stringify(await c0.read('combo')), '（每次读都重算，结果正确）');

// ---------- 7. 状态操作回放 ----------
show('7. 操作日志回放');
await c.transact((tx) => {
  tx.set('factor', 100);
  tx.set('items', [9, 7, 8]);
});
const replayed = ReactiveCache.replay(c.opLog, { budget: 4_096, cacheBudget: 512 }, (cc) => {
  cc.defineDerived('sorted', ['items'], ([a]) => [...(a as number[])].sort((x, y) => x - y));
  cc.defineDerived('scaled', ['sorted', 'factor'], ([s, f]) => (s as number[]).map((v) => v * (f as number)));
});
console.log('原系统 scaled =', await c.read('scaled'), ' 回放系统 scaled =', await replayed.read('scaled'));
console.log('快照一致:', JSON.stringify(replayed.snapshot().toObject()) === JSON.stringify(c.snapshot().toObject()));

// ---------- 8. 独立遍历核验 ----------
show('8. 独立引用遍历核验账本');
sub.unsubscribe();
console.log(formatReport(verifyLedger(c)));
console.log(formatReport(verifyLedger(c2)));
