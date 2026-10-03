import { Simulator } from '../src/simulator.ts';
import { SwimNode } from '../src/swim/node.ts';

/**
 * Demo: split-brain race + crash detection, fully deterministic.
 * Run with: npm run demo
 */

const SEED = 0x5eed;

function section(title: string): void {
  console.log(`\n=== ${title} ${'='.repeat(Math.max(0, 60 - title.length - 5))}`);
}

// ---------------------------------------------------------------------------
section(`Part 1: split-brain race (seed=0x${SEED.toString(16)}, 10000 ticks)`);

const sim = new Simulator(SEED, { lossRate: 0.1, baseLatency: 1, jitter: 2 });
sim.net.setLink('A', 'B', { lossRate: 0, baseLatency: 1, jitter: 0 });
sim.addNode(new SwimNode('A', ['B']));
sim.addNode(new SwimNode('B', ['A']));
for (const id of ['C', 'D', 'E']) {
  sim.addNode(new SwimNode(id, ['C', 'D', 'E'].filter((p) => p !== id)));
}

console.log('t=0      5 nodes up. {A,B} clean link, {C,D,E} lossy (10% loss, jitter 0-2).');
sim.run(1000);
console.log('t=1000   PARTITION A<->B (hard isolation, 100% loss both ways)');
sim.net.isolate('A', 'B');
sim.run(1000);
console.log('t=2000   HEAL A<->B');
sim.net.heal('A', 'B');
sim.run(8000);
console.log('t=10000  done.\n');

const raceLines = sim.logLines.filter(
  (l) => / [AB] (suspect|declare-dead|alive) [AB] /.test(l) || / [AB] refute /.test(l),
);
console.log('race events (A/B pair):');
for (const line of raceLines) console.log('  ' + line);

console.log('\nfinal membership views:');
for (const [id, node] of [...sim.nodes].sort()) {
  const view = Object.entries(node.view())
    .map(([peer, m]) => `${peer}:${m.status}@${m.incarnation}`)
    .join('  ');
  console.log(`  ${id} (inc=${node.ownIncarnation}) -> ${view}`);
}
console.log(`\nevent log: ${sim.logLines.length} lines, fnv1a=${sim.logHash()}`);

// ---------------------------------------------------------------------------
section('Part 2: replay proof (same seed, same hash)');
const replay = new Simulator(SEED, { lossRate: 0.1, baseLatency: 1, jitter: 2 });
replay.net.setLink('A', 'B', { lossRate: 0, baseLatency: 1, jitter: 0 });
replay.addNode(new SwimNode('A', ['B']));
replay.addNode(new SwimNode('B', ['A']));
for (const id of ['C', 'D', 'E']) {
  replay.addNode(new SwimNode(id, ['C', 'D', 'E'].filter((p) => p !== id)));
}
replay.run(1000);
replay.net.isolate('A', 'B');
replay.run(1000);
replay.net.heal('A', 'B');
replay.run(8000);
console.log(`run 1 hash: ${sim.logHash()}`);
console.log(`run 2 hash: ${replay.logHash()}`);
console.log(sim.logHash() === replay.logHash() ? 'IDENTICAL - fully deterministic' : 'MISMATCH!');

// ---------------------------------------------------------------------------
section('Part 3: crash detection (4 nodes, D crashes at t=500)');
const sim2 = new Simulator(2024);
for (const id of ['A', 'B', 'C', 'D']) {
  sim2.addNode(new SwimNode(id, ['A', 'B', 'C', 'D'].filter((p) => p !== id)));
}
sim2.run(500);
sim2.nodes.get('D')!.stop();
console.log('t=500    D crashes (stops responding)');
sim2.run(500);
for (const line of sim2.logLines.filter((l) => !l.includes(' drop '))) {
  console.log('  ' + line);
}
for (const id of ['A', 'B', 'C']) {
  console.log(`  ${id} sees D as: ${sim2.nodes.get(id)!.view()['D'].status}`);
}
