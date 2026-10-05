import { Network } from './network.ts';
import { canonicalTopology } from './topology.ts';
import type { LinkEnd } from './lsa.ts';

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`ASSERT FAILED: ${msg}`);
  console.log(`  ok: ${msg}`);
}

function linksOf(net: Network, id: string): LinkEnd[] {
  return net.upNeighbors(id).map((n) => ({ neighbor: n, metric: 1 + Math.floor(net.random() * 9) }));
}

function assertConverged(net: Network, ids: string[], label: string): void {
  const digests = ids.map((id) => net.node(id).dbDigest());
  assert(digests.every((d) => d === digests[0]), `${label}: ${ids.length} 个节点 LSDB 完全一致`);
  const topos = ids.map((id) => canonicalTopology(net.node(id).lsdb));
  assert(topos.every((t) => t === topos[0]), `${label}: ${ids.length} 个节点拓扑视图完全一致`);
}

function scenarioStaleSkip(): void {
  console.log('\n[场景1] 被同源新版本覆盖的旧更新应被跳过');
  const net = new Network(1);
  const a = net.addNode('a');
  const b = net.addNode('b');
  net.addEdge('a', 'b');
  const v1 = a.originate([{ neighbor: 'b', metric: 5 }]);
  a.updateLinks([{ neighbor: 'b', metric: 6 }]);
  const v3 = a.updateLinks([{ neighbor: 'b', metric: 7 }]);
  net.run();
  net.send('a', 'b', { kind: 'update', lsa: v1 });
  net.run();
  const stored = b.lsdb.get('a')!;
  assert(stored.version === 3 && stored.links[0].metric === 7, 'b 保留 v3，迟到的 v1 被跳过');
  assert(b.stats.skippedStale >= 1, `b 统计到过期更新被跳过 (实际 ${b.stats.skippedStale} 条)`);
  void v3;
}

function scenarioDeleteRecreate(): void {
  console.log('\n[场景2] 删除后重建必须使用新代次，旧代次更新无法覆盖');
  const net = new Network(2);
  const a = net.addNode('a');
  const b = net.addNode('b');
  net.addEdge('a', 'b');
  a.originate([{ neighbor: 'b', metric: 1 }]);
  a.updateLinks([{ neighbor: 'b', metric: 2 }]);
  const staleGen1 = a.updateLinks([{ neighbor: 'b', metric: 3 }]);
  a.withdraw();
  const recreated = a.recreate([{ neighbor: 'b', metric: 9 }]);
  net.run();
  net.send('a', 'b', { kind: 'update', lsa: staleGen1 });
  net.run();
  const stored = b.lsdb.get('a')!;
  assert(recreated.generation === 2, '重建后代次升为 2');
  assert(stored.generation === 2 && stored.version === 1 && !stored.withdrawn, 'b 上是 gen2 的新 LSA');
  assert(stored.links[0].metric === 9, '迟到的 gen1 旧版本未能覆盖 gen2');
}

function scenarioVectorVsGlobalMax(): void {
  console.log('\n[场景3] 每来源进度向量，而非全局最大版本');
  const net = new Network(3);
  const x = net.addNode('x');
  const y = net.addNode('y');
  const p = net.addNode('p');
  const q = net.addNode('q');
  net.addEdge('x', 'p');
  net.addEdge('y', 'q');
  p.originate([{ neighbor: 'x', metric: 1 }]);
  for (let i = 0; i < 4; i++) p.updateLinks([{ neighbor: 'x', metric: 1 }]);
  q.originate([{ neighbor: 'y', metric: 1 }]);
  for (let i = 0; i < 4; i++) q.updateLinks([{ neighbor: 'y', metric: 1 }]);
  net.run();
  const maxOf = (id: string) =>
    Math.max(...[...net.node(id).lsdb.values()].map((l) => l.version));
  assert(maxOf('x') === 5 && maxOf('y') === 5, 'x 与 y 的全局最大版本相同 (都是 5)，但数据库不同');
  assert(!x.lsdb.has('q') && !y.lsdb.has('p'), 'x 缺 q、y 缺 p：全局最大版本会误判为已同步');
  net.addEdge('x', 'y');
  net.setEdgeUp('x', 'y', false);
  net.setEdgeUp('x', 'y', true);
  net.run();
  assert(x.lsdb.has('q') && y.lsdb.has('p'), '摘要交换 + 缺失补齐后双方互相补全');
  assertConverged(net, ['x', 'y'], '场景3');
}

