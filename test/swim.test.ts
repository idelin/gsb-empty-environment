import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { Simulator } from '../src/simulator.ts';
import { Network } from '../src/network.ts';
import { Random } from '../src/rng.ts';
import { SwimNode, arbitrate, type MemberRecord } from '../src/swim.ts';

const SEED = 0xc0ffee;
const TICKS = 10000;
const IDS = ['A', 'B', 'C', 'D', 'E'];

const OPTS = {
  protocolPeriod: 40,
  ackTimeout: 8,
  suspicionTimeout: 200,
  indirectPings: 2,
  targetSelection: 'round-robin' as const,
};

/**
 * 5-node cluster. At t=500 the link A<->B and every link between
 * {A,B} and {C,D,E} is cut, so A and B can only see each other --
 * through a dead link. At t=4000 everything heals.
 */
function buildCluster(seed: number) {
  const sim = new Simulator();
  const rng = new Random(seed);
  const network = new Network(sim, rng, { latency: 2, jitter: 1, lossRate: 0 });
  const nodes = new Map<string, SwimNode>();
  for (const id of IDS) {
    nodes.set(id, new SwimNode(id, IDS.filter((x) => x !== id), sim, network, rng, OPTS));
  }
  sim.scheduleAt(500, () => {
    network.cut('A', 'B');
    for (const x of ['A', 'B']) for (const y of ['C', 'D', 'E']) network.cut(x, y);
    sim.log('PARTITION_APPLIED');
  });
  sim.scheduleAt(4000, () => {
    network.heal('A', 'B');
    for (const x of ['A', 'B']) for (const y of ['C', 'D', 'E']) network.heal(x, y);
    sim.log('PARTITION_HEALED');
  });
  return { sim, nodes, network };
}

function hashLog(lines: string[]): string {
  return createHash('sha256').update(lines.join('\n')).digest('hex');
}

test('race: A and B declare each other dead at the same tick; arbitration converges', () => {
  const { sim, nodes } = buildCluster(SEED);
  sim.run(TICKS);

  // The extreme race: both declarations happen at the exact same tick.
  const deadBbyA = sim.eventLog.find((l) => l.includes('A DECLARE_DEAD B'));
  const deadAbyB = sim.eventLog.find((l) => l.includes('B DECLARE_DEAD A'));
  assert.ok(deadBbyA, 'A must declare B dead');
  assert.ok(deadAbyB, 'B must declare A dead');
  const tickOfB = Number(deadBbyA!.split(' ')[0]);
  const tickOfA = Number(deadAbyB!.split(' ')[0]);
  assert.equal(tickOfA, tickOfB, 'both declarations must land on the same tick');

  // Both falsely-accused nodes refute by bumping their incarnation.
  assert.ok(sim.eventLog.some((l) => l.includes('A REFUTE')), 'A refutes');
  assert.ok(sim.eventLog.some((l) => l.includes('B REFUTE')), 'B refutes');

  // Arbitration converges: all 5 nodes hold byte-identical tables...
  const snapshots = [...nodes.values()].map((n) => JSON.stringify(n.snapshot()));
  for (const snap of snapshots) assert.equal(snap, snapshots[0]);
  // ...and the final view is "everybody alive", incarnations bumped once.
  for (const rec of nodes.get('A')!.snapshot()) {
    assert.equal(rec.state, 'alive', `${rec.id} must be alive everywhere`);
    assert.equal(rec.incarnation, 1, `${rec.id} refuted exactly once`);
  }
});

test('deterministic replay: same seed reproduces the identical 10000-tick run', () => {
  const first = buildCluster(SEED);
  first.sim.run(TICKS);
  const second = buildCluster(SEED);
  second.sim.run(TICKS);

  assert.ok(first.sim.eventLog.length > 0);
  assert.equal(hashLog(second.sim.eventLog), hashLog(first.sim.eventLog));
  assert.equal(second.sim.now, TICKS);
  assert.deepEqual(
    [...second.nodes.values()].map((n) => n.snapshot()),
    [...first.nodes.values()].map((n) => n.snapshot()),
  );
});

test('arbitrate: total deterministic order over conflicting records', () => {
  const rec = (state: MemberRecord['state'], inc: number, by: string): MemberRecord => ({
    id: 'X',
    state,
    incarnation: inc,
    declaredBy: by,
  });

  // 1. Higher incarnation always wins (this is how self-refutation
  //    revives a falsely-declared-dead member).
  assert.equal(arbitrate(rec('dead', 5, 'A'), rec('alive', 6, 'X')).state, 'alive');
  assert.equal(arbitrate(rec('alive', 6, 'X'), rec('dead', 5, 'A')).state, 'alive');

  // 2. Equal incarnation: worse state wins (dead > suspect > alive).
  assert.equal(arbitrate(rec('alive', 3, 'X'), rec('suspect', 3, 'B')).state, 'suspect');
  assert.equal(arbitrate(rec('suspect', 3, 'B'), rec('dead', 3, 'C')).state, 'dead');

  // 3. Full tie: larger declaredBy wins -- symmetric, so every node
  //    resolves the identical conflict to the identical record.
  const ab = arbitrate(rec('dead', 2, 'A'), rec('dead', 2, 'B'));
  const ba = arbitrate(rec('dead', 2, 'B'), rec('dead', 2, 'A'));
  assert.equal(ab.declaredBy, 'B');
  assert.equal(ab.declaredBy, ba.declaredBy);
});

test('seeded network: loss and jitter are reproducible and match the profile', () => {
  const runOnce = (seed: number) => {
    const sim = new Simulator();
    const rng = new Random(seed);
    const net = new Network(sim, rng);
    net.setLink('a', 'b', { latency: 1, jitter: 2, lossRate: 0.3 });
    net.register('b', () => {});
    for (let i = 0; i < 10000; i++) net.send('a', 'b', i);
    sim.run(100000);
    return { ...net.stats };
  };

  const first = runOnce(7);
  const second = runOnce(7);
  assert.deepEqual(second, first, 'same seed => identical drop/delivery pattern');

  const lossRate = first.dropped / first.sent;
  assert.ok(lossRate > 0.27 && lossRate < 0.33, `loss rate ${lossRate} ~= 0.3`);
  assert.equal(first.delivered, first.sent - first.dropped);
});

test('healthy cluster: jitter and 2% loss cause no false positives in 10000 ticks', () => {
  const sim = new Simulator();
  const rng = new Random(1234);
  const network = new Network(sim, rng, { latency: 2, jitter: 2, lossRate: 0.02 });
  const nodes = new Map<string, SwimNode>();
  for (const id of IDS) {
    nodes.set(
      id,
      new SwimNode(id, IDS.filter((x) => x !== id), sim, network, rng, {
        ...OPTS,
        targetSelection: 'random',
      }),
    );
  }
  sim.run(TICKS);

  assert.equal(
    sim.eventLog.filter((l) => l.includes('DECLARE_DEAD')).length,
    0,
    'no node may be declared dead',
  );
  for (const node of nodes.values()) {
    for (const rec of node.snapshot()) assert.equal(rec.state, 'alive');
  }
});
