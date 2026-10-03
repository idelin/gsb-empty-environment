import { Cluster } from './cluster.ts';
import { BRANCHING_FACTOR } from './merkle.ts';
import type { ClusterNode } from './node.ts';
import type { NodeId } from './types.ts';

const SIZE = 8;
const DEAD: NodeId[] = ['n6', 'n7'];
const cluster = new Cluster(SIZE, { dropRate: 0.9, seed: 20261004 });

const view = (node: ClusterNode, id: NodeId) => node.states.get(id)?.status ?? 'unknown';

console.log('=== Phase 1: gossip with 90% piggyback packet loss ===');
cluster.gossipRounds(6);
cluster.killNode('n6', 'n0'); // detector n0 declares n6 offline
cluster.killNode('n7', 'n1'); // detector n1 declares n7 offline
cluster.gossipRounds(6);

const t = cluster.transport;
console.log(
  `gossip packets: sent=${t.sent} dropped=${t.dropped} delivered=${t.delivered} ` +
    `(drop ratio ${(t.dropRatio * 100).toFixed(1)}%)`,
);
console.log('\nview of the two offline events after gossip:');
for (const id of cluster.order) {
  const n = cluster.get(id);
  const tag = n.alive ? '' : '  [offline]';
  console.log(`  ${id}: n6=${view(n, 'n6')} n7=${view(n, 'n7')}${tag}`);
}

const lagging = cluster
  .aliveNodes()
  .filter((n) => view(n, 'n6') !== 'dead' && view(n, 'n7') !== 'dead');
if (lagging.length === 0) throw new Error('demo seed broken: gossip fully converged');
const lag = lagging[0];
console.log(`\nlagging replica: ${lag.id} (missed both offline events)`);

console.log('\n=== Phase 2: anti-entropy background repair ===');
// Let the two detectors reconcile first so n0 holds both events.
cluster.get('n0').syncWith('n1');
cluster.get('n1').syncWith('n0');

// Show the depth-by-depth tree walk between the lagging node and n0.
const peer = cluster.get('n0');
console.log(`\n${lag.id} root != ${peer.id} root -> walking the Merkle tree:`);
console.log(`  depth 1 (root children):`);
const localTree = lag.merkle();
const localChildren = localTree.childrenOf([]);
const remoteChildren = peer.childrenOf([]);
const divergent: number[] = [];
for (let i = 0; i < BRANCHING_FACTOR; i++) {
  const same = localChildren[i].hash === remoteChildren[i].hash;
  if (!same) divergent.push(i);
  console.log(`    branch[${i}] ${same ? 'equal (pruned)' : 'DIFFER -> descend'}`);
}
console.log(`  depth 2 (leaf buckets under differing branches):`);
for (const branch of divergent) {
  const l = localTree.childrenOf([branch]);
  const r = peer.childrenOf([branch]);
  for (let i = 0; i < l.length; i++) {
    if (l[i].hash !== r[i].hash) {
      const keys = [...new Set([...(l[i].keys ?? []), ...(r[i].keys ?? [])])].sort();
      console.log(`    bucket[${branch}][${i}] DIFFER -> keys: ${keys.join(', ')}`);
    }
  }
}

const result = lag.syncWith('n0')!;
console.log(
  `\n${lag.id} <- ${result.peer}: located keys=[${result.keys.join(', ')}] ` +
    `repaired=[${result.repaired.join(', ')}] ` +
    `(levels=${result.levelsTraversed}, hash comparisons=${result.nodesCompared}, round trips=${result.roundTrips})`,
);
console.log(`  ${lag.id} now sees: n6=${view(lag, 'n6')} n7=${view(lag, 'n7')}`);

console.log('\n=== Phase 3: low-frequency background coroutine finishes the job ===');
const rounds = cluster.runAntiEntropyUntilConverged();
console.log(`anti-entropy rounds to full convergence: ${rounds}`);
console.log(`converged: ${cluster.converged()}`);
for (const id of cluster.order) {
  const n = cluster.get(id);
  if (!n.alive) continue;
  console.log(`  ${id}: n6=${view(n, 'n6')} n7=${view(n, 'n7')} root=${n.rootHash().slice(0, 12)}…`);
}
const total = cluster.aliveNodes().reduce(
  (acc, n) => ({
    rounds: acc.rounds + n.aeStats.rounds,
    keysRepaired: acc.keysRepaired + n.aeStats.keysRepaired,
    nodesCompared: acc.nodesCompared + n.aeStats.nodesCompared,
  }),
  { rounds: 0, keysRepaired: 0, nodesCompared: 0 },
);
console.log(
  `\nanti-entropy totals: rounds=${total.rounds} keysRepaired=${total.keysRepaired} ` +
    `hashComparisons=${total.nodesCompared}`,
);
