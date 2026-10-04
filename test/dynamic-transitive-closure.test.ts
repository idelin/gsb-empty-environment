import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DynamicTransitiveClosure, type Edge } from '../src/index.ts';

/** 校验见证路径本身合法：起点终点正确、每条边都在当前图中。 */
function assertWitnessValid<N>(tc: DynamicTransitiveClosure<N>, from: N, to: N, path: N[]): void {
  assert.equal(path[0], from, '见证路径起点应为 from');
  assert.equal(path[path.length - 1], to, '见证路径终点应为 to');
  assert.ok(path.length >= 2, '见证路径至少包含一条边');
  for (let i = 0; i + 1 < path.length; i++) {
    assert.ok(tc.hasEdge(path[i], path[i + 1]), `边 ${path[i]} -> ${path[i + 1]} 应存在`);
  }
}

/** 校验当前所有可达对的见证路径都合法，且与全量重算一致。 */
function assertConsistent<N>(tc: DynamicTransitiveClosure<N>): void {
  for (const { from, to, path } of tc.entries()) assertWitnessValid(tc, from, to, path);
  const v = tc.verify();
  assert.deepEqual(v.missing, [], '不应有丢失的可达对');
  assert.deepEqual(v.extra, [], '不应有多余的可达对');
  assert.ok(v.ok);
}

test('基本链式加边产生传递闭包差量和见证路径', () => {
  const tc = new DynamicTransitiveClosure<string>();
  const d1 = tc.addEdges([['a', 'b']]);
  assert.deepEqual(d1.added.map((e) => [e.from, e.to]), [['a', 'b']]);

  const d2 = tc.addEdges([['b', 'c']]);
  const pairs = d2.added.map((e) => [e.from, e.to]);
  assert.ok(pairs.some(([f, t]) => f === 'b' && t === 'c'));
  assert.ok(pairs.some(([f, t]) => f === 'a' && t === 'c'), '应新增传递可达 a->c');

  const w = tc.witnessPath('a', 'c');
  assert.deepEqual(w, ['a', 'b', 'c']);
  assert.ok(!tc.isReachable('c', 'a'));
  assertConsistent(tc);
});

test('删除一条边后，由其他路径支撑的可达关系必须保留', () => {
  const tc = new DynamicTransitiveClosure<number>();
  // 1->2->3 与直达边 1->3 并存
  tc.addEdges([[1, 2], [2, 3], [1, 3]]);
  assert.ok(tc.isReachable(1, 3));

  // 删除直达边 1->3：可达关系 (1,3) 必须保留，见证路径改走 1->2->3
  const d = tc.removeEdges([[1, 3]]);
  assert.deepEqual(d.removed, [], '不该删除仍被 1->2->3 支撑的可达对');
  assert.ok(tc.isReachable(1, 3));
  const w = tc.witnessPath(1, 3)!;
  assert.deepEqual(w, [1, 2, 3]);
  assertConsistent(tc);
});

test('删除唯一路径上的边才真正移除可达对', () => {
  const tc = new DynamicTransitiveClosure<number>();
  tc.addEdges([[1, 2], [2, 3], [3, 4]]);
  const d = tc.removeEdges([[2, 3]]);
  const removed = d.removed.map((e) => `${e.from}->${e.to}`).sort();
  assert.deepEqual(removed, ['1->3', '1->4', '2->3', '2->4']);
  assert.ok(tc.isReachable(1, 2));
  assert.ok(tc.isReachable(3, 4));
  assert.ok(!tc.isReachable(1, 4));
  assertConsistent(tc);
});

test('菱形结构：删一条并联边，所有可达关系保留', () => {
  const tc = new DynamicTransitiveClosure<string>();
  tc.addEdges([['s', 'a'], ['s', 'b'], ['a', 't'], ['b', 't']]);
  assert.ok(tc.isReachable('s', 't'));

  const d = tc.removeEdges([['s', 'a']]);
  assert.deepEqual(d.removed.map((e) => [e.from, e.to]), [['s', 'a']], '仅 (s,a) 本身消失');
  assert.ok(tc.isReachable('s', 't'), '(s,t) 由 s->b->t 支撑，必须保留');
  assert.deepEqual(tc.witnessPath('s', 't'), ['s', 'b', 't']);
  assertConsistent(tc);
});

test('环：删除环上一条边后环内可达性保持，断环后正确收缩', () => {
  const tc = new DynamicTransitiveClosure<number>();
  // 环 1->2->3->1，加尾巴 3->4
  tc.addEdges([[1, 2], [2, 3], [3, 1], [3, 4]]);
  assert.ok(tc.isReachable(1, 1), '环上节点应可达自身');
  assert.ok(tc.isReachable(2, 4));

  // 删除 2->3：改走 2->... 无替代，但 1->2->? 检查：1->2 仍在，3->1、3->4 仍在
  let d = tc.removeEdges([[2, 3]]);
  // 1 仍可经 1->2? 不，1->2 到 2 后无出边。1->3 需 1->2->3 已断。1 不可再到 3/4/自身。
  assert.ok(!tc.isReachable(1, 3));
  assert.ok(!tc.isReachable(1, 1));
  assert.ok(tc.isReachable(3, 1), '3->1 边仍在');
  assert.ok(tc.isReachable(3, 2), '3->1->2 仍在');
  assert.ok(tc.isReachable(3, 4));
  assertConsistent(tc);

  // 加回 2->3，环恢复
  d = tc.addEdges([[2, 3]]);
  assert.ok(tc.isReachable(1, 1));
  assert.ok(tc.isReachable(2, 4));
  assertConsistent(tc);
});

