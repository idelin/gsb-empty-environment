import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Replica,
  covers,
  merge,
  compare,
  applyEvent,
  reconcile,
  emptyState,
  versionGraphSize,
  type DottedEvent,
} from '../src/index.ts';

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

test('版本向量基本运算：covers / merge / compare', () => {
  assert.equal(covers({ a: 2 }, { node: 'a', counter: 2 }), true);
  assert.equal(covers({ a: 1 }, { node: 'a', counter: 2 }), false);
  assert.equal(covers({}, { node: 'a', counter: 1 }), false);
  assert.deepEqual(merge({ a: 1, b: 2 }, { a: 3, c: 1 }), { a: 3, b: 2, c: 1 });
  assert.equal(compare({ a: 2 }, { a: 1 }), 'a-descends');
  assert.equal(compare({ a: 1 }, { a: 2 }), 'b-descends');
  assert.equal(compare({ a: 1 }, { a: 1 }), 'equal');
  assert.equal(compare({ a: 1 }, { b: 1 }), 'concurrent');
});

test('顺序覆盖写：活跃分支恒为 1，无幽灵分支', () => {
  const a = new Replica<number>('a');
  const b = new Replica<number>('b');
  b.receive(a.write('x', 1));
  a.receive(b.write('x', 2));
  b.receive(a.write('x', 3));
  for (const replica of [a, b]) {
    assert.deepEqual(replica.read('x').values, [3]);
    assert.equal(replica.read('x').conflict, false);
    // 1 个活跃点 + 前置向量 {a:1, b:1} => 体积恒为 3，与写入次数无关
    assert.equal(replica.graphSize('x'), 3);
  }
});

test('高频空更新：同值重写 100 次仍只有 1 个分支', () => {
  const a = new Replica<string>('a');
  for (let i = 0; i < 100; i++) a.write('hot', 'same');
  const state = a.read('hot');
  assert.deepEqual(state.values, ['same']);
  assert.deepEqual(state.dots, [{ node: 'a', counter: 100 }]);
  assert.deepEqual(state.context, { a: 99 }); // 历史被压缩为单个计数器
  assert.equal(a.graphSize('hot'), 2); // 1 活跃点 + 1 条前置向量，与写入次数无关
});

test('相同值的并发写入被去重为单分支', () => {
  const a = new Replica<string>('a');
  const b = new Replica<string>('b');
  const ea = a.write('k', 'v');
  const eb = b.write('k', 'v');
  a.receive(eb);
  b.receive(ea);
  for (const replica of [a, b]) {
    assert.deepEqual(replica.read('k').values, ['v']);
    assert.equal(replica.read('k').conflict, false);
    assert.equal(replica.state('k').values.length, 1);
  }
  assert.deepEqual(a.state('k'), b.state('k'));
});

test('不同值的并发覆盖保留为真实兄弟分支，合并写后坍缩', () => {
  const a = new Replica<string>('a');
  const b = new Replica<string>('b');
  const ea = a.write('k', 'from-a');
  const eb = b.write('k', 'from-b');
  a.receive(eb);
  b.receive(ea);
  for (const replica of [a, b]) {
    assert.deepEqual([...replica.read('k').values].sort(), ['from-a', 'from-b']);
    assert.equal(replica.read('k').conflict, true);
  }
  // 应用层解决冲突后写回：兄弟坍缩为单分支
  const resolve = a.write('k', 'merged');
  b.receive(resolve);
  assert.deepEqual(b.read('k').values, ['merged']);
  assert.equal(b.state('k').values.length, 1);
});

test('乱序 + 重复广播下状态收敛且幂等', () => {
  const a = new Replica<number>('a');
  const b = new Replica<number>('b');
  const c = new Replica<number>('c');
  const e1 = a.write('x', 1);
  b.receive(e1);
  const e2 = b.write('x', 2);
  a.receive(e2);
  const e3 = a.write('x', 3);
  // c 逆序收到事件，且重复投递
  for (const event of [e3, e2, e1, e3, e1, e2]) c.receive(event);
  b.receive(e3);
  for (const replica of [a, b, c]) {
    assert.deepEqual(replica.read('x').values, [3]);
    assert.equal(replica.graphSize('x'), 3);
  }
  assert.deepEqual(a.state('x'), c.state('x'));
});

