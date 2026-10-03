import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Simulator } from '../src/simulator.ts';
import { SwimNode } from '../src/swim/node.ts';

/**
 * Acceptance scenario: 10 000 discrete clock steps from a fixed seed.
 *
 *  - Cluster {A, B}: clean link. At t=1000 the A<->B link is hard
 *    partitioned. Both nodes keep probing, both fail, and at the exact same
 *    tick each declares the other dead (split-brain race). At t=2000 the
 *    partition heals; each side then receives gossip claiming *itself* is
 *    dead, and the arbitration code must deterministically refute it.
 *  - Cluster {C, D, E}: background chatter over a lossy, jittery link to
 *    keep the RNG stream and event queue busy.
 */

const SEED = 0x5eed;
const TOTAL_STEPS = 10_000;
const PARTITION_AT = 1_000;
const HEAL_AT = 2_000;

function buildSimulation(seed: number): Simulator {
  const sim = new Simulator(seed, { lossRate: 0.1, baseLatency: 1, jitter: 2 });
  sim.net.setLink('A', 'B', { lossRate: 0, baseLatency: 1, jitter: 0 });

  sim.addNode(new SwimNode('A', ['B']));
  sim.addNode(new SwimNode('B', ['A']));
  for (const id of ['C', 'D', 'E']) {
    sim.addNode(new SwimNode(id, ['C', 'D', 'E'].filter((p) => p !== id)));
  }
  return sim;
}

function runScenario(seed: number): Simulator {
  const sim = buildSimulation(seed);
  sim.run(PARTITION_AT); // ticks 1..1000: healthy
  sim.net.isolate('A', 'B'); // hard partition from t=1000 on
  sim.run(HEAL_AT - PARTITION_AT); // ticks 1001..2000: split brain
  sim.net.heal('A', 'B'); // partition heals at t=2000
  sim.run(TOTAL_STEPS - HEAL_AT); // ticks 2001..10000: arbitration + steady state
  return sim;
}

function tickOf(lines: string[], pattern: RegExp): number {
  const line = lines.find((l) => pattern.test(l));
  assert.ok(line, `expected log line matching ${pattern}`);
  return Number(line.match(/t=(\d+)/)![1]);
}

test('split-brain race: A and B declare each other dead at the same tick', () => {
  const sim = runScenario(SEED);
  assert.equal(sim.now, TOTAL_STEPS, 'scenario must span exactly 10000 clock steps');

  const aDeclaresB = tickOf(sim.logLines, /A declare-dead B/);
  const bDeclaresA = tickOf(sim.logLines, /B declare-dead A/);
  assert.equal(aDeclaresB, bDeclaresA, 'both nodes must declare each other dead at the SAME tick');

  // Arbitration: after the heal, both race nodes refute their alleged death
  // exactly once. (The lossy background cluster may refute its own transient
  // false positives; those are filtered out here.)
  const refutes = sim.logLines.filter((l) => / [AB] refute dead /.test(l));
  assert.deepEqual(
    refutes.map((l) => l.replace(/t=\d+ /, '')).sort(),
    ['A refute dead inc=2', 'B refute dead inc=2'],
  );
  for (const line of refutes) {
    const t = Number(line.match(/t=(\d+)/)![1]);
    assert.ok(t > HEAL_AT, `refutation must happen after the heal: ${line}`);
  }
});

test('arbitration converges deterministically to both-alive', () => {
  const sim = runScenario(SEED);
  const a = sim.nodes.get('A')!;
  const b = sim.nodes.get('B')!;
  assert.equal(a.ownIncarnation, 2);
  assert.equal(b.ownIncarnation, 2);
  assert.deepEqual(a.view(), { B: { incarnation: 2, status: 'alive' } });
  assert.deepEqual(b.view(), { A: { incarnation: 2, status: 'alive' } });

  // Background cluster: transient suspects are expected on a 10%-loss link,
  // but arbitration must never let a false death stick.
  for (const id of ['C', 'D', 'E']) {
    for (const m of Object.values(sim.nodes.get(id)!.view())) {
      assert.notEqual(m.status, 'dead', `${id} has a stuck false positive`);
    }
  }
});

test('fixed seed reproduces the run bit-for-bit (10000 steps, twice)', () => {
  const first = runScenario(SEED);
  const second = runScenario(SEED);
  assert.deepEqual(first.logLines, second.logLines);
  assert.equal(first.logHash(), second.logHash());
});

test('event log matches the recorded golden fingerprint', () => {
  const sim = runScenario(SEED);
  assert.equal(sim.logHash(), 'f64e616c');
});
