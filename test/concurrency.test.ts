import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OccEngine } from '../src/occ/engine.ts';
import { sleep } from '../src/util/sleep.ts';

test('高并发单一热点键：全部提交且最终值精确，冲突率被统计', async () => {
  const engine = new OccEngine<string, number>({ maxRetries: 200 });
  const workers = 64;
  await Promise.all(
    Array.from({ length: workers }, () =>
      engine.run(async (txn) => {
        const v = txn.get('hot') ?? 0;
        await sleep(Math.random() * 2); // 模拟业务延迟，制造读写重叠窗口
        txn.set('hot', v + 1);
      }),
    ),
  );

  assert.equal(engine.store.getCommitted('hot')?.value, workers);
  const stats = engine.getStats();
  assert.ok(stats.aborts > 0, '热点键上应发生冲突');
  assert.equal(stats.attempts, stats.commits + stats.aborts);
  assert.ok(stats.conflictRate > 0 && stats.conflictRate < 1);
  assert.equal(stats.conflictsByKey.get('hot'), stats.aborts);
  console.log(
    `  [热点键] 尝试=${stats.attempts} 提交=${stats.commits} 中止=${stats.aborts} ` +
      `冲突率=${(stats.conflictRate * 100).toFixed(1)}%`,
  );
});

test('高并发不同热点键（无重叠写）：零冲突全部提交', async () => {
  const engine = new OccEngine<string, number>();
  const workers = 32;
  await Promise.all(
    Array.from({ length: workers }, (_, i) =>
      engine.run(async (txn) => {
        const key = `k${i}`;
        const v = txn.get(key) ?? 0;
        await sleep(Math.random() * 2);
        txn.set(key, v + 1);
      }),
    ),
  );

  const stats = engine.getStats();
  assert.equal(stats.aborts, 0);
  assert.equal(stats.commits, workers);
  for (let i = 0; i < workers; i += 1) {
    assert.equal(engine.store.getCommitted(`k${i}`)?.value, 1);
  }
});

test('混合负载：多热点键读写并发，提交计数与数据总量一致', async () => {
  const hotKeys = 8;
  const workers = 48;
  const txnsEach = 3;
  const engine = new OccEngine<string, number>({ maxRetries: 200 });
  await engine.run((txn) => {
    for (let i = 0; i < hotKeys; i += 1) {
      txn.set(`h${i}`, 0);
    }
  });
  engine.resetStats();

  await Promise.all(
    Array.from({ length: workers }, (_, w) =>
      (async () => {
        for (let t = 0; t < txnsEach; t += 1) {
          const key = `h${(w * 7 + t * 3) % hotKeys}`;
          await engine.run(async (txn) => {
            const v = txn.get(key) ?? 0;
            await sleep(Math.random());
            txn.set(key, v + 1);
          });
        }
      })(),
    ),
  );

  let sum = 0;
  for (let i = 0; i < hotKeys; i += 1) {
    sum += engine.store.getCommitted(`h${i}`)?.value ?? 0;
  }
  assert.equal(sum, workers * txnsEach);
  const stats = engine.getStats();
  assert.equal(stats.commits, workers * txnsEach);
  assert.ok(stats.aborts > 0, '热点键竞争下应发生冲突');
  console.log(
    `  [多热点键] 尝试=${stats.attempts} 提交=${stats.commits} 中止=${stats.aborts} ` +
      `冲突率=${(stats.conflictRate * 100).toFixed(1)}%`,
  );
});
