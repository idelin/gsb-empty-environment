import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReactiveCache, SubscriptionRejectedError, verifyLedger } from '../src/index.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('随机工作负载下账本始终与独立遍历一致', async () => {
  const rnd = mulberry32(20260924);
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 400 });
  c.defineSource('s0', 1);
  c.defineSource('s1', 2);
  c.defineSource('s2', [1, 2, 3]);
  c.defineDerived('d0', ['s0', 's1'], ([a, b]) => (a as number) + (b as number));
  c.defineDerived('d1', ['d0', 's2'], ([d, arr]) => ({ d, arr }));
  c.defineDerived('d2', ['s2'], ([arr]) => [...(arr as number[])].reverse());
  c.defineDerived('d3', ['d1', 'd2'], ([x, y]) => ({ x, y }));

  const derivedIds = ['d0', 'd1', 'd2', 'd3'];
  const subs: { unsubscribe(): void }[] = [];

  for (let step = 0; step < 300; step++) {
    const r = rnd();
    if (r < 0.35) {
      await c.read(derivedIds[Math.floor(rnd() * derivedIds.length)]);
    } else if (r < 0.6) {
      const cell = `s${Math.floor(rnd() * 3)}`;
      await c.transact((tx) => tx.set(cell, cell === 's2' ? [Math.floor(rnd() * 9)] : Math.floor(rnd() * 100)));
    } else if (r < 0.75) {
      c.flush();
    } else if (r < 0.9) {
      try {
        subs.push(await c.subscribe(derivedIds[Math.floor(rnd() * derivedIds.length)]));
      } catch (err) {
        assert.ok(err instanceof SubscriptionRejectedError); // 拒绝也是合法结果
      }
    } else if (subs.length > 0) {
      subs.pop()?.unsubscribe();
    }
    const report = verifyLedger(c);
    assert.ok(report.ok, `step ${step}: ${JSON.stringify(report.checks.filter((x) => !x.ok))}`);
    assert.deepEqual(c.scheduler.checkQuiescent(), []);
  }
  for (const s of subs) s.unsubscribe();
  assert.ok(verifyLedger(c).ok);
});

test('核验器能发现被篡改的引用计数（独立性证明）', async () => {
  const c = new ReactiveCache({ budget: 1_000_000, cacheBudget: 100_000 });
  c.defineSource('a', [1, 2]);
  c.defineDerived('d', ['a'], ([a]) => a);
  await c.read('d');
  assert.ok(verifyLedger(c).ok);

  // 篡改内部引用计数，独立遍历必须发现不一致
  const blockId = c.store.cellBlock('a');
  const internal = (c.blocks as unknown as { blocks: Map<number, { refs: number }> }).blocks;
  (internal.get(blockId) as { refs: number }).refs += 1;

  const report = verifyLedger(c);
  assert.equal(report.ok, false);
  assert.ok(report.checks.some((ch) => !ch.ok && ch.name.includes('引用计数')));
});
