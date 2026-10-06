import assert from 'node:assert/strict';
import { test } from 'node:test';
import { normalizeResult } from '../src/conservation.ts';
import { matrixPublish } from '../src/matrixModel.ts';
import { RevisionStacker } from '../src/revisions.ts';
import type { BatchRecord } from '../src/revisions.ts';
import type { StackMode } from '../src/types.ts';

test('column stays pending until every required series reports it', () => {
  const s = new RevisionStacker('absolute', ['a', 'b']);
  s.applyBatch('a', 1, [
    { time: 1, value: 3 },
    { time: 2, value: 1 },
  ]);
  let pub = s.publish();
  assert.deepEqual(pub.pendingTimes, [1, 2]);
  assert.deepEqual(pub.result.times, []);

  s.applyBatch('b', 1, [{ time: 1, value: 1 }]);
  pub = s.publish();
  assert.deepEqual(pub.result.times, [1]);
  assert.deepEqual(pub.pendingTimes, [2]);
  assert.equal(pub.barriers.get(1), 1);
});

test('percent baseline never uses half-column new data', () => {
  const s = new RevisionStacker('percent', ['a', 'b']);
  s.applyBatch('a', 1, [{ time: 1, value: 3 }]);
  s.applyBatch('b', 1, [{ time: 1, value: 1 }]);
  s.applyBatch('a', 2, [{ time: 1, value: 9 }]);

  let pub = s.publish();
  assert.equal(pub.barriers.get(1), 1, 'barrier held back by b@1');
  let [pa] = pub.result.columns[0].points;
  assert.equal(pa.value, 3, 'a is read as-of barrier 1, not its newer rev 2');
  assert.ok(Math.abs(pa.upper - 0.75) < 1e-12);

  s.applyBatch('b', 2, [{ time: 1, value: 1 }]);
  pub = s.publish();
  assert.equal(pub.barriers.get(1), 2);
  [pa] = pub.result.columns[0].points;
  assert.equal(pa.value, 9);
  assert.ok(Math.abs(pa.upper - 0.9) < 1e-12);
});

test('out-of-order batch arrival across series converges to the same barrier', () => {
  const s = new RevisionStacker('absolute', ['a', 'b']);
  s.applyBatch('b', 2, [{ time: 1, value: 10 }]);
  s.applyBatch('a', 2, [{ time: 1, value: 4 }]);
  s.applyBatch('b', 1, [{ time: 1, value: 1 }]);
  s.applyBatch('a', 1, [{ time: 1, value: 2 }]);
  const pub = s.publish();
  assert.equal(pub.barriers.get(1), 2);
  const [pa, pb] = pub.result.columns[0].points;
  assert.deepEqual([pa.lower, pa.upper], [0, 4]);
  assert.deepEqual([pb.lower, pb.upper], [4, 14]);
});

test('retracting a series recomputes layers and emits a replacement version', () => {
  const s = new RevisionStacker('percent', ['a', 'b']);
  s.applyBatch('a', 1, [{ time: 1, value: 3 }]);
  s.applyBatch('b', 1, [{ time: 1, value: 1 }]);
  const before = s.publish();
  assert.equal(before.result.seriesIds.length, 2);

  const replacement = s.retractSeries('b');
  assert.ok(replacement.version > before.version, 'replacement bumps version');
  assert.deepEqual(replacement.result.seriesIds, ['a']);
  const [pa] = replacement.result.columns[0].points;
  assert.ok(Math.abs(pa.upper - 1) < 1e-12, 'a renormalized to the full column');

  assert.throws(() => s.applyBatch('b', 2, [{ time: 1, value: 5 }]), /retracted/);
});

