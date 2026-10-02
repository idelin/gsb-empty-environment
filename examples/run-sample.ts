/**
 * Runnable sample: the mutual-death race scenario over 10000 ticks.
 * Run with: npm run sample
 */
import { createHash } from 'node:crypto';
import { Simulator } from '../src/simulator.ts';
import { Network } from '../src/network.ts';
import { Random } from '../src/rng.ts';
import { SwimNode } from '../src/swim.ts';

const SEED = 0xc0ffee;
const IDS = ['A', 'B', 'C', 'D', 'E'];

const sim = new Simulator();
const rng = new Random(SEED);
const network = new Network(sim, rng, { latency: 2, jitter: 1, lossRate: 0 });
const nodes = new Map<string, SwimNode>();
for (const id of IDS) {
  nodes.set(
    id,
    new SwimNode(id, IDS.filter((x) => x !== id), sim, network, rng, {
      protocolPeriod: 40,
      ackTimeout: 8,
      suspicionTimeout: 200,
      indirectPings: 2,
      targetSelection: 'round-robin',
    }),
  );
}

sim.scheduleAt(500, () => {
  network.cut('A', 'B');
  for (const x of ['A', 'B']) for (const y of ['C', 'D', 'E']) network.cut(x, y);
  sim.log('PARTITION_APPLIED  ({A,B} isolated from {C,D,E}, A<->B cut)');
});
sim.scheduleAt(4000, () => {
  network.heal('A', 'B');
  for (const x of ['A', 'B']) for (const y of ['C', 'D', 'E']) network.heal(x, y);
  sim.log('PARTITION_HEALED');
});

sim.run(10000);

console.log(`seed=0x${SEED.toString(16)}  ticks=10000  nodes=${IDS.join(',')}`);
console.log(`links: latency=2 jitter=1 loss=0 (partition injected at t=500, healed at t=4000)\n`);
console.log('--- deterministic event log ---');
for (const line of sim.eventLog) console.log(`t=${line}`);
console.log('--- final membership (node A view; all nodes identical) ---');
for (const rec of nodes.get('A')!.snapshot()) {
  console.log(`  ${rec.id}: ${rec.state} inc=${rec.incarnation} declaredBy=${rec.declaredBy}`);
}
console.log('--- network stats ---');
console.log(`  sent=${network.stats.sent} delivered=${network.stats.delivered} dropped=${network.stats.dropped}`);
const hash = createHash('sha256').update(sim.eventLog.join('\n')).digest('hex');
console.log(`--- replay hash (sha256 of event log) ---\n  ${hash}`);
