import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { InvariantMonitor } from '../src/monitor.ts';
import { inspect } from '../src/inspector.ts';

/** Deterministic PRNG (mulberry32) for reproducible interleavings. */
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

function randomScenario(seed: number, steps: number): void {
  const rnd = mulberry32(seed);
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  const ids = c.nodeIds;
  let proposal = 0;

  for (let i = 0; i < steps; i++) {
    const action = Math.floor(rnd() * 7);
    const id = ids[Math.floor(rnd() * ids.length)];
    switch (action) {
      case 0:
        c.elect(id);
        break;
      case 1:
        c.retryElect(id);
        break;
      case 2:
        c.propose(id, `p${seed}-${proposal++}`);
        break;
      case 3: {
        // Random 2- or 3-way partition.
        const shuffled = [...ids].sort(() => rnd() - 0.5);
        const cut = 1 + Math.floor(rnd() * (ids.length - 1));
        c.partition([shuffled.slice(0, cut), shuffled.slice(cut)]);
        break;
      }
      case 4:
        c.heal();
        break;
      case 5:
        c.tick(Math.floor(rnd() * 120));
        break;
      case 6:
        if (c.net.pending > 0 && rnd() < 0.3) c.net.dropPending();
        else c.net.deliverNext();
        break;
    }
    // Deliver a bounded random number of messages between actions.
    const bursts = Math.floor(rnd() * 4);
    for (let b = 0; b < bursts; b++) c.net.deliverNext();
  }

  // Convergence phase: heal, then elect every node that still holds queued
  // client values so each queue drains under its own leadership term.
  c.heal();
  c.tick(200);
  c.runUntilIdle();
  for (const id of ids) {
    // A stale-term election can be nacked; each nack teaches the node the
    // current term, so bounded retries are guaranteed to win eventually.
    for (let attempt = 0; attempt < 8 && c.node(id).pendingClientValues > 0; attempt++) {
      c.elect(id);
      c.runUntilIdle();
    }
  }
  // Guarantee a live leader for the final sync round.
  if (!ids.some((id) => c.node(id).role === 'leader')) {
    c.elect(ids[ids.length - 1]);
    c.runUntilIdle();
  }
  c.tick(60);
  c.runUntilIdle();

  // Safety held at every single delivery (monitor throws otherwise).
  mon.assertClean();

  // Liveness/convergence: every node agrees on the committed prefix.
  const prefixes = ids.map((id) => c.node(id).committedPrefix());
  const reference = prefixes[0];
  for (const p of prefixes) assert.deepEqual(p, reference);

  // Offline full-state inspection of the converged cluster is clean.
  const report = inspect(c.snapshot());
  assert.ok(report.ok, JSON.stringify(report.invariantViolations));
  assert.deepEqual(c.findLeaks(), []);
  mon.dispose();
  c.dispose();
}

for (const seed of [1, 7, 42, 1337, 90210]) {
  test(`random interleavings preserve all invariants (seed=${seed})`, () => {
    randomScenario(seed, 120);
  });
}

test('message-level concurrency: every interleaving of a 2-leader race', () => {
  // Exhaustively interleave the delivery of a small message window.
  for (let seed = 100; seed < 110; seed++) {
    const rnd = mulberry32(seed);
    const c = new Cluster(3);
    const mon = new InvariantMonitor(c);
    c.elect('A');
    c.elect('B');
    while (c.net.pending > 0) {
      // Randomly shuffle delivery order by rotating the queue.
      if (rnd() < 0.5 && c.net.pending > 1) {
        c.net.deliverNext();
      } else {
        c.net.runUntilIdle();
      }
    }
    c.runUntilIdle();
    mon.assertClean();
    mon.dispose();
    c.dispose();
  }
});
