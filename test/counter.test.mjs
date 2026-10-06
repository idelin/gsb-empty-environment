import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import {
  CounterReplica,
  applyOp,
  canonicalState,
  deserializeState,
  emptyState,
  mergeStates,
  serializeState,
  valueOf,
} from '../dist/counter.mjs';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

/** 生成全排列（Heap 算法） */
function* permutations(items) {
  const a = items.slice();
  yield a.slice();
  const c = new Array(a.length).fill(0);
  let i = 0;
  while (i < a.length) {
    if (c[i] < i) {
      const j = i % 2 === 0 ? 0 : c[i];
      [a[i], a[j]] = [a[j], a[i]];
      yield a.slice();
      c[i] += 1;
      i = 0;
    } else {
      c[i] = 0;
      i += 1;
    }
  }
}

/** 确定性伪随机（LCG），用于可复现的洗牌 */
function makeRng(seed) {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

function shuffled(items, rng) {
  const a = items.slice();
  for (let i = a.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rng() * (i + 1));
    [a[i], a[j]] = [a[j], a[i]];
  }
  return a;
}

function applyAll(ops) {
  let state = emptyState();
  for (const op of ops) state = applyOp(state, op);
  return state;
}

test('基本：单副本递增求值', () => {
  const r = new CounterReplica('A');
  r.increment();
  r.increment();
  r.increment(5);
  assert.equal(r.value(), 7);
});

test('基本：reset 清空已观察贡献，值归零', () => {
  const r = new CounterReplica('A');
  r.increment();
  r.increment(3);
  r.reset();
  assert.equal(r.value(), 0);
});

test('操作不携带任何时间戳/时钟字段', () => {
  const r = new CounterReplica('A');
  const inc = r.increment();
  const reset = r.reset();
  for (const op of [inc, reset]) {
    for (const key of Object.keys(op)) {
      assert.ok(
        !/time|clock|ts|date|stamp/i.test(key),
        `操作不应包含时间相关字段，发现: ${key}`,
      );
    }
  }
  assert.deepEqual(Object.keys(inc).sort(), ['amount', 'id', 'kind']);
  assert.deepEqual(Object.keys(reset).sort(), ['id', 'kind', 'observed']);
});

