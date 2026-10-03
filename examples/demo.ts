/**
 * Runnable demo:
 *   node --experimental-strip-types examples/demo.ts
 *
 * Part 1: Multi-Paxos across a 5-node cluster with a network partition.
 * Part 2: Hierarchical sharding with locality-aware routing at scale.
 */
import { Cluster } from '../src/paxos/cluster.ts';
import { ShardingEngine } from '../src/sharding/engine.ts';
import { Topology } from '../src/sharding/topology.ts';

function line(title: string): void {
  console.log(`\n=== ${title} ${'='.repeat(Math.max(0, 60 - title.length))}`);
}

async function paxosDemo(): Promise<void> {
  line('Part 1: Multi-Paxos consensus log (5 nodes)');
  const cluster = new Cluster(['N1', 'N2', 'N3', 'N4', 'N5'], { leaseMs: 100 });

  const n1 = cluster.elect('N1');
  console.log(`[elect] N1 becomes leader, term=${n1.getCurrentBallot()?.term}, lease valid=${n1.hasValidLease()}`);

  cluster.propose('set user:1 = alice');
  cluster.propose('set user:2 = bob');
  console.log(`[commit] fast path (lease, phase 2 only): commitIndex=${n1.getCommitIndex()}`);

  cluster.network.partition([['N1', 'N2'], ['N3', 'N4', 'N5']]);
  console.log('[partition] {N1,N2} isolated from {N3,N4,N5}');

  n1.propose('set user:3 = dirty');
  cluster.network.flush();
  console.log(`[old leader] accepts write in minority, commitIndex stuck at ${n1.getCommitIndex()} (cannot commit)`);

  cluster.advanceClock(200);
  console.log(`[lease] old leader lease expired: ${!n1.hasValidLease()}`);

  const n3 = cluster.node('N3');
  n3.campaign();
  cluster.network.flush();
  console.log(`[elect] N3 becomes leader, term=${n3.getCurrentBallot()?.term} (phase 1 repaired undecided slots)`);

  n3.propose('set user:3 = carol');
  n3.propose('set user:4 = dave');
  cluster.network.flush();
  console.log(`[new leader] majority commits slots 3-4, commitIndex=${n3.getCommitIndex()}`);

  cluster.network.heal();
  n3.propose('set user:5 = erin');
  cluster.network.flush();
  console.log(`[heal] old leader role=${n1.getRole()}, all replicas converged:`);
  for (const id of ['N1', 'N2', 'N3', 'N4', 'N5']) {
    const node = cluster.node(id);
    console.log(
      `  ${id}: commitIndex=${node.getCommitIndex()} log=[${node.committedEntries().map((e) => e.value).join(' | ')}]`,
    );
  }
  cluster.dispose();
}

async function shardingDemo(): Promise<void> {
  line('Part 2: Hierarchical sharding + locality-aware routing');
  const topology = new Topology();
  const regions = [
    { region: 'east', zones: ['ea', 'eb'] },
    { region: 'west', zones: ['wa', 'wb'] },
    { region: 'north', zones: ['na', 'nb'] },
  ];
  let n = 0;
  for (const { region, zones } of regions) {
    for (const zone of zones) {
      for (let i = 0; i < 2; i++) {
        topology.addNode({ id: `node-${n++}`, region, zone });
      }
    }
  }
  topology.setRegionLatency('east', 'west', 30);
  topology.setRegionLatency('east', 'north', 20);
  topology.setRegionLatency('west', 'north', 25);
  console.log(`[topology] ${topology.size} nodes across 3 regions x 2 zones`);

  const engine = new ShardingEngine(topology, { vshardCount: 4096, replicationFactor: 3 });
  console.log(`[shards] 4096 virtual shards, replicationFactor=3`);

  const clients = topology.list();
  let sameZone = 0;
  let sameRegion = 0;
  let crossRegion = 0;
  const KEYS = 30000;
  for (let i = 0; i < KEYS; i++) {
    const client = clients[i % clients.length];
    const route = engine.router.routeRead(`key:${i}`, client.id);
    if (route.cost <= 1) sameZone++;
    else if (route.cost <= 5) sameRegion++;
    else crossRegion++;
  }
  const pct = (x: number): string => `${((x / KEYS) * 100).toFixed(1)}%`;
  console.log(`[locality] ${KEYS} reads from random clients:`);
  console.log(`  same-zone:    ${pct(sameZone)}`);
  console.log(`  same-region:  ${pct(sameRegion)}`);
  console.log(`  cross-region: ${pct(crossRegion)}`);

  const before = engine.getShardMap();
  const plan = await engine.addNode({ id: `node-${n}`, region: 'east', zone: 'ea' });
  console.log(
    `[rebalance] +1 node -> moved ${plan.moves.length}/${before.vshardCount} shards ` +
      `(${((plan.moves.length / before.vshardCount) * 100).toFixed(1)}%, ideal ~${((3 / 13) * 100).toFixed(1)}% for RF=3)`,
  );

  const counts = engine.getShardMap().replicaCounts();
  const loads = [...counts.entries()].map(([id, c]) => `${id}:${c}`).join(' ');
  console.log(`[balance] replicas per node -> ${loads}`);

  engine.dispose();
  topology.dispose();
}

await paxosDemo();
await shardingDemo();
console.log('\nDemo finished.');
