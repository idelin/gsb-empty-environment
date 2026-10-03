import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WeightedGraph } from '../src/graph.ts';
import { BottleneckMonitor } from '../src/monitor.ts';
import { buildHybridNetwork } from '../src/topology.ts';

function buildTwoPathGraph(): WeightedGraph {
  const g = new WeightedGraph();
  g.addEdge('s', 'a', 1);
  g.addEdge('a', 't', 1);
  g.addEdge('s', 'b', 1);
  g.addEdge('b', 't', 1);
  return g;
}

test('Top-K 瓶颈节点按介数降序返回', () => {
  const { graph } = buildHybridNetwork();
  const mon = new BottleneckMonitor(graph, { topK: 3 });
  const top = mon.topKNodes();
  assert.equal(top.length, 3);
  assert.equal(top[0].node, 'GW');
  assert.ok(top[0].score >= top[1].score && top[1].score >= top[2].score);
  assert.deepEqual(top.map((e) => e.rank), [1, 2, 3]);
});

test('边权动态变动后 Top-K 随之更新', () => {
  const g = buildTwoPathGraph();
  const mon = new BottleneckMonitor(g, { topK: 2 });
  const before = mon.topKNodes();
  // 两条等权路径分流，a/b 各承担一半 s-t 流量。
  assert.deepEqual(before.map((e) => e.node).sort(), ['a', 'b']);
  assert.ok(Math.abs(before[0].score - 0.5) < 1e-9);

  // s-a 链路劣化后，s-t 与 s-a 流量全部改走 b-t 一侧，a 退出瓶颈榜。
  const after = mon.updateEdgeWeight('s', 'a', 5);
  assert.deepEqual(after.map((e) => e.node).sort(), ['b', 't']);
  assert.equal(after[0].score, 2);
  const full = mon.topKNodes(4);
  assert.equal(full.find((e) => e.node === 'a')!.score, 0);
});

test('Top-K 边瓶颈可输出', () => {
  const { graph } = buildHybridNetwork();
  const mon = new BottleneckMonitor(graph, { topK: 4 });
  const edges = mon.topKEdges();
  assert.equal(edges.length, 4);
  assert.ok(edges[0].edge.includes('GW'));
});

test('网关完全故障：报告指出首要分流替代节点且 GW 退出 Top-K', () => {
  const { graph, gateway } = buildHybridNetwork();
  const mon = new BottleneckMonitor(graph, { topK: 5 });
  const report = mon.simulateNodeFailure(gateway, { mode: 'remove' });

  assert.equal(report.failedNode, 'GW');
  assert.equal(report.before[0].node, 'GW');
  assert.ok(report.reroutedPairs > 0);
  assert.equal(report.disconnectedPairs, 0); // 备用链路保证连通
  assert.ok(!report.after.some((e) => e.node === 'GW'));
  assert.ok(report.primarySubstitute !== null);
  // 首要替代节点必须是骨干节点之一，且确实承接了最多改道流量。
  assert.ok(['A', 'B', 'C', 'D'].includes(report.primarySubstitute!));
  assert.equal(report.diversions[0].node, report.primarySubstitute);
  assert.ok(report.diversions[0].absorbedPaths > 0);
  assert.ok(report.summary.some((line) => line.includes('首要分流替代节点')));
  // 原图未被修改。
  assert.ok(graph.hasNode('GW'));
});

test('网关局部故障（链路劣化）：瓶颈转移并给出分流建议', () => {
  const { graph, gateway } = buildHybridNetwork();
  const mon = new BottleneckMonitor(graph, { topK: 5 });
  const report = mon.simulateNodeFailure(gateway, { mode: 'degrade', degradeFactor: 10 });

  assert.equal(report.mode, 'degrade');
  assert.ok(report.reroutedPairs > 0);
  assert.ok(report.primarySubstitute !== null);
  assert.ok(report.after[0].score <= report.before[0].score || report.after[0].node !== 'GW');
  assert.ok(report.summary.length >= 3);
});

test('近似模式监控器：抽样下网关仍居 Top-1', () => {
  const { graph } = buildHybridNetwork();
  const mon = new BottleneckMonitor(graph, { topK: 3, sampleSize: 6, seed: 99 });
  const top = mon.topKNodes();
  assert.equal(top[0].node, 'GW');
  assert.equal(mon.analyze().exact, false);
});
