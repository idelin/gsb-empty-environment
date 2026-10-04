import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Scheduler } from '../src/scheduler.ts';
import type { CategoryConfig } from '../src/scheduler.ts';
import { ReferenceScheduler } from '../src/reference.ts';

const bigQueue = 1_000_000;

function run(sched: Scheduler, budgets: number[]) {
  for (const b of budgets) sched.step(b);
}

test('大包跨 tick 发送，完成只上报一次', () => {
  const sched = new Scheduler({
    bulk: { tokenRate: 1000, tokenCap: 1000, maxQueueBytes: bigQueue, weight: 1000 },
  });
  sched.submit({ id: 'big', arrivalTick: 0, bytes: 350, category: 'bulk' });
  run(sched, [100, 100, 100, 100, 100]);

  assert.equal(sched.completions.length, 1);
  assert.deepEqual(sched.completions[0], { tick: 3, id: 'big', category: 'bulk', bytes: 350 });
  assert.deepEqual(sched.ledger.map((l) => l.sentTotal), [100, 100, 100, 50, 0]);
  for (const l of sched.ledger) assert.ok(l.sentTotal <= l.budget);
});

test('大小包混合：小包不被别类大包阻塞，结果与逐字节参考一致', () => {
  const configs = {
    voice: { tokenRate: 100, tokenCap: 100, maxQueueBytes: bigQueue, weight: 10 },
    bulk: { tokenRate: 1000, tokenCap: 1000, maxQueueBytes: bigQueue, weight: 1000 },
  };
  const sched = new Scheduler(configs);
  const ref = new ReferenceScheduler(configs);
  const packets = [
    { id: 'B1', arrivalTick: 0, bytes: 1000, category: 'bulk' },
    { id: 'V1', arrivalTick: 0, bytes: 20, category: 'voice' },
    { id: 'V2', arrivalTick: 1, bytes: 20, category: 'voice' },
    { id: 'V3', arrivalTick: 2, bytes: 20, category: 'voice' },
  ];
  for (const p of packets) { sched.submit(p); ref.submit(p); }
  const budgets = Array.from({ length: 15 }, () => 100);
  run(sched, budgets);
  for (const b of budgets) ref.step(b);

  // voice 小包 20B：weight=10 每 tick 累积 10 字节亏欠，两个 tick 发完一个
  const voiceDone = sched.completions.filter((c) => c.category === 'voice');
  assert.equal(voiceDone.length, 3);
  assert.ok(voiceDone.every((c) => c.tick <= 6), 'voice packets must not wait for the 1000B bulk packet');
  assert.equal(sched.completions.filter((c) => c.id === 'B1').length, 1);
  assert.deepEqual(sched.completions, ref.completions);
  assert.deepEqual(sched.ledger, ref.ledger);
});

test('令牌不足时按 tokenRate 限速', () => {
  const sched = new Scheduler({
    c: { tokenRate: 5, tokenCap: 10, maxQueueBytes: bigQueue, weight: 1000 },
  });
  sched.submit({ id: 'p', arrivalTick: 0, bytes: 30, category: 'c' });
  run(sched, [1000, 1000, 1000, 1000, 1000]);

  // t0 桶满 10 字节，之后每 tick 仅补充 5
  assert.deepEqual(sched.ledger.map((l) => l.sentTotal), [10, 5, 5, 5, 5]);
  assert.deepEqual(sched.completions[0], { tick: 4, id: 'p', category: 'c', bytes: 30 });
});

test('整包入队超限即丢；缩容不丢既有包只拒新包', () => {
  const sched = new Scheduler({
    q: { tokenRate: 10, tokenCap: 10, maxQueueBytes: 100, weight: 100 },
  });
  sched.submit({ id: 'a', arrivalTick: 0, bytes: 60, category: 'q' });
  sched.submit({ id: 'b', arrivalTick: 0, bytes: 40, category: 'q' });
  sched.submit({ id: 'c', arrivalTick: 0, bytes: 1, category: 'q' }); // 100+1 超限
  sched.reconfigure(2, { q: { tokenRate: 10, tokenCap: 10, maxQueueBytes: 50, weight: 100 } });
  sched.submit({ id: 'd', arrivalTick: 2, bytes: 1, category: 'q' }); // 缩容后仍超
  sched.submit({ id: 'e', arrivalTick: 11, bytes: 1, category: 'q' }); // 排到 45 后可入
  run(sched, Array.from({ length: 25 }, () => 5));

  assert.deepEqual(
    sched.drops,
    [
      { tick: 0, id: 'c', reason: 'queue-limit' },
      { tick: 2, id: 'd', reason: 'queue-limit' },
    ],
  );
  // 既有包 a、b 不因缩容被丢弃；新包 e 在队列降到上限内后准入
  assert.deepEqual(
    sched.completions.map((c) => [c.id, c.tick]),
    [['a', 11], ['b', 19], ['e', 20]],
  );
});