test('库源码不依赖本地时钟', () => {
  const src = readFileSync(join(root, 'src', 'counter.ts'), 'utf8');
  assert.ok(!/Date\s*\(|Date\.now|performance\.now|hrtime/.test(src));
});

test('消息排列：所有操作按任意顺序到达，结果一致', () => {
  // 构造场景：A、B、C 三个副本，含并发 reset 与并发递增
  const a = new CounterReplica('A');
  const b = new CounterReplica('B');
  const c = new CounterReplica('C');

  const ops = [];
  ops.push(a.increment());      // A:1
  ops.push(a.increment(2));     // A:2
  ops.push(b.increment());      // B:1
  // A 观察到 A:1 A:2 B:1 后 reset
  a.receive(ops[2]);
  ops.push(a.reset());          // A:3 观察 {A:1, A:2, B:1}
  // 与 reset 并发的递增（未被 A 观察）
  ops.push(b.increment(4));     // B:2
  ops.push(c.increment());      // C:1
  // B 观察到 B:1 B:2 后 reset（与 A 的 reset 并发，观察集重叠于 B:1）
  ops.push(b.reset());          // B:3 观察 {B:1, B:2}
  ops.push(c.increment(3));     // C:2

  // 期望值：A:1、A:2、B:1 被 A 的 reset 清除；B:1、B:2 被 B 的 reset 清除；
  // 幸存：C:1(1) + C:2(3) = 4
  const expectedValue = 4;
  const expected = canonicalState(applyAll(ops));

  // 8 个操作 = 40320 种排列，全部验证
  let checked = 0;
  for (const perm of permutations(ops)) {
    const state = applyAll(perm);
    assert.equal(valueOf(state), expectedValue);
    assert.deepEqual(canonicalState(state), expected);
    checked += 1;
  }
  assert.equal(checked, 40320);
});

test('消息排列：副本逐条 receive 的随机顺序同样收敛', () => {
  const rng = makeRng(42);
  const a = new CounterReplica('A');
  const b = new CounterReplica('B');
  const ops = [];
  ops.push(a.increment());
  ops.push(b.increment(2));
  a.receive(ops[1]);
  ops.push(a.reset());
  ops.push(b.increment(5));
  ops.push(a.increment(7));

  const expected = canonicalState(applyAll(ops));
  for (let trial = 0; trial < 200; trial += 1) {
    const replica = new CounterReplica('R');
    for (const op of shuffled(ops, rng)) replica.receive(op);
    assert.deepEqual(canonicalState(replica.snapshot()), expected);
  }
});

test('重复合并：同一操作重复投递是空操作', () => {
  const r = new CounterReplica('R');
  const a = new CounterReplica('A');
  const inc = a.increment(3);
  const reset = a.reset();

  assert.equal(r.receive(inc), true);
  assert.equal(r.receive(inc), false);
  assert.equal(r.receive(inc), false);
  assert.equal(r.value(), 3);

  assert.equal(r.receive(reset), true);
  assert.equal(r.receive(reset), false);
  assert.equal(r.value(), 0);
});

test('重复合并：状态合并幂等，且与自身合并不变', () => {
  const a = new CounterReplica('A');
  a.increment(2);
  a.increment(3);
  a.reset();
  a.increment(1);

  const s = a.snapshot();
  assert.deepEqual(canonicalState(mergeStates(s, s)), canonicalState(s));

  const b = new CounterReplica('B');
  b.merge(s);
  const once = canonicalState(b.snapshot());
  b.merge(s);
  b.merge(a.snapshot());
  assert.deepEqual(canonicalState(b.snapshot()), once);
  assert.equal(b.value(), 1);
});

test('重复合并：序列化/反序列化往返保持状态', () => {
  const a = new CounterReplica('A');
  a.increment(2);
  a.reset();
  a.increment(5);
  const round = deserializeState(JSON.parse(JSON.stringify(serializeState(a.snapshot()))));
  assert.deepEqual(canonicalState(round), canonicalState(a.snapshot()));
  assert.equal(valueOf(round), 5);
});

test('重置重叠：并发 reset 的观察集有交集，清除并集', () => {
  // 共同历史：A:1, B:1
  const a = new CounterReplica('A');
  const b = new CounterReplica('B');
  const incA = a.increment();     // A:1
  const incB = b.increment();     // B:1
  a.receive(incB);
  b.receive(incA);

  // 各自再产生本地递增，然后并发 reset（观察集重叠于 {A:1, B:1}）
  const incA2 = a.increment(10);  // A:2，B 未观察
  const incB2 = b.increment(20);  // B:2，A 未观察
  const resetA = a.reset();       // 观察 {A:1, B:1, A:2}
  const resetB = b.reset();       // 观察 {A:1, B:1, B:2}

  // 合并所有操作（乱序 + 重复投递）
  const rng = makeRng(7);
  const ops = [incA, incB, incA2, incB2, resetA, resetB];
  const merged = new CounterReplica('M');
  for (const op of shuffled(ops, rng)) merged.receive(op);
  for (const op of shuffled(ops, rng)) merged.receive(op); // 重复投递一轮

  // 并集清除 {A:1, B:1, A:2, B:2}，值为 0
  assert.equal(merged.value(), 0);
});

test('重置重叠：未被任何 reset 观察的并发递增保留', () => {
  const a = new CounterReplica('A');
  const b = new CounterReplica('B');
  const incA = a.increment(3);    // A:1
  b.receive(incA);
  const resetB = b.reset();       // B 观察 {A:1}
  const incA2 = a.increment(4);   // A:2，与 B 的 reset 并发，未被观察

  const merged = new CounterReplica('M');
  merged.receive(resetB);         // reset 先到
  merged.receive(incA2);
  merged.receive(incA);
  assert.equal(merged.value(), 4);
});

test('重置语义：reset 先于被观察递增到达，递增到达后仍被清除', () => {
  const a = new CounterReplica('A');
  const inc = a.increment(9);     // A:1
  const reset = a.reset();        // 观察 {A:1}

  // 另一副本先收到 reset（此时它还没见过 A:1），后收到递增
  const r = new CounterReplica('R');
  r.receive(reset);
  assert.equal(r.value(), 0);
  r.receive(inc);
  assert.equal(r.value(), 0);     // 集合语义：A:1 属于 reset 的观察集，仍被清除
});

test('合并满足交换律与结合律（随机状态）', () => {
  const rng = makeRng(1234);
  // 构造三个有共同历史、各自分叉的副本状态
  const base = new CounterReplica('base');
  const shared = [base.increment(), base.increment(2)];
  const replicas = ['A', 'B', 'C'].map((id) => new CounterReplica(id));
  for (const r of replicas) for (const op of shared) r.receive(op);

  const states = [];
  for (const r of replicas) {
    r.increment(Math.floor(rng() * 10));
    if (rng() < 0.7) r.reset();
    r.increment(Math.floor(rng() * 10));
    states.push(r.snapshot());
  }

  const full = states.reduce(mergeStates, emptyState());
  const expected = canonicalState(full);

  // 任意顺序两两合并都收敛到同一状态
  for (let trial = 0; trial < 100; trial += 1) {
    const order = shuffled(states, rng);
    const merged = order.reduce(mergeStates, emptyState());
    assert.deepEqual(canonicalState(merged), expected);
    // 交换律抽查
    assert.deepEqual(
      canonicalState(mergeStates(order[0], order[1])),
      canonicalState(mergeStates(order[1], order[0])),
    );
  }
});

test('端到端：三副本随机操作 + 随机消息传递后收敛', () => {
  const rng = makeRng(99);
  const replicas = [new CounterReplica('A'), new CounterReplica('B'), new CounterReplica('C')];
  const messages = [];

  for (let round = 0; round < 12; round += 1) {
    const r = replicas[Math.floor(rng() * replicas.length)];
    messages.push(rng() < 0.25 ? r.reset() : r.increment(1 + Math.floor(rng() * 5)));
    // 随机投递部分历史消息（含重复）
    for (const op of messages) {
      if (rng() < 0.3) {
        replicas[Math.floor(rng() * replicas.length)].receive(op);
      }
    }
  }
  // 最终全量交换所有消息
  for (const op of messages) for (const r of replicas) r.receive(op);

  const expected = canonicalState(applyAll(messages));
  for (const r of replicas) {
    assert.deepEqual(canonicalState(r.snapshot()), expected);
  }
});

let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failed += 1;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}
console.log(`\n${tests.length - failed}/${tests.length} passed`);
process.exit(failed === 0 ? 0 : 1);
