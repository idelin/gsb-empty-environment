import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReactiveCache, verifyLedger } from '../src/index.ts';
import { barrier, deferred } from './helpers.ts';

test('驱逐与异步重算交错：借用保护物理块，结束后资源释放', async () => {
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 100_000 });
  c.defineSource('a', [1, 2, 3]);
  c.defineDerived('b', ['a'], ([a]) => a); // 与 a 共享同一物理块
  const started = deferred<null>();
  const gate = barrier();
  c.defineDerived('c', ['b'], async ([b]) => {
    started.resolve(null);
    await gate.p; // 重算中途挂起，等待测试交错执行驱逐
    return [...(b as number[]), 99];
  });

  await c.read('b');
  const sharedBlock = c.store.cellBlock('a');
  assert.equal(c.derivedNodes().find((n) => n.id === 'b')?.entry?.block, sharedBlock);

  const readC = c.read('c');
  await started.p; // 重算已借用 b 的块

  const evicted = c.flush(); // 驱逐 b 的缓存项
  assert.deepEqual(evicted, ['b']);
  assert.ok(c.blocks.has(sharedBlock)); // 借用中的物理块不被释放

  gate.release();
  assert.deepEqual(await readC, [1, 2, 3, 99]);
  await c.scheduler.drain();

  assert.deepEqual(c.scheduler.checkQuiescent(), []); // 预留与借用全部释放
  assert.equal(c.blocks.refsOf(sharedBlock), 1); // 只剩源单元持有
  assert.ok(verifyLedger(c).ok);
});

test('频繁变化依赖：旧结果绝不重新发布', async () => {
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 100_000 });
  c.defineSource('x', 1);
  const started = deferred<null>();
  const gate = barrier();
  c.defineDerived('y', ['x'], async ([x]) => {
    started.resolve(null);
    await gate.p;
    return (x as number) * 10;
  });

  const p = c.read('y');
  await started.p;
  await c.transact((tx) => tx.set('x', 2)); // 计算期间依赖变化
  gate.release();

  assert.equal(await p, 20); // 发布的是基于新依赖的结果，而非旧结果 10
  assert.equal(c.stats.staleDiscards, 1); // 旧结果被丢弃过一次
  assert.equal(await c.read('y'), 20); // 缓存中的就是最新结果
  assert.equal(c.stats.hits, 1);
  assert.ok(verifyLedger(c).ok);
});

test('同一节点的并发读去重为一次重算', async () => {
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 100_000 });
  c.defineSource('x', 3);
  const gate = barrier();
  c.defineDerived('slow', ['x'], async ([x]) => {
    await gate.p;
    return (x as number) + 1;
  });

  const reads = [c.read('slow'), c.read('slow'), c.read('slow')];
  gate.release();
  assert.deepEqual(await Promise.all(reads), [4, 4, 4]);
  assert.equal(c.stats.computes, 1);
});

test('静默性检查能发现未完成的重算，drain 后资源全部释放', async () => {
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 100_000 });
  c.defineSource('x', 1);
  const gate = barrier();
  c.defineDerived('d', ['x'], async ([x]) => {
    await gate.p;
    return x;
  });

  const p = c.read('d');
  await Promise.resolve();
  await Promise.resolve();
  assert.ok(c.scheduler.checkQuiescent().length > 0); // 在途重算被发现

  gate.release();
  await p;
  await c.scheduler.drain();
  assert.deepEqual(c.scheduler.checkQuiescent(), []);
  assert.equal(c.ledger.pendingReservedBytes, 0);
  assert.ok(verifyLedger(c).ok);
});
