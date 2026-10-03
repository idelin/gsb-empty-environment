import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fullSpf, incrementalSpf, buildRoutes } from '../src/spf.ts';
import { LSDB, type ChangedEdge } from '../src/lsdb.ts';
import type { NodeId, Topology } from '../src/types.ts';

/** 由边列表构建无向拓扑 */
function makeTopo(edges: [NodeId, NodeId, number][]): Topology {
  const topo: Topology = new Map();
  const ensure = (id: NodeId) => {
    if (!topo.has(id)) topo.set(id, new Map());
    return topo.get(id)!;
  };
  for (const [a, b, c] of edges) {
    ensure(a).set(b, c);
    ensure(b).set(a, c);
  }
  return topo;
}

function removeEdge(topo: Topology, a: NodeId, b: NodeId): Topology {
  const copy: Topology = new Map();
  for (const [u, adj] of topo) copy.set(u, new Map(adj));
  copy.get(a)?.delete(b);
  copy.get(b)?.delete(a);
  return copy;
}

const EDGES: [NodeId, NodeId, number][] = [
  ['R1', 'R2', 1], ['R2', 'R3', 1], ['R3', 'R4', 1], ['R4', 'R1', 1],
  ['R5', 'R6', 1], ['R6', 'R7', 1], ['R7', 'R8', 1], ['R8', 'R5', 1],
  ['R3', 'R5', 2], ['R4', 'R8', 3],
];

test('全量 Dijkstra: 距离与首跳正确', () => {
  const topo = makeTopo(EDGES);
  const { dist, prev } = fullSpf(topo, 'R1');
  assert.equal(dist.get('R1'), 0);
  assert.equal(dist.get('R6'), 5);
  const routes = buildRoutes('R1', dist, prev);
  assert.equal(routes.get('R6')!.nextHop, 'R2');
  assert.equal(routes.get('R6')!.cost, 5);
});

test('增量 SPF: 断链后与全量结果一致，且只重算受影响目的地', () => {
  const topo = makeTopo(EDGES);
  const before = fullSpf(topo, 'R1');
  const after = removeEdge(topo, 'R3', 'R5');
  const changes: ChangedEdge[] = [{ u: 'R3', v: 'R5', kind: 'down' }];
  const inc = incrementalSpf(after, 'R1', before.dist, before.prev, changes);
  const full = fullSpf(after, 'R1');
  assert.equal(inc.mode, 'incremental');
  assert.ok(inc.recomputed > 0 && inc.recomputed < after.size);
  for (const id of after.keys()) {
    assert.equal(inc.dist.get(id), full.dist.get(id), `dist ${id}`);
  }
  // R1->R6 改走备用主干 R1-R4-R8-R5-R6 = 1+3+1+1 = 6
  assert.equal(inc.dist.get('R6'), 6);
});

test('增量 SPF: 权值上升与全量结果一致', () => {
  const topo = makeTopo(EDGES);
  const before = fullSpf(topo, 'R1');
  const after = makeTopo(EDGES.map(([a, b, c]) =>
    (a === 'R3' && b === 'R5') || (a === 'R5' && b === 'R3') ? [a, b, 10] : [a, b, c],
  ) as [NodeId, NodeId, number][]);
  const changes: ChangedEdge[] = [{ u: 'R3', v: 'R5', kind: 'cost-up' }];
  const inc = incrementalSpf(after, 'R1', before.dist, before.prev, changes);
  const full = fullSpf(after, 'R1');
  assert.equal(inc.mode, 'incremental');
  for (const id of after.keys()) {
    assert.equal(inc.dist.get(id), full.dist.get(id), `dist ${id}`);
  }
});

test('增量 SPF: 改善型变化（链路 Up / 权值下降）回退全量', () => {
  const topo = makeTopo(EDGES);
  const before = fullSpf(topo, 'R1');
  const up = incrementalSpf(topo, 'R1', before.dist, before.prev, [
    { u: 'R2', v: 'R6', kind: 'up' },
  ]);
  assert.equal(up.mode, 'full');
  const down = incrementalSpf(topo, 'R1', before.dist, before.prev, [
    { u: 'R3', v: 'R5', kind: 'cost-down' },
  ]);
  assert.equal(down.mode, 'full');
});

test('增量 SPF: 未受影响时零重算', () => {
  const topo = makeTopo(EDGES);
  const before = fullSpf(topo, 'R1');
  // R1 的 SPT 不含 R6-R7（R7 经 R4-R8 到达），断开它不影响任何最短路径
  const after = removeEdge(topo, 'R6', 'R7');
  const inc = incrementalSpf(after, 'R1', before.dist, before.prev, [
    { u: 'R6', v: 'R7', kind: 'down' },
  ]);
  assert.equal(inc.mode, 'incremental');
  assert.equal(inc.recomputed, 0);
  const full = fullSpf(after, 'R1');
  for (const id of after.keys()) {
    assert.equal(inc.dist.get(id), full.dist.get(id), `dist ${id}`);
  }
});

test('LSDB: 拒绝过期序列号的 LSA', () => {
  const db = new LSDB();
  const lsa = { origin: 'R1', seq: 5, age: 0, links: [{ neighbor: 'R2', cost: 1, up: true }] };
  assert.equal(db.insert(lsa), true);
  assert.equal(db.insert({ ...lsa, seq: 4 }), false);
  assert.equal(db.insert({ ...lsa, seq: 5 }), false);
  assert.equal(db.insert({ ...lsa, seq: 6 }), true);
});

test('LSDB: 双向校验——单边通告的链路不参与计算', () => {
  const db = new LSDB();
  db.insert({ origin: 'R1', seq: 1, age: 0, links: [{ neighbor: 'R2', cost: 1, up: true }] });
  db.insert({ origin: 'R2', seq: 1, age: 0, links: [{ neighbor: 'R1', cost: 1, up: false }] });
  const topo = db.toTopology();
  assert.equal(topo.get('R1')!.size, 0);
  assert.equal(topo.get('R2')!.size, 0);
});
