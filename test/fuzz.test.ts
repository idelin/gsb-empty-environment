import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { SafetyOracle } from '../src/invariants.ts';
import { IDS } from './helpers.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface FuzzResult {
  digest: string;
  violations: string[];
}

/**
 * Deterministic interleaving fuzz: random link cuts, heals and client writes
 * against whichever node currently leads. Safety invariants are checked after
 * every single tick; at the end the cluster must fully reconverge with no
 * leaked messages and exactly one stable leader.
 */
function runFuzz(seed: number): FuzzResult {
  const cluster = new Cluster(IDS, {});
  const oracle = new SafetyOracle();
  const rnd = mulberry32(seed);
  const violations: string[] = [];
  let writes = 0;

  for (let t = 0; t < 1200; t++) {
    const roll = rnd();
    if (roll < 0.06) {
      const a = IDS[Math.floor(rnd() * IDS.length)]!;
      const b = IDS[Math.floor(rnd() * IDS.length)]!;
      if (a !== b) cluster.network.cut(a, b);
    } else if (roll < 0.1) {
      cluster.healAll();
    } else if (roll < 0.25) {
      const leader = cluster.leader();
      if (leader !== undefined) cluster.write(leader.id, `w${writes++}`);
    }
    cluster.tick();
    violations.push(...oracle.check(cluster));
  }

  cluster.healAll();
  for (let t = 0; t < 500; t++) {
    cluster.tick();
    violations.push(...oracle.check(cluster));
  }

  const reference = JSON.stringify(cluster.nodes[0]!.applied);
  for (const n of cluster.nodes) {
    if (JSON.stringify(n.applied) !== reference) {
      violations.push(`convergence: ${n.id} diverged after healing`);
    }
  }
  const maxInFlight = 2 * (IDS.length - 1); // one heartbeat round
  if (cluster.network.pending > maxInFlight) {
    violations.push(`leak: ${cluster.network.pending} in-flight messages exceed ${maxInFlight}`);
  }
  if (cluster.leaders().length !== 1) violations.push('leak: no stable single leader');
  for (const n of cluster.nodes) {
    if (n.drainOutbox().length !== 0) violations.push(`leak: ${n.id} has an undrained outbox`);
  }

  const digest = JSON.stringify(cluster.nodes.map((n) => [n.id, n.role, n.applied]));
  return { digest, violations };
}

test('fuzz: safety invariants hold under random partitions and writes', () => {
  for (const seed of [1, 7, 42, 1337, 2024]) {
    const { violations } = runFuzz(seed);
    assert.deepEqual(violations, [], `seed ${seed}`);
  }
});

test('determinism: identical seeds produce identical final states', () => {
  assert.equal(runFuzz(99).digest, runFuzz(99).digest);
  assert.equal(runFuzz(7).digest, runFuzz(7).digest);
});
