import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Lsdb } from '../src/lsdb.ts';
import { buildGraph, diffGraphs, fullSpf, incrementalSpf } from '../src/spf.ts';
import type { Adjacency, NodeId } from '../src/types.ts';

function makeLsdb(links: Array<[NodeId, NodeId, number]>): Lsdb {
  const lsdb = new Lsdb();
  const adj = new Map<NodeId, Array<{ neighbor: NodeId; cost: number }>>();
  for (const [a, b, cost] of links) {
    if (!adj.has(a)) adj.set(a, []);
    if (!adj.has(b)) adj.set(b, []);
    (adj.get(a) as Array<{ neighbor: NodeId; cost: number }>).push({ neighbor: b, cost });
    (adj.get(b) as Array<{ neighbor: NodeId; cost: number }>).push({ neighbor: a, cost });
  }
  for (const [id, lsaLinks] of adj) {
    lsdb.apply({ originator: id, seq: 1, links: lsaLinks });
  }
  return lsdb;
}

function cloneGraph(g: Adjacency): Adjacency {
  const copy: Adjacency = new Map();
  for (const [n, edges] of g) copy.set(n, new Map(edges));
  return copy;
}

test('full Dijkstra computes known shortest distances', () => {
  const lsdb = makeLsdb([
    ['A', 'B', 1],
    ['B', 'C', 1],
    ['A', 'C', 5],
    ['C', 'D', 1],
    ['B', 'D', 10],
  ]);
  const graph = buildGraph(lsdb);
  const { dist, prev } = fullSpf(graph, 'A');
  assert.equal(dist.get('A'), 0);
  assert.equal(dist.get('B'), 1);
  assert.equal(dist.get('C'), 2);
  assert.equal(dist.get('D'), 3);
  assert.equal(prev.get('C'), 'B');
  assert.equal(prev.get('D'), 'C');
});

test('two-way check hides half-visible links', () => {
  const lsdb = new Lsdb();
  lsdb.apply({ originator: 'A', seq: 1, links: [{ neighbor: 'B', cost: 1 }] });
  lsdb.apply({ originator: 'B', seq: 1, links: [] });
  const graph = buildGraph(lsdb);
  assert.equal(graph.get('A')?.size ?? 0, 0);
});

test('incremental SPF after link removal matches full recompute', () => {
  const links: Array<[NodeId, NodeId, number]> = [
    ['A', 'B', 1], ['B', 'C', 1], ['C', 'D', 1], ['D', 'A', 1],
    ['A', 'C', 1], ['B', 'D', 4], ['C', 'E', 1], ['D', 'E', 1],
  ];
  const before = buildGraph(makeLsdb(links));
  const base = fullSpf(before, 'A');

  const afterLinks = links.filter(([a, b]) => !(a === 'A' && b === 'C') && !(a === 'C' && b === 'A'));
  const after = buildGraph(makeLsdb(afterLinks));

  const worsened = diffGraphs(before, after);
  assert.ok(worsened.length > 0);
  const inc = incrementalSpf(after, 'A', base.dist, base.prev, worsened);
  const full = fullSpf(after, 'A');

  for (const node of after.keys()) {
    assert.equal(inc.dist.get(node), full.dist.get(node), `dist mismatch for ${node}`);
  }
  assert.ok(inc.affectedCount < after.size, 'incremental should touch fewer nodes than full SPF');
});

test('incremental SPF after cost decrease matches full recompute', () => {
  const links: Array<[NodeId, NodeId, number]> = [
    ['A', 'B', 5], ['A', 'C', 1], ['C', 'B', 1], ['B', 'D', 1], ['C', 'D', 10],
  ];
  const before = buildGraph(makeLsdb(links));
  const base = fullSpf(before, 'A');

  const cheaper = links.map(([a, b, c]): [NodeId, NodeId, number] => {
    const pair = [a, b].sort().join('-');
    return pair === 'A-B' ? [a, b, 1] : [a, b, c];
  });
  const after = buildGraph(makeLsdb(cheaper));

  const worsened = diffGraphs(before, after);
  assert.equal(worsened.length, 0, 'cost decrease is not a worsening');
  const inc = incrementalSpf(after, 'A', base.dist, base.prev, worsened);
  const full = fullSpf(after, 'A');
  for (const node of after.keys()) {
    assert.equal(inc.dist.get(node), full.dist.get(node), `dist mismatch for ${node}`);
  }
  assert.equal(inc.dist.get('D'), 2);
});

test('incremental SPF across random topologies equals full Dijkstra', () => {
  let seed = 42;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  for (let trial = 0; trial < 30; trial++) {
    const ids = ['N0', 'N1', 'N2', 'N3', 'N4', 'N5', 'N6', 'N7'];
    const links: Array<[NodeId, NodeId, number]> = [];
    for (let i = 1; i < ids.length; i++) {
      links.push([ids[Math.floor(rand() * i)] as NodeId, ids[i] as NodeId, 1 + Math.floor(rand() * 9)]);
    }
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        if (rand() < 0.3) links.push([ids[i] as NodeId, ids[j] as NodeId, 1 + Math.floor(rand() * 9)]);
      }
    }
    const before = buildGraph(makeLsdb(links));
    const source = ids[0] as NodeId;
    const base = fullSpf(before, source);

    const mutated = cloneGraph(before);
    for (const [a, edges] of mutated) {
      for (const [b, c] of edges) {
        const r = rand();
        if (r < 0.15) edges.delete(b);
        else if (r < 0.4) edges.set(b, 1 + Math.floor(rand() * 12));
        else edges.set(b, c);
      }
    }
    const worsened = diffGraphs(before, mutated);
    const inc = incrementalSpf(mutated, source, base.dist, base.prev, worsened);
    const full = fullSpf(mutated, source);
    for (const node of mutated.keys()) {
      assert.equal(inc.dist.get(node), full.dist.get(node), `trial ${trial} node ${node}`);
    }
  }
});

test('LSDB rejects stale and duplicate LSAs', () => {
  const lsdb = new Lsdb();
  assert.equal(lsdb.apply({ originator: 'A', seq: 5, links: [] }), 'applied');
  assert.equal(lsdb.apply({ originator: 'A', seq: 5, links: [] }), 'duplicate');
  assert.equal(lsdb.apply({ originator: 'A', seq: 3, links: [] }), 'stale');
  assert.equal(lsdb.apply({ originator: 'A', seq: 6, links: [] }), 'applied');
});