function scenarioPartition(): void {
  console.log('\n[场景4] 分区后重连，全网拓扑收敛一致');
  const net = new Network(42);
  const ids = Array.from({ length: 8 }, (_, i) => `n${i}`);
  for (const id of ids) net.addNode(id);
  const edges: Array<[string, string]> = [
    ['n0', 'n1'], ['n1', 'n2'], ['n2', 'n3'], ['n3', 'n4'],
    ['n4', 'n5'], ['n5', 'n6'], ['n6', 'n7'], ['n7', 'n0'],
    ['n0', 'n4'], ['n1', 'n5'], ['n2', 'n6'], ['n3', 'n7'],
  ];
  for (const [a, b] of edges) net.addEdge(a, b);

  console.log('  阶段1: 初始泛洪与随机更新');
  for (const id of ids) net.node(id).originate(linksOf(net, id));
  net.run();
  for (const id of ['n0', 'n3', 'n5', 'n7']) net.node(id).updateLinks(linksOf(net, id));
  net.run();
  assertConverged(net, ids, '阶段1 收敛');

  console.log('  阶段2: 分区 {n0..n3} | {n4..n7}，两侧独立演化');
  const groupA = ['n0', 'n1', 'n2', 'n3'];
  const groupB = ['n4', 'n5', 'n6', 'n7'];
  const inA = new Set(groupA);
  const cutEdges = edges.filter(([a, b]) => inA.has(a) !== inA.has(b));
  for (const [a, b] of cutEdges) net.setEdgeUp(a, b, false);
  for (const id of ids) net.node(id).updateLinks(linksOf(net, id));
  net.node('n1').updateLinks(linksOf(net, 'n1'));
  net.node('n1').updateLinks(linksOf(net, 'n1'));
  net.node('n5').withdraw();
  net.node('n5').recreate(linksOf(net, 'n5'));
  net.node('n6').withdraw();
  net.node('n4').updateLinks(linksOf(net, 'n4'));
  net.run();
  assertConverged(net, groupA, '分区A 内部收敛');
  assertConverged(net, groupB, '分区B 内部收敛');
  assert(
    net.node('n0').dbDigest() !== net.node('n4').dbDigest(),
    '两个分区的 LSDB 已发散',
  );
  assert(!net.node('n0').lsdb.has('n5') || net.node('n0').lsdb.get('n5')!.generation === 1,
    '分区A 看不到 n5 的 gen2 重建');

  console.log('  阶段3: 重连，触发摘要交换与缺失补齐');
  for (const [a, b] of cutEdges) net.setEdgeUp(a, b, true);
  for (const id of ids) {
    if (id !== 'n6') net.node(id).updateLinks(linksOf(net, id));
  }
  net.run();
  assertConverged(net, ids, '重连后全网收敛');
  for (const id of ids) {
    const n5 = net.node(id).lsdb.get('n5')!;
    assert(n5.generation === 2 && !n5.withdrawn, `${id} 上 n5 为 gen2 重建版本`);
    assert(net.node(id).lsdb.get('n6')!.withdrawn, `${id} 上 n6 的删除(墓碑)已同步`);
  }
  const topo = canonicalTopology(net.node('n0').lsdb);
  console.log('  最终拓扑(双向确认的邻接):');
  for (const line of topo.split('\n')) console.log(`    ${line}`);
  const totalSkipped = ids.reduce((s, id) => s + net.node(id).stats.skippedStale, 0);
  console.log(`  统计: 投递 ${net.delivered} 条消息, 跳过过期更新 ${totalSkipped} 条`);
}

console.log('链路状态同步模拟器');
scenarioStaleSkip();
scenarioDeleteRecreate();
scenarioVectorVsGlobalMax();
scenarioPartition();
console.log('\n全部场景通过');
