import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import type { ClusterNode } from '../src/node.ts';
import type { NodeId } from '../src/types.ts';

const SIZE = 8;
const SEED = 20261004;
const DROP_RATE = 0.9;
const DEAD: NodeId[] = ['n6', 'n7'];

const statusOf = (node: ClusterNode, id: NodeId) => node.states.get(id)?.status;

function buildDivergedCluster(): Cluster {
  const cluster = new Cluster(SIZE, { dropRate: DROP_RATE, seed: SEED });
  cluster.gossipRounds(6);
  cluster.killNode('n6', 'n0');
  cluster.killNode('n7', 'n1');
  cluster.gossipRounds(6);
  return cluster;
}

test('acceptance: 90% gossip loss, anti-entropy locates and repairs exactly the 2 missed offline events', () => {
  const cluster = buildDivergedCluster();

  // 1) The gossip piggyback channel really is lossy (~90% of packets dropped).
  assert.ok(
    cluster.transport.dropRatio >= 0.8,
    `expected >=80% drop ratio, got ${cluster.transport.dropRatio}`,
  );

  // 2) Probabilistic long-tail loss: some replica missed both offline events.
  const lagging = cluster
    .aliveNodes()
    .filter((n) => statusOf(n, 'n6') !== 'dead' && statusOf(n, 'n7') !== 'dead');
  assert.ok(lagging.length >= 1, 'expected at least one replica that missed both offline events');
  const lag = lagging[0];

  // 3) The two detectors reconcile, then the lagging replica runs one
  //    anti-entropy exchange: the Merkle walk must locate exactly {n6, n7}.
  cluster.get('n0').syncWith('n1');
  cluster.get('n1').syncWith('n0');
  const result = lag.syncWith('n0');
  assert.ok(result);
  assert.deepEqual(result.keys, DEAD, 'tree diff must locate exactly the 2 missed offline events');
  assert.deepEqual([...result.repaired].sort(), DEAD);
  assert.equal(result.levelsTraversed, 2, 'diff traverses the tree depth by depth');
  assert.ok(result.nodesCompared < 20, `tree pruning keeps comparisons low (${result.nodesCompared})`);

  // 4) The missed events are repaired on the lagging replica.
  assert.equal(statusOf(lag, 'n6'), 'dead');
  assert.equal(statusOf(lag, 'n7'), 'dead');

  // 5) The low-frequency background loop converges the whole cluster.
  const rounds = cluster.runAntiEntropyUntilConverged();
  assert.ok(rounds > 0);
  assert.ok(cluster.converged(), 'all live replicas must share one Merkle root');
  for (const node of cluster.aliveNodes()) {
    assert.equal(statusOf(node, 'n6'), 'dead', `${node.id} must know n6 is offline`);
    assert.equal(statusOf(node, 'n7'), 'dead', `${node.id} must know n7 is offline`);
  }
});

test('background anti-entropy coroutine converges the cluster on its own', async () => {
  const cluster = buildDivergedCluster();
  assert.ok(!cluster.converged());

  cluster.startAntiEntropy(5);
  try {
    const deadline = Date.now() + 5000;
    while (!cluster.converged() && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    assert.ok(cluster.converged(), 'background coroutine should converge the cluster');
    for (const node of cluster.aliveNodes()) {
      assert.equal(statusOf(node, 'n6'), 'dead');
      assert.equal(statusOf(node, 'n7'), 'dead');
    }
  } finally {
    cluster.stopAll();
  }
});

test('out-of-order delivery: stale events never overwrite newer state', () => {
  const cluster = new Cluster(4, { dropRate: 0, seed: 1 });
  const node = cluster.get('n0');
  assert.equal(node.merge({ nodeId: 'n1', status: 'dead', version: 2, updatedBy: 'n2' }), true);
  assert.equal(node.merge({ nodeId: 'n1', status: 'alive', version: 1, updatedBy: 'n1' }), false);
  assert.equal(statusOf(node, 'n1'), 'dead');
  assert.equal(node.states.get('n1')?.version, 2);
});
