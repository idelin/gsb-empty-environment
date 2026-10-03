import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { mulberry32 } from '../src/network.ts';
import type { Command } from '../src/types.ts';

function runFuzz(seed: number): string {
  const c = new Cluster(5, { seed, leaseDuration: 10 });
  const rand = mulberry32(seed * 1000 + 7);
  let seq = 0;

  for (let round = 0; round < 150; round++) {
    const action = rand();
    if (action < 0.3) {
      c.nodes[Math.floor(rand() * 5)].startElection();
    } else if (action < 0.65) {
      const cmd: Command = { kind: 'set', key: `k${seq}`, value: `v${seq}` };
      seq++;
      c.submit(Math.floor(rand() * 5), cmd);
    } else if (action < 0.85) {
      const ids = [0, 1, 2, 3, 4];
      for (let i = ids.length - 1; i > 0; i--) {
        const j = Math.floor(rand() * (i + 1));
        [ids[i], ids[j]] = [ids[j], ids[i]];
      }
      const cut = 1 + Math.floor(rand() * 4);
      c.partition(ids.slice(0, cut), ids.slice(cut));
    } else {
      c.heal();
    }
    const ticks = 1 + Math.floor(rand() * 5);
    for (let t = 0; t < ticks; t++) c.network.tick();
    // Safety invariant checked after every single delivery round.
    c.assertConverged();
  }

  // Final: heal, elect deterministically, commit a marker, verify convergence.
  c.heal();
  c.stabilize();
  let leader = -1;
  for (const id of [0, 1, 2, 3, 4]) {
    c.nodes[id].startElection();
    c.stabilize();
    if (c.nodes[id].getRole() === 'leader') {
      leader = id;
      break;
    }
  }
  assert.ok(leader >= 0, 'a leader must emerge after healing');
  c.submit(leader, { kind: 'set', key: 'final', value: 'marker' });
  c.stabilize();
  c.assertConverged();

  const reference = c.committedLog(0).map((e) => e.command);
  for (let i = 1; i < 5; i++) {
    assert.deepEqual(
      c.committedLog(i).map((e) => e.command),
      reference,
      `node ${i} must converge to the identical committed log`,
    );
  }
  assert.equal(c.inconsistencies.length, 0);
  assert.equal(c.network.pendingCount, 0, 'no leaked in-flight messages');
  return JSON.stringify(reference);
}

test('fuzz: randomized elections, partitions and concurrent submits never violate safety', () => {
  for (const seed of [1, 2, 3, 4, 5, 6, 7, 8]) {
    runFuzz(seed);
  }
});

test('fuzz: same seed produces identical results (deterministic, race-free)', () => {
  assert.equal(runFuzz(100), runFuzz(100));
});

test('resources: no timers, no pending messages, no leaked handles', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.submit(0, { kind: 'set', key: 'a', value: '1' });
  c.stabilize();
  assert.equal(c.network.pendingCount, 0);
  const timers = (process as unknown as { _getActiveTimers?: () => unknown[] })._getActiveTimers?.() ?? [];
  assert.equal(timers.length, 0, 'engine must not leave timers behind');
});