test('同一事件重复投递幂等', () => {
  const a = new Replica<string>('a');
  const b = new Replica<string>('b');
  const event = a.write('k', 'v');
  b.receive(event);
  const before = b.state('k');
  b.receive(event);
  b.receive(event);
  assert.deepEqual(b.state('k'), before);
});

test('同一计数器被复用为不同值时抛出不变式错误', () => {
  const forged: DottedEvent<string> = {
    kind: 'dvv-event',
    key: 'k',
    dot: { node: 'a', counter: 1 },
    context: {},
    value: 'one',
  };
  const conflicting = { ...forged, value: 'two' };
  let state = emptyState<string>();
  state = applyEvent(state, forged);
  assert.throws(() => applyEvent(state, conflicting), /reused/);
  assert.throws(
    () => reconcile(state, applyEvent(emptyState<string>(), conflicting)),
    /reused/,
  );
});

test('反熵 reconcile 与事件广播殊途同归', () => {
  const rand = mulberry32(7);
  const viaEvents = new Replica<number>('p');
  const viaSync = new Replica<number>('q');
  const writers = [new Replica<number>('a'), new Replica<number>('b')];
  const events: Array<DottedEvent<number>> = [];
  for (let i = 0; i < 50; i++) {
    const writer = writers[Math.floor(rand() * writers.length)];
    const event = writer.write('k', Math.floor(rand() * 3));
    events.push(event);
    // 写者之间偶尔互通，制造因果交织
    if (rand() < 0.5) writers[1 - writers.indexOf(writer)].receive(event);
  }
  const shuffled = [...events].sort(() => rand() - 0.5);
  for (const event of shuffled) viaEvents.receive(event);
  for (const writer of writers) viaSync.sync('k', writer);
  assert.deepEqual(viaEvents.state('k'), viaSync.state('k'));
});

test('验收场景：高频空更新+覆盖更新，多轮乱序广播后收敛且版本图极简', () => {
  const rand = mulberry32(42);
  const nodes = ['n1', 'n2', 'n3'].map((id) => new Replica<number>(id));
  const mailbox: Array<DottedEvent<number>> = [];
  const VALUES = [0, 1, 2, 3];
  let current = 0;
  for (let round = 0; round < 400; round++) {
    const writer = nodes[Math.floor(rand() * nodes.length)];
    // 70% 空更新（重写当前值），30% 覆盖更新
    const value = rand() < 0.7 ? current : (current = VALUES[Math.floor(rand() * VALUES.length)]);
    const event = writer.write('hot-key', value);
    // 广播：随机投递给部分副本，且可能重复
    for (const replica of nodes) {
      if (replica === writer) continue;
      if (rand() < 0.8) mailbox.push(event);
      if (rand() < 0.2) mailbox.push(event);
    }
    // 每 40 轮做一次乱序投递
    if (round % 40 === 39) {
      const batch = mailbox.splice(0).sort(() => rand() - 0.5);
      for (const event of batch) for (const replica of nodes) replica.receive(event);
    }
  }
  const rest = mailbox.splice(0).sort(() => rand() - 0.5);
  for (const event of rest) for (const replica of nodes) replica.receive(event);
  // 收敛：三个副本状态逐字节一致
  assert.deepEqual(nodes[0].state('hot-key'), nodes[1].state('hot-key'));
  assert.deepEqual(nodes[1].state('hot-key'), nodes[2].state('hot-key'));
  // 极简：活跃分支 1 个，版本图体积 <= 1 + 节点数
  const state = nodes[0].state('hot-key');
  assert.equal(state.values.length, 1);
  assert.ok(versionGraphSize(state) <= 1 + nodes.length);
  assert.deepEqual(nodes[0].read('hot-key').values, [current]);
});
