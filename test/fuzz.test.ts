import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { assertNoLeaks, mulberry32,snapshot} from './helpers.ts';

/**
 * Concurrency-safety harness: drives adversarial interleavings (elections,
 * proposals, partitions, isolations, jittered delivery) through the
 * deterministic event queue. Safety is checked after every single tick.
 */
function driveOps(c: Cluster, rng: () => number, ops: number, checkEveryTick: boolean): void {
  c.delayFn = () => 1 + Math.floor(rng() * 3);
  for (let i = 0; i < ops; i++) {
    const op = Math.floor(rng() * 6);
    switch (op) {
      case 0:
        c.elect(Math.floor(rng() * c.size));
        break;
      case 1:
        c.propose(Math.floor(rng() * c.size), `v${i}`);
        break;
      case 2: {
        const ids = [0, 1, 2, 3, 4].sort(() => rng() - 0.5);
        const k = 1 + Math.floor(rng() * 3);
        c.partition(ids.slice(0, k), ids.slice(k));
        break;
      }
      case 3:
        c.heal();
        break;
      case 4:
        c.wait(1 + Math.floor(rng() * 5));
        break;
      case 5:
        c.isolate(Math.floor(rng() * c.size));
        break;
    }
    const steps = 1 + Math.floor(rng() * 6);
    for (let s = 0; s < steps; s++) {
      if (!c.tick()) break;
      if (checkEveryTick) c.checkSafety();
    }
  }
}

function convergeFully(c: Cluster): void {
  c.heal();
  c.delayFn = null;
  for (const id of [0, 1, 2, 3, 4]) {
    c.elect(id);
    c.run();
    c.checkSafety();
    if (c.leaderIds().length === 1) break;
  }
  assert.equal(c.leaderIds().length, 1, 'a leader must emerge once the network heals');
  const leader = c.leaderIds()[0];
  assert.equal(c.propose(leader, 'final').ok, true);
  c.run();
  c.checkSafety();
  const reference = c.nodes[leader].committedValues();
  assert.ok(reference.length > 0 && reference[reference.length - 1] === 'final');
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), reference, `node ${n.id} diverged after convergence`);
  }
  assertNoLeaks(c);
}

test('fuzz: seeded adversarial interleavings never violate safety', () => {
  for (const seed of [1, 7, 42, 1337]) {
    const c = new Cluster(5);
    driveOps(c, mulberry32(seed), 300, true);
    convergeFully(c);
  }
});

test('fuzz: the simulation is bit-for-bit deterministic', () => {
  const run = () => {
    const c = new Cluster(5);
    driveOps(c, mulberry32(99), 250, false);
    c.heal();
    c.delayFn = null;
    for (const id of [0, 1, 2, 3, 4]) {
      c.elect(id);
      c.run();
      if (c.leaderIds().length === 1) break;
    }
    c.run();
    return c;
  };
  const a = run();
  const b = run();
  assert.deepEqual(a.delivered, b.delivered, 'same seed => identical message trace');
  assert.deepEqual(snapshot(a), snapshot(b), 'same seed => identical final state');
  assert.deepEqual(a.stats, b.stats);
  assertNoLeaks(a);
  assertNoLeaks(b);
});

test('fuzz: rapid leader churn keeps a single committed prefix', () => {
  const c = new Cluster(5);
  const rng = mulberry32(2024);
  for (let round = 0; round < 30; round++) {
    c.elect(Math.floor(rng() * 5));
    for (let s = 0; s < 4; s++) {
      if (!c.tick()) break;
      c.checkSafety();
    }
    const leaders = c.leaderIds();
    if (leaders.length === 1) c.propose(leaders[0], `r${round}`);
    for (let s = 0; s < 4; s++) {
      if (!c.tick()) break;
      c.checkSafety();
    }
  }
  convergeFully(c);
});