test('自环与重复边：幂等且不出错', () => {
  const tc = new DynamicTransitiveClosure<number>();
  const d1 = tc.addEdges([[1, 1]]);
  assert.equal(d1.added.length, 1, '自环产生 (1,1)');
  assert.ok(tc.isReachable(1, 1));

  // 重复加边：空操作
  const d2 = tc.addEdges([[1, 1], [1, 1]]);
  assert.equal(d2.added.length, 0);
  assert.equal(tc.edgeCount, 1);

  // 重复删边：第二次为空操作
  const d3 = tc.removeEdges([[1, 1]]);
  assert.equal(d3.removed.length, 1);
  const d4 = tc.removeEdges([[1, 1]]);
  assert.equal(d4.removed.length, 0);
  assert.equal(tc.edgeCount, 0);
  assertConsistent(tc);
});

test('重复边不影响删除语义（图按简单有向图处理）', () => {
  const tc = new DynamicTransitiveClosure<number>();
  tc.addEdges([[1, 2], [1, 2], [1, 2], [2, 3]]);
  assert.equal(tc.edgeCount, 2);
  const d = tc.removeEdges([[1, 2]]);
  assert.ok(!tc.isReachable(1, 2));
  assert.ok(!tc.isReachable(1, 3));
  assert.ok(tc.isReachable(2, 3));
  assertConsistent(tc);
});

test('批量 update：先删后加，差量合并输出', () => {
  const tc = new DynamicTransitiveClosure<number>();
  tc.addEdges([[1, 2], [2, 5], [1, 3], [3, 4], [4, 5]]);
  // 同一批：删 1->2，加 2->4
  const d = tc.update({ remove: [[1, 2]], add: [[2, 4]] });
  assert.ok(d.removed.some((e) => e.from === 1 && e.to === 2));
  assert.ok(d.added.some((e) => e.from === 2 && e.to === 4));
  // 1->5 仍由 1->3->4->5 支撑
  assert.ok(tc.isReachable(1, 5));
  assertConsistent(tc);
});

test('删除不可达节点相关的边为空操作', () => {
  const tc = new DynamicTransitiveClosure<string>();
  tc.addEdges([['x', 'y']]);
  const d = tc.removeEdges([['p', 'q'], ['x', 'z']]);
  assert.deepEqual(d, { added: [], removed: [] });
  assertConsistent(tc);
});

// ---------------------------------------------------------------- 随机化对照

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

test('随机化对照：批量增删（含环与重复边）后始终与全量搜索一致', () => {
  const rand = mulberry32(20261005);
  const N = 12;
  const tc = new DynamicTransitiveClosure<number>();

  for (let iter = 0; iter < 300; iter++) {
    const adds: Edge<number>[] = [];
    const removes: Edge<number>[] = [];
    const addCount = Math.floor(rand() * 5);
    const removeCount = Math.floor(rand() * 5);

    for (let i = 0; i < addCount; i++) {
      const u = Math.floor(rand() * N);
      const v = Math.floor(rand() * N);
      adds.push([u, v]); // 可能含自环、重复边
      if (rand() < 0.2) adds.push([u, v]); // 故意制造重复边
    }
    const current = tc.edges();
    for (let i = 0; i < removeCount; i++) {
      if (current.length > 0 && rand() < 0.8) {
        removes.push(current[Math.floor(rand() * current.length)]);
      } else {
        removes.push([Math.floor(rand() * N), Math.floor(rand() * N)]); // 可能不存在
      }
    }

    tc.update({ add: adds, remove: removes });
    assertConsistent(tc);
  }
});

test('随机化对照：删边后保留的可达对见证路径均不经过被删边', () => {
  const rand = mulberry32(42);
  const N = 8;
  const tc = new DynamicTransitiveClosure<number>();
  const all: Edge<number>[] = [];
  for (let u = 0; u < N; u++) for (let v = 0; v < N; v++) if (u !== v) all.push([u, v]);
  // 随机稠密图
  tc.addEdges(all.filter(() => rand() < 0.35));

  for (let iter = 0; iter < 100; iter++) {
    const es = tc.edges();
    if (es.length === 0) break;
    const victim = es[Math.floor(rand() * es.length)];
    tc.removeEdges([victim]);
    // 所有保留的可达对，其见证路径不得包含被删边
    for (const { path } of tc.entries()) {
      for (let i = 0; i + 1 < path.length; i++) {
        assert.notDeepEqual([path[i], path[i + 1]], victim, '见证路径不得经过已删除的边');
      }
    }
    assertConsistent(tc);
  }
});
