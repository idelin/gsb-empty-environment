import test from 'node:test';
import assert from 'node:assert/strict';
import { WatershedEngine } from '../src/engine.ts';
import { mulberry32, assertMatchesReference } from './helpers.ts';

function busyEngine(seed = 11): { eng: WatershedEngine; w: number; h: number } {
  const rng = mulberry32(seed);
  const w = 24, h = 16;
  const heights = Int32Array.from({ length: w * h }, () => (rng() * 4) | 0);
  const eng = new WatershedEngine(w, h, heights, { tileSize: 4 });
  eng.addSeed('A', 2, 2);
  eng.addSeed('B', 21, 13);
  return { eng, w, h };
}

test('budget interruption never exposes a half-labeled map', () => {
  const { eng } = busyEngine();
  const v0 = eng.version;
  const snapshot = eng.getLabels();
  // Start a mutation with a tiny budget: the job pauses mid-flood.
  eng.setHeights([{ x: 12, y: 8, h: 0 }, { x: 13, y: 8, h: 0 }, { x: 11, y: 8, h: 0 }], { budget: 1 });
  let checks = 0;
  while (eng.paused) {
    assert.equal(eng.version, v0, 'version must not bump before commit');
    assert.deepEqual([...eng.getLabels()], [...snapshot], 'committed map must stay complete while paused');
    checks++;
    eng.step(1); // trickle budget
    if (checks > 100000) throw new Error('job did not converge');
  }
  assert.ok(checks > 0, 'job must actually have paused');
  assertMatchesReference(eng, 'after budgeted resume');
});

test('every pause point of a long job shows a consistent committed map', () => {
  const { eng } = busyEngine(23);
  eng.addSeed('C', 12, 8, { budget: 3 });
  const v0 = eng.version;
  const snapshot = eng.getLabels();
  while (eng.paused) {
    assert.equal(eng.version, v0);
    assert.deepEqual([...eng.getLabels()], [...snapshot]);
    eng.step(2);
  }
  assertMatchesReference(eng, 'seed C budgeted');
});

test('crash recovery: serialize mid-job, restore, resume to the same result', () => {
  const { eng } = busyEngine(31);
  // Uninterrupted reference run on a twin engine.
  const twin = WatershedEngine.deserialize(eng.serialize());
  twin.setHeights([{ x: 5, y: 5, h: 3 }, { x: 18, y: 10, h: 1 }]);
  // Interrupted run: pause mid-job, "crash", recover from the journal.
  eng.setHeights([{ x: 5, y: 5, h: 3 }, { x: 18, y: 10, h: 1 }], { budget: 7 });
  assert.ok(eng.paused);
  const journal = eng.serialize();
  const recovered = WatershedEngine.deserialize(journal);
  assert.ok(recovered.paused, 'paused job must survive serialization');
  recovered.step(); // finish
  assert.deepEqual([...recovered.getLabels()], [...twin.getLabels()]);
  assert.equal(recovered.version, twin.version);
  assertMatchesReference(recovered, 'recovered');
});

test('recovery from every pause point converges to the same map', () => {
  const { eng } = busyEngine(47);
  const finalTwin = WatershedEngine.deserialize(eng.serialize());
  finalTwin.removeSeed('B');
  const expected = finalTwin.getLabels();
  eng.removeSeed('B', { budget: 5 });
  let rounds = 0;
  for (;;) {
    if (!eng.paused) break;
    const restored = WatershedEngine.deserialize(eng.serialize());
    restored.step(5 + rounds); // resume with varying budgets
    if (!restored.paused) {
      assert.deepEqual([...restored.getLabels()], [...expected], `recovered at round ${rounds}`);
      return;
    }
    eng.step(5);
    rounds++;
    if (rounds > 10000) throw new Error('no convergence');
  }
});

test('mutations are rejected while a job is paused', () => {
  const { eng } = busyEngine();
  eng.setHeights([{ x: 0, y: 0, h: 2 }], { budget: 1 });
  assert.ok(eng.paused);
  assert.throws(() => eng.addSeed('X', 5, 5), /paused/);
  eng.step();
  assert.doesNotThrow(() => eng.addSeed('X', 5, 5));
});