test('重复包 ID：内容一致幂等，内容不同冲突，未知类别丢弃', () => {
  const sched = new Scheduler({
    voice: { tokenRate: 100, tokenCap: 100, maxQueueBytes: bigQueue, weight: 10 },
  });
  sched.submit({ id: 'p1', arrivalTick: 0, bytes: 10, category: 'voice' });
  sched.submit({ id: 'p1', arrivalTick: 1, bytes: 10, category: 'voice' }); // 幂等
  sched.submit({ id: 'p1', arrivalTick: 2, bytes: 11, category: 'voice' }); // 冲突
  sched.submit({ id: 'p2', arrivalTick: 2, bytes: 5, category: 'ghost' }); // 未知类别
  run(sched, [100, 100, 100]);

  assert.deepEqual(sched.duplicates, [{ tick: 1, id: 'p1' }]);
  assert.deepEqual(sched.drops, [
    { tick: 2, id: 'p1', reason: 'conflict' },
    { tick: 2, id: 'p2', reason: 'unknown-category' },
  ]);
  assert.equal(sched.completions.length, 1);
  assert.equal(sched.completions[0].id, 'p1');
  assert.equal(sched.ledger.reduce((s, l) => s + l.sentTotal, 0), 10); // 只发了一份
});

test('长期负载：发送份额精确等于 DRR 权重比，且每 tick 不超链路额度', () => {
  const configs = {
    voice: { tokenRate: 4, tokenCap: 8, maxQueueBytes: bigQueue, weight: 4 },
    inter: { tokenRate: 2, tokenCap: 4, maxQueueBytes: bigQueue, weight: 2 },
    bulk: { tokenRate: 1, tokenCap: 2, maxQueueBytes: bigQueue, weight: 1 },
  };
  const sched = new Scheduler(configs);
  const ticks = 2000;
  for (const cat of Object.keys(configs)) {
    sched.submit({ id: `backlog-${cat}`, arrivalTick: 0, bytes: 100_000, category: cat });
  }
  run(sched, Array.from({ length: ticks }, () => 10));

  const totals: Record<string, number> = { voice: 0, inter: 0, bulk: 0 };
  for (const l of sched.ledger) {
    assert.ok(l.sentTotal <= l.budget, `tick ${l.tick}: sent ${l.sentTotal} > budget ${l.budget}`);
    assert.equal(l.sentTotal, 7, '需求充足时工作守恒：每 tick 发满权重和');
    for (const [k, v] of Object.entries(l.sentByCategory)) totals[k] += v;
  }
  assert.deepEqual(totals, { voice: 4 * ticks, inter: 2 * ticks, bulk: 1 * ticks });
});

test('无饿死：持续积压队列在有界 tick 内必然推进', () => {
  // n=2, B=1：由 README 定理，bulk 首字节不超过 floor((n-1)/B)+1 = 2 个 tick，
  // 且两次 bulk 发送之间至多 1 次 voice 发送。
  const sched = new Scheduler({
    voice: { tokenRate: 100, tokenCap: 100, maxQueueBytes: bigQueue, weight: 100 },
    bulk: { tokenRate: 1, tokenCap: 1, maxQueueBytes: bigQueue, weight: 1 },
  });
  sched.submit({ id: 'v', arrivalTick: 0, bytes: 10_000, category: 'voice' });
  sched.submit({ id: 'b', arrivalTick: 0, bytes: 10_000, category: 'bulk' });
  const ticks = 100;
  run(sched, Array.from({ length: ticks }, () => 1));

  const bulkSent = sched.ledger.reduce((s, l) => s + (l.sentByCategory.bulk ?? 0), 0);
  assert.ok(bulkSent >= ticks / 2, `bulk starved: only ${bulkSent} bytes in ${ticks} ticks`);
  for (const l of sched.ledger) assert.ok(l.sentTotal <= 1);
});

