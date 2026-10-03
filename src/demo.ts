import { Simulation } from './simulation';

declare const process: { exitCode?: number };

const GROUP_A = [1, 2, 3, 4, 5];
const GROUP_B = [6, 7, 8, 9, 10];

function printView(sim: Simulation, ids: number[]): void {
  for (const id of ids) {
    const n = sim.nodes.get(id)!;
    console.log(`  node-${id}: epoch=${n.epoch} clock=${n.clock} state=${n.state} members=[${n.memberIds()}]`);
  }
}

function main(): void {
  const sim = new Simulation({ seed: 42, logger: (line) => console.log(line) });

  console.log('=== Phase 1: initial 10-node cluster converges ===');
  sim.run(30);
  console.log(`t=${sim.now} converged=${sim.converged()}`);

  console.log('\n=== Phase 2: partition [1-5] | [6-10], each side evolves independently ===');
  sim.partition([GROUP_A, GROUP_B]);
  sim.run(300);
  console.log(`t=${sim.now} group A view (node-1): [${sim.nodes.get(1)!.memberIds()}]`);
  console.log(`t=${sim.now} group B view (node-6): [${sim.nodes.get(6)!.memberIds()}]`);
  const preHealMaxClock = Math.max(...[...sim.nodes.values()].map((n) => n.clock));
  console.log(`t=${sim.now} max lamport clock before heal: ${preHealMaxClock}`);

  console.log('\n=== Phase 3: heal network; first cross-group PingReq triggers merge ===');
  sim.heal();
  const ok = sim.runUntil(() => sim.converged(), 2000);
  const first = sim.stats.firstCrossGroupSuccess!;
  console.log(`first cross-group success: ${first.kind} (ack ${first.from} -> ${first.to}) at t=${first.tick}`);
  console.log(`merge handshake: proposals=${sim.stats.mergeProposals} accepts=${sim.stats.mergeAccepts} commits=${sim.stats.mergeCommits}`);
  console.log(`converged=${ok} at t=${sim.now}`);

  console.log('\n=== Final state ===');
  printView(sim, [...GROUP_A, ...GROUP_B]);
  const clocks = [...sim.nodes.values()].map((n) => n.clock);
  console.log(`clock correction: all clocks >= pre-heal max (${preHealMaxClock}): ${Math.min(...clocks) >= preHealMaxClock}`);
  if (!ok) process.exitCode = 1;
}

main();
