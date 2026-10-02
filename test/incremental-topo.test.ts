import { test } from 'node:test';
import assert from 'node:assert/strict';
import { IncrementalTopoSort } from '../src/incremental-topo.ts';

function assertTopoValid<T>(g: IncrementalTopoSort<T>): void {
  assert.ok(g.validate(), '拓扑序与图结构不一致');
}

test('验收：初始链 1->2->3->4 拓扑序正确', () => {
  const g = new IncrementalTopoSort<number>();
  g.addEdge(1, 2);
  g.addEdge(2, 3);
  g.addEdge(3, 4);
  assert.deepEqual(g.order(), [1, 2, 3, 4]);
  assertTopoValid(g);
});

test('验收：插入 4->2 触发环检测，返回 [2,3,4,2] 且拓扑序不变', () => {
  const g = new IncrementalTopoSort<number>();
  g.addEdge(1, 2);
  g.addEdge(2, 3);
  g.addEdge(3, 4);
  const before = g.order();

  const r = g.addEdge(4, 2);
  assert.equal(r.ok, false);
  assert.deepEqual((r as { cycle: number[] }).cycle, [2, 3, 4, 2]);

  // 拒绝后图与拓扑序完全未变
  assert.deepEqual(g.order(), before);
  assert.equal(g.hasEdge(4, 2), false);
  assertTopoValid(g);
});

test('验收：插入 1->4 走前向快路径，保持拓扑单调性', () => {
  const g = new IncrementalTopoSort<number>();
  g.addEdge(1, 2);
  g.addEdge(2, 3);
  g.addEdge(3, 4);
  const before = g.order();

  const r = g.addEdge(1, 4);
  assert.equal(r.ok, true);
  assert.deepEqual((r as { affected: number[] }).affected, []); // 快路径零重排
  assert.deepEqual(g.order(), before); // 序号无需变化
  assert.equal(g.hasEdge(1, 4), true);
  assertTopoValid(g);
});

test('后向边触发双向局部重染色，仅影响窗口内节点', () => {
  // 无边三节点，序号按插入顺序：a=0, b=1, c=2
  const g = new IncrementalTopoSort<string>();
  g.addNode('a');
  g.addNode('b');
  g.addNode('c');

  const r = g.addEdge('c', 'a'); // 后向边：ord(c)=2 > ord(a)=0，无环
  assert.equal(r.ok, true);
  const affected = (r as { affected: string[] }).affected;
  assert.deepEqual([...affected].sort(), ['a', 'c']); // 仅窗口内两节点被重染色
  assert.equal(g.rank('b'), 1); // 窗口内但不在可达集上的节点序号不变
  assert.equal(g.rank('c'), 0);
  assert.equal(g.rank('a'), 2);
  assert.deepEqual(g.order(), ['c', 'b', 'a']);
  assertTopoValid(g);
});

test('后向边重排保持拓扑单调性且窗口外节点不受影响', () => {
  const g = new IncrementalTopoSort<number>();
  g.addEdge(1, 2);
  g.addEdge(2, 3);
  g.addEdge(3, 4);
  g.addNode(5); // 链尾之后的独立节点
  const before = g.order();

  const r = g.addEdge(4, 2); // 成环，拒绝
  assert.equal(r.ok, false);
  assert.deepEqual(g.order(), before); // 拒绝后零改动

  const r2 = g.addEdge(5, 2); // 后向边，无环：5 被重排到 2 之前
  assert.equal(r2.ok, true);
  const affected = (r2 as { affected: number[] }).affected;
  assert.ok(affected.includes(5));
  assert.ok(!affected.includes(1)); // 1 在窗口 [ord(2), ord(5)] 之外
  assert.equal(g.rank(1), 0);
  assert.ok(g.rank(5)! < g.rank(2)!);
  assertTopoValid(g);
});

test('自环立即拒绝', () => {
  const g = new IncrementalTopoSort<number>();
  const r = g.addEdge(7, 7);
  assert.equal(r.ok, false);
  assert.deepEqual((r as { cycle: number[] }).cycle, [7, 7]);
  assert.equal(g.hasEdge(7, 7), false);
});

test('重复边幂等', () => {
  const g = new IncrementalTopoSort<number>();
  g.addEdge(1, 2);
  const r = g.addEdge(1, 2);
  assert.equal(r.ok, true);
  assert.deepEqual((r as { affected: number[] }).affected, []);
});

test('tryAddEdges 全有或全无：成环批次整体回滚', () => {
  const g = new IncrementalTopoSort<number>();
  g.addEdge(1, 2);
  g.addEdge(2, 3);
  const before = g.order();

  const r = g.tryAddEdges([
    [3, 4],
    [4, 5],
    [5, 2], // 成环 2->3->4->5->2
  ]);
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.equal(r.failedIndex, 2);
    assert.deepEqual(r.failedEdge, [5, 2]);
    assert.deepEqual(r.cycle, [2, 3, 4, 5, 2]);
  }
  // 整体回滚：3->4、4->5 也未生效
  assert.deepEqual(g.order(), before);
  assert.equal(g.hasEdge(3, 4), false);
  assert.equal(g.hasNode(5), false);
  assertTopoValid(g);
});

test('tryAddEdges 成功批次全部生效', () => {
  const g = new IncrementalTopoSort<number>();
  const r = g.tryAddEdges([
    [1, 2],
    [2, 3],
    [1, 3],
  ]);
  assert.equal(r.ok, true);
  assert.deepEqual(g.order(), [1, 2, 3]);
  assertTopoValid(g);
});

test('并发（Promise.all 交错）提交下结果确定且拓扑一致', async () => {
  const g = new IncrementalTopoSort<number>();
  const edges: Array<[number, number]> = [];
  // 生成一个分层 DAG 的并发插入序列
  for (let i = 0; i < 50; i++) {
    edges.push([i, i + 50]); // 跨层边
    if (i < 49) edges.push([i, i + 1]); // 链边
  }
  const results = await Promise.all(
    edges.map(([u, v]) => Promise.resolve().then(() => g.addEdge(u, v))),
  );
  assert.ok(results.every((r) => r.ok));
  assertTopoValid(g);
});

test('随机 DAG 压力测试：每次插入后拓扑序均有效，环均被拒绝', () => {
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;

  const g = new IncrementalTopoSort<number>();
  const N = 300;
  for (let i = 0; i < N; i++) g.addNode(i);

  // 随机插入 2000 条边（含必然成环的回边）
  for (let k = 0; k < 2000; k++) {
    const u = Math.floor(rand() * N);
    const v = Math.floor(rand() * N);
    const r = g.addEdge(u, v);
    if (!r.ok) {
      const cyc = (r as { cycle: number[] }).cycle;
      // 环路首尾相同，且每对相邻节点间存在真实边（最后一条为被拒绝的新边）
      assert.equal(cyc[0], cyc[cyc.length - 1]);
      for (let i = 0; i + 2 < cyc.length; i++) {
        assert.ok(g.hasEdge(cyc[i], cyc[i + 1]), `环路边 ${cyc[i]}->${cyc[i + 1]} 不存在`);
      }
    }
    assertTopoValid(g);
  }
});

test('环路路径因果性：报告的环是图中真实可达路径', () => {
  const g = new IncrementalTopoSort<string>();
  g.addEdge('compile', 'link');
  g.addEdge('link', 'load');
  g.addEdge('load', 'run');
  const r = g.addEdge('run', 'compile');
  assert.equal(r.ok, false);
  assert.deepEqual((r as { cycle: string[] }).cycle, ['compile', 'link', 'load', 'run', 'compile']);
});
