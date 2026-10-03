import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Simulator } from '../src/simulator.ts';
import { SwimNode } from '../src/swim/node.ts';

function buildCluster(seed: number, ids: string[]): Simulator {
  const sim = new Simulator(seed);
  for (const id of ids) {
    sim.addNode(new SwimNode(id, ids.filter((p) => p !== id)));
  }
  return sim;
}

test('cluster detects a crashed node and converges', () => {
  const ids = ['A', 'B', 'C', 'D'];
  const sim = buildCluster(2024, ids);
  sim.run(500);
  sim.nodes.get('D')!.stop();
  sim.run(500);

  for (const id of ['A', 'B', 'C']) {
    const view = sim.nodes.get(id)!.view();
    assert.equal(view['D'].status, 'dead', `${id} should see D as dead`);
    for (const other of ['A', 'B', 'C'].filter((x) => x !== id)) {
      assert.equal(view[other].status, 'alive', `${id} should see ${other} as alive`);
    }
  }
  assert.ok(
    sim.logLines.some((l) => l.includes('declare-dead D')),
    'expected a declare-dead event for D',
  );
});

test('healthy cluster produces no false positives', () => {
  const sim = buildCluster(7, ['A', 'B', 'C']);
  sim.run(2000);
  assert.equal(sim.logLines.filter((l) => !l.includes(' drop ')).length, 0);
  for (const id of ['A', 'B', 'C']) {
    for (const m of Object.values(sim.nodes.get(id)!.view())) {
      assert.equal(m.status, 'alive');
    }
  }
});

test('identical seeds replay the exact same event log', () => {
  const runOnce = () => {
    const sim = buildCluster(555, ['A', 'B', 'C', 'D']);
    sim.net.setLink('A', 'B', { lossRate: 0.2, baseLatency: 1, jitter: 3 });
    sim.run(3000);
    return sim.logLines;
  };
  assert.deepEqual(runOnce(), runOnce());
});