test('hidden series is not required for the barrier; unhide recovers it', () => {
  const s = new RevisionStacker('absolute', ['a', 'b']);
  s.applyBatch('a', 1, [
    { time: 1, value: 2 },
    { time: 2, value: 3 },
  ]);
  s.applyBatch('b', 1, [{ time: 1, value: 5 }]);

  let pub = s.publish();
  assert.deepEqual(pub.pendingTimes, [2], 'b has not reported t2');

  s.hideSeries('b');
  pub = s.publish();
  assert.deepEqual(pub.pendingTimes, [], 'hidden series no longer gates t2');
  assert.deepEqual(pub.result.seriesIds, ['a']);
  assert.deepEqual(pub.result.times, [1, 2]);

  s.unhideSeries('b');
  pub = s.publish();
  assert.deepEqual(pub.result.seriesIds, ['a', 'b'], 'unhidden series restored');
  assert.deepEqual(pub.pendingTimes, [2], 'barrier again waits for b at t2');

  s.applyBatch('b', 2, [{ time: 2, value: 7 }]);
  pub = s.publish();
  assert.deepEqual(pub.pendingTimes, [], 't2 still publishes, at barrier 1');
  assert.equal(pub.barriers.get(2), 1, 'a only reached rev 1 at t2');
  assert.equal(
    pub.result.columns[1].points[1].missing,
    true,
    'b@2 data withheld until a catches up',
  );

  s.applyBatch('a', 2, [{ time: 2, value: 3 }]);
  pub = s.publish();
  assert.deepEqual(pub.pendingTimes, [], 'barrier reached once a also hits rev 2');
  const t2 = pub.result.columns[1];
  assert.deepEqual([t2.points[1].lower, t2.points[1].upper], [3, 10]);
});

test('duplicate revision and unknown series are rejected', () => {
  const s = new RevisionStacker('absolute', ['a']);
  s.applyBatch('a', 1, [{ time: 1, value: 1 }]);
  assert.throws(() => s.applyBatch('a', 1, [{ time: 1, value: 2 }]), /duplicate/);
  assert.throws(() => s.applyBatch('zzz', 1, []), /not a required series/);
});

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

test('fuzz: randomized event stream matches the full matrix model', () => {
  const modes: StackMode[] = ['absolute', 'percent'];
  for (const mode of modes) {
    for (const seed of [1, 2, 3, 4, 5]) {
      const rng = mulberry32(seed);
      const ids = ['a', 'b', 'c', 'd'];
      const times = [1, 2, 3, 4, 5, 6];
      const stacker = new RevisionStacker(mode, ids);
      const liveBatches: BatchRecord[] = [];
      const required = [...ids];
      const hidden = new Set<string>();
      const retracted = new Set<string>();
      const nextRevision = new Map(ids.map((id) => [id, 1]));

      const activeIds = () => required.filter((id) => !retracted.has(id));

      for (let step = 0; step < 80; step += 1) {
        const roll = rng();
        const active = activeIds();
        if (roll < 0.55 && active.length > 0) {
          const id = active[Math.floor(rng() * active.length)];
          const revision = nextRevision.get(id)!;
          nextRevision.set(id, revision + 1);
          const points = times
            .filter(() => rng() < 0.7)
            .map((time) => {
              const r = rng();
              const value =
                r < 0.15 ? null : r < 0.3 ? 0 : Math.round(rng() * 40 - 20);
              return { time, value };
            });
          stacker.applyBatch(id, revision, points);
          liveBatches.push({ seriesId: id, revision, points });
        } else if (roll < 0.7 && active.length > 0) {
          const id = active[Math.floor(rng() * active.length)];
          stacker.hideSeries(id);
          hidden.add(id);
        } else if (roll < 0.8 && hidden.size > 0) {
          const id = [...hidden][Math.floor(rng() * hidden.size)];
          stacker.unhideSeries(id);
          hidden.delete(id);
        } else if (roll < 0.9 && active.length > 1) {
          const id = active[Math.floor(rng() * active.length)];
          stacker.retractSeries(id);
          retracted.add(id);
          hidden.delete(id);
          required.splice(required.indexOf(id), 1);
        }

        const pub = stacker.publish();
        const expected = matrixPublish({
          mode,
          required,
          hidden: [...hidden],
          batches: liveBatches.filter((b) => !retracted.has(b.seriesId)),
        });
        assert.deepEqual(
          normalizeResult(pub.result),
          normalizeResult(expected.result),
          `${mode} seed=${seed} step=${step} result`,
        );
        assert.deepEqual(
          pub.barriers,
          expected.barriers,
          `${mode} seed=${seed} step=${step} barriers`,
        );
        assert.deepEqual(
          pub.pendingTimes,
          expected.pendingTimes,
          `${mode} seed=${seed} step=${step} pending`,
        );
      }
    }
  }
});
