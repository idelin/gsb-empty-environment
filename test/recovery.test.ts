import test from 'node:test';
import assert from 'node:assert/strict';
import { WatershedEngine } from '../src/engine.ts';
import { segmentReference } from '../src/reference.ts';
import { mulberry32 } from './helpers.ts';

function buildScenario(seed: number): { eng: WatershedEngine; ops: (() => void)[] } {
  const rng = mulberry32(seed);
  const w = 20, h = 14;
  const heights = Int32Array.from({ length: w * h }, () => (rng() * 4) | 0);
  const eng = new WatershedEngine(w, h, heights, { tileSize: 4 });
  const ops: (() => void)[] = [];
  const ids = ['A', 'B', 'C'];
  for (const id of ids) {
    const x = (rng() * w) | 0, y = (rng() * h) | 0;
    ops.push(() => eng.addSeed(id, x, y, { budget: 3 }));
  }
  for (let i = 0; i < 6; i++) {
    const edits = Array.from({ length: 2 }, () => ({
      x: (rng() * w) | 0, y: (rng() * h) | 0, h: (rng() * 4) | 0,
    }));
    ops.push(() => eng.setHeights(edits, { budget: 3 }));
  }
  ops.push(() => eng.removeSeed('B', { budget: 3 }));
  ops.push(() => eng.moveSeed('C', 1, 1, { budget: 3 }));
  return { eng, ops };
}

test('budget interruption never publishes a half-labeled map', () => {
  const { eng, ops } = buildScenario(777);
  for (const op of ops) {
    const committedBefore = eng.getLabels();
    const versionBefore = eng.version;
    op();
    let pauses = 0;
    while (eng.paused) {
      pauses++;
      assert.equal(eng.version, versionBefore, 'version must not bump mid-job');
      assert.deepEqual(
        [...eng.getLabels()], [...committedBefore],
        'while paused, queries must see the last fully labeled version',
      );
      eng.step(2); // trickle a tiny budget
      if (pauses > 100000) throw new Error('job did not converge');
    }
    assert.ok(pauses > 0, 'budget=3 must actually pause on a 20x14 map');
  }
  const ref = segmentReference(eng.width, eng.height, eng.getHeights(), eng.getSeeds());
  assert.deepEqual([...eng.getLabels()], [...ref.labels]);
});

test('crash-recovery fuzz: serialize at every pause, resume from the journal', () => {
  // Uninterrupted run for per-op expected snapshots.
  const twin = buildScenario(888);
  const expectedPerOp: Int32Array[] = [];
  for (const op of twin.ops) {
    op();
    while (twin.eng.paused) twin.eng.step();
    expectedPerOp.push(twin.eng.getLabels());
  }
  // Same op sequence, but every pause is serialized and recovered before resuming.
  const { eng, ops } = buildScenario(888);
  ops.forEach((op, i) => {
    op();
    let recoveries = 0;
    while (eng.paused) {
      const recovered = WatershedEngine.deserialize(eng.serialize());
      assert.ok(recovered.paused, 'paused job must survive the journal round-trip');
      recovered.step(); // finish the job on the recovered copy
      assert.deepEqual(
        [...recovered.getLabels()], [...expectedPerOp[i]],
        `recovered mid-op#${i} must converge to the uninterrupted result`,
      );
      recoveries++;
      eng.step(2);
      if (recoveries > 100000) throw new Error('job did not converge');
    }
    assert.deepEqual([...eng.getLabels()], [...expectedPerOp[i]], `op#${i} final`);
  });
  const ref = segmentReference(eng.width, eng.height, eng.getHeights(), eng.getSeeds());
  assert.deepEqual([...eng.getLabels()], [...ref.labels]);
});

test('recovered engine preserves version history and old-version queries', () => {
  const { eng, ops } = buildScenario(999);
  for (const op of ops) {
    op();
    while (eng.paused) eng.step(5);
  }
  const json = eng.serialize();
  const restored = WatershedEngine.deserialize(json);
  assert.equal(restored.version, eng.version);
  for (let v = 0; v <= eng.version; v++) {
    assert.deepEqual([...restored.getLabelsVersion(v)], [...eng.getLabelsVersion(v)], `version ${v}`);
    assert.deepEqual(restored.getEvents(v), eng.getEvents(v), `events v${v}`);
  }
  assert.deepEqual([...restored.getLabels()], [...eng.getLabels()]);
});