test('重配置在指定 tick 开始原子生效；令牌按新桶容截断', () => {
  const sched = new Scheduler({
    c: { tokenRate: 100, tokenCap: 100, maxQueueBytes: bigQueue, weight: 1 },
  });
  sched.submit({ id: 'p', arrivalTick: 0, bytes: 10_000, category: 'c' });
  sched.reconfigure(3, { c: { tokenRate: 0, tokenCap: 5, maxQueueBytes: bigQueue, weight: 100 } });
  run(sched, [100, 100, 100, 100, 100]);

  // t0..t2：weight=1，每 tick 仅 1 字节亏欠可发
  assert.deepEqual(sched.ledger.slice(0, 3).map((l) => l.sentTotal), [1, 1, 1]);
  // t3：重配置生效，tokens 截断到 5，weight=100 → 受令牌限制发 5
  assert.equal(sched.ledger[3].sentTotal, 5);
  assert.equal(sched.ledger[3].tokensByCategory.c, 0);
  // t4 起 tokenRate=0，无令牌可发
  assert.equal(sched.ledger[4].sentTotal, 0);
});

function mulberry32(seed: number) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('随机化交叉核对：主实现与逐字节参考完全一致（含重配置/重复/冲突）', () => {
  for (let seed = 1; seed <= 150; seed++) {
    const rnd = mulberry32(seed);
    const catNames = ['voice', 'inter', 'bulk'].slice(0, 2 + Math.floor(rnd() * 2));
    const configs: Record<string, CategoryConfig> = {};
    for (const name of catNames) {
      configs[name] = {
        tokenRate: Math.floor(rnd() * 21),
        tokenCap: 1 + Math.floor(rnd() * 40),
        maxQueueBytes: Math.floor(rnd() * 121),
        weight: 1 + Math.floor(rnd() * 15),
      };
    }
    const sched = new Scheduler(configs);
    const ref = new ReferenceScheduler(configs);

    const idPool = Array.from({ length: 12 }, (_, i) => `pkt-${i}`);
    for (let i = 0; i < 30; i++) {
      const p = {
        id: idPool[Math.floor(rnd() * idPool.length)],
        arrivalTick: Math.floor(rnd() * 40),
        bytes: 1 + Math.floor(rnd() * 80),
        category: rnd() < 0.05 ? 'ghost' : catNames[Math.floor(rnd() * catNames.length)],
      };
      sched.submit(p);
      ref.submit(p);
    }
    for (let i = 0; i < 3; i++) {
      const at = Math.floor(rnd() * 50);
      const name = catNames[Math.floor(rnd() * catNames.length)];
      const cfg = {
        [name]: {
          tokenRate: Math.floor(rnd() * 21),
          tokenCap: 1 + Math.floor(rnd() * 40),
          maxQueueBytes: Math.floor(rnd() * 121),
          weight: 1 + Math.floor(rnd() * 15),
        },
      };
      sched.reconfigure(at, cfg);
      ref.reconfigure(at, cfg);
    }
    const budgets = Array.from({ length: 60 }, () => Math.floor(rnd() * 51));
    for (const b of budgets) { sched.step(b); ref.step(b); }

    assert.deepEqual(sched.completions, ref.completions, `seed ${seed} completions`);
    assert.deepEqual(sched.drops, ref.drops, `seed ${seed} drops`);
    assert.deepEqual(sched.duplicates, ref.duplicates, `seed ${seed} duplicates`);
    assert.deepEqual(sched.ledger, ref.ledger, `seed ${seed} ledger`);
    for (const l of sched.ledger) {
      assert.ok(l.sentTotal <= l.budget, `seed ${seed} tick ${l.tick} over budget`);
      const sum = Object.values(l.sentByCategory).reduce((a, b) => a + b, 0);
      assert.equal(sum, l.sentTotal, `seed ${seed} tick ${l.tick} ledger mismatch`);
    }
  }
});
