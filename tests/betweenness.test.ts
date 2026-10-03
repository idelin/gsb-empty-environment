import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WeightedGraph } from '../src/graph.ts';
import { betweennessCentrality, shortestPath, edgeKey } from '../src/betweenness.ts';
import { buildStarNetwork, buildMeshNetwork, buildHybridNetwork } from '../src/topology.ts';

test('星型拓扑：中心节点介数 = C(n-1,2)，叶子为 0', () => {
  const g = buildStarNetwork(4); // 5 个节点
  const { nodeBC, exact } = betweennessCentrality(g);
  assert.equal(exact, true);
  assert.equal(nodeBC.get('C'), 6); // C(4,2) = 6
  for (const leaf of ['L1', 'L2', 'L3', 'L4']) assert.equal(nodeBC.get(leaf), 0);
});

test('网状拓扑（全连接）：所有节点介数为 0', () => {
  const g = buildMeshNetwork(5);
  const { nodeBC } = betweennessCentrality(g);
  for (const v of nodeBC.values()) assert.equal(v, 0);
});

test('边介数：星型拓扑每条边介数 = 叶子数', () => {
  const g = buildStarNetwork(4);
  const { edgeBC } = betweennessCentrality(g);
  for (const leaf of ['L1', 'L2', 'L3', 'L4']) {
    assert.equal(edgeBC.get(edgeKey('C', leaf)), 4);
  }
});

test('双路径分流：等权时两中继各承担一半流量', () => {
  const g = new WeightedGraph();
  g.addEdge('s', 'a', 1);
  g.addEdge('a', 't', 1);
  g.addEdge('s', 'b', 1);
  g.addEdge('b', 't', 1);
  const { nodeBC } = betweennessCentrality(g);
  assert.equal(nodeBC.get('a'), 0.5);
  assert.equal(nodeBC.get('b'), 0.5);
});

test('近似模式：抽样结果可复现且星型中心仍居首', () => {
  const g = buildStarNetwork(8);
  const r1 = betweennessCentrality(g, { sampleSize: 3, seed: 7 });
  const r2 = betweennessCentrality(g, { sampleSize: 3, seed: 7 });
  assert.equal(r1.exact, false);
  assert.deepEqual(r1.sampledSources, r2.sampledSources);
  assert.deepEqual([...r1.nodeBC.entries()], [...r2.nodeBC.entries()]);
  const top = [...r1.nodeBC.entries()].sort((a, b) => b[1] - a[1])[0];
  assert.equal(top[0], 'C');
});

test('混合网络：中心网关 GW 是首要瓶颈', () => {
  const { graph, gateway } = buildHybridNetwork();
  const { nodeBC } = betweennessCentrality(graph);
  const gw = nodeBC.get(gateway)!;
  for (const [n, v] of nodeBC) {
    if (n !== gateway) assert.ok(gw > v, `GW(${gw}) should exceed ${n}(${v})`);
  }
});

test('最短路径：权重变化后路径随之改变', () => {
  const g = new WeightedGraph();
  g.addEdge('s', 'a', 1);
  g.addEdge('a', 't', 1);
  g.addEdge('s', 'b', 1);
  g.addEdge('b', 't', 1);
  g.updateEdgeWeight('s', 'a', 5);
  const { path, distance } = shortestPath(g, 's', 't');
  assert.deepEqual(path, ['s', 'b', 't']);
  assert.equal(distance, 2);
});
